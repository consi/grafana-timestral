// Port of github.com/mistralai/timestral (Copyright 2026 Mistral AI, Apache-2.0), translated
// to Go and modified for inference in a Grafana plugin; see NOTICE.

package timestral

import (
	"context"
	"errors"
	"fmt"
	"math"
)

// Options mirrors the knobs of timestral.TimestralForecaster and its forecast() call.
type Options struct {
	// MaxContext caps the context in time steps (most recent kept); 0 = the trained context length.
	MaxContext int `json:"max_context,omitempty"`
	// PerShotHorizonPatches is the number of patches decoded per block, capped at the trained
	// block size; 0 = 32.
	PerShotHorizonPatches int `json:"per_shot_horizon_patches,omitempty"`
	// QuantileRealCapK clamps forecasts to [min - k*s, max + k*s] of the observed context;
	// nil = 1e4, 0 disables.
	QuantileRealCapK *float64 `json:"quantile_real_cap_k,omitempty"`
	// SymmetricAveraging averages with the reflected forecast of -context; nil = true.
	SymmetricAveraging *bool `json:"symmetric_averaging,omitempty"`
	// ClipNonnegative clamps at 0 the series whose observed context is non-negative; nil = true.
	ClipNonnegative *bool `json:"clip_nonnegative,omitempty"`
}

// Input is one forecast request. Every series has the same number of target variates and
// covariates; series may have different context lengths (they are left-padded).
// NaN marks a missing value everywhere.
type Input struct {
	Context          [][][]float64 // (B, V, T_b)
	PredictionLength int
	PastCovariates   [][][]float64 // (B, C_past, T_b) or nil
	KnownCovariates  [][][]float64 // (B, C_known, T_b + H) or nil
	CategoricalPast  [][][]float64 // (B, C, T_b) category codes or nil
	CategoricalKnown [][][]float64 // (B, C, T_b + H) category codes or nil
	Options          Options
}

// Forecast holds quantiles (B, V, H, Q) and their levels.
type Forecast struct {
	Quantiles      [][][][]float32
	QuantileLevels []float64
}

// Column returns the forecast of series b, variate v at one quantile level.
func (f *Forecast) Column(b, v int, level float64) ([]float32, error) {
	for i, l := range f.QuantileLevels {
		if math.Abs(l-level) < 1e-9 {
			steps := f.Quantiles[b][v]
			out := make([]float32, len(steps))
			for s, q := range steps {
				out[s] = q[i]
			}
			return out, nil
		}
	}
	return nil, fmt.Errorf("quantile %v is not one of the model's levels %v", level, f.QuantileLevels)
}

const extremeValueThreshold = 1e30

// batch is the dense (B, rows-per-series, T) layout the forecaster works on.
type batch struct {
	b, v, t int
	vals    [][][]float32 // [b][v][t], missing = 0
	obs     [][][]bool
}

func newBatch(b, v, t int) *batch {
	bt := &batch{b: b, v: v, t: t, vals: make([][][]float32, b), obs: make([][][]bool, b)}
	for i := 0; i < b; i++ {
		bt.vals[i] = make([][]float32, v)
		bt.obs[i] = make([][]bool, v)
		for j := 0; j < v; j++ {
			bt.vals[i][j] = make([]float32, t)
			bt.obs[i][j] = make([]bool, t)
		}
	}
	return bt
}

// rightAlign copies src (any length) into a row of length t aligned at the end; NaN = missing.
func rightAlign(src []float64, vals []float32, obs []bool) {
	off := len(vals) - len(src)
	for i, x := range src {
		j := off + i
		if j < 0 {
			continue
		}
		if !math.IsNaN(x) && !math.IsInf(x, 0) {
			vals[j] = float32(x)
			obs[j] = true
		}
	}
}

// Forecast runs the full Timestral forecaster (block decoding, refine frame, clampsym).
// It stops between decode blocks when ctx is cancelled.
func (m *Model) Forecast(ctx context.Context, in Input) (*Forecast, error) {
	cfg := m.Config
	h := in.PredictionLength
	if h < 1 {
		return nil, errors.New("prediction_length must be >= 1")
	}
	b := len(in.Context)
	if b == 0 {
		return nil, errors.New("no series")
	}
	v := len(in.Context[0])
	t := 0
	for i, s := range in.Context {
		if len(s) != v || v == 0 {
			return nil, fmt.Errorf("series %d: every series needs the same number (>0) of target variates", i)
		}
		for _, row := range s {
			t = max(t, len(row))
		}
	}
	if t == 0 {
		return nil, errors.New("empty context")
	}
	// Per series context length (all variates of a series share it).
	lengths := make([]int, b)
	for i, s := range in.Context {
		for _, row := range s {
			if lengths[i] != 0 && len(row) != lengths[i] {
				return nil, fmt.Errorf("series %d: target variates must have equal length", i)
			}
			lengths[i] = len(row)
		}
	}

	targets := newBatch(b, v, t)
	for i := range in.Context {
		for j, row := range in.Context[i] {
			rightAlign(row, targets.vals[i][j], targets.obs[i][j])
		}
	}

	countCov := func(name string, rows [][][]float64, extra int) (int, error) {
		if rows == nil {
			return 0, nil
		}
		if len(rows) != b {
			return 0, fmt.Errorf("%s needs one entry per series", name)
		}
		c := len(rows[0])
		for i, s := range rows {
			if len(s) != c {
				return 0, fmt.Errorf("%s: every series needs the same number of rows", name)
			}
			for _, r := range s {
				if len(r) != lengths[i]+extra {
					return 0, fmt.Errorf("%s: series %d rows must have length %d, got %d", name, i, lengths[i]+extra, len(r))
				}
			}
		}
		return c, nil
	}
	nPast, err := countCov("past_covariates", in.PastCovariates, 0)
	if err != nil {
		return nil, err
	}
	nKnown, err := countCov("known_covariates", in.KnownCovariates, h)
	if err != nil {
		return nil, err
	}
	nCatPast, err := countCov("categorical_past_covariates", in.CategoricalPast, 0)
	if err != nil {
		return nil, err
	}
	nCatKnown, err := countCov("categorical_known_covariates", in.CategoricalKnown, h)
	if err != nil {
		return nil, err
	}
	if nPast+nKnown+nCatPast+nCatKnown > 0 && !cfg.CovariateEnabled {
		return nil, errors.New("this checkpoint was not trained with covariates")
	}
	if nKnown+nCatKnown > 0 && !cfg.SupportsKnownCovariates() {
		return nil, errors.New("this checkpoint cannot condition on known-future covariates")
	}

	opts := m.resolveOptions(in.Options)

	// Covariates as float64 (B, C, len) with NaN for missing, right-aligned to t (+h).
	past := make([][][]float64, b)
	known := make([][][]float64, b)
	for i := 0; i < b; i++ {
		pad := t - lengths[i]
		for c := 0; c < nPast; c++ {
			past[i] = append(past[i], leftPadNaN(in.PastCovariates[i][c], pad))
		}
		for c := 0; c < nKnown; c++ {
			known[i] = append(known[i], leftPadNaN(in.KnownCovariates[i][c], pad))
		}
	}
	if nCatPast+nCatKnown > 0 {
		enc := m.encodingTargets(targets, lengths, opts.maxContext)
		for i := 0; i < b; i++ {
			pad := t - lengths[i]
			for c := 0; c < nCatPast; c++ {
				codes := leftPadNaN(in.CategoricalPast[i][c], pad)
				past[i] = append(past[i], encodeCodes(enc[i], codes, nil, t))
			}
			for c := 0; c < nCatKnown; c++ {
				codes := leftPadNaN(in.CategoricalKnown[i][c], pad)
				known[i] = append(known[i], encodeCodes(enc[i], codes[:t], codes[t:], t))
			}
		}
		nPast += nCatPast
		nKnown += nCatKnown
	}

	q, err := m.forecastRaw(ctx, targets, past, known, nPast, nKnown, h, opts, false)
	if err != nil {
		return nil, err
	}
	nq := cfg.numQuantiles()
	if opts.symmetric {
		mirrored, err := m.forecastRaw(ctx, targets, past, known, nPast, nKnown, h, opts, true)
		if err != nil {
			return nil, err
		}
		for i := range q {
			for j := range q[i] {
				for s := 0; s < h; s++ {
					a, mr := q[i][j][s], mirrored[i][j][s]
					out := make([]float32, nq)
					for k := 0; k < nq; k++ {
						out[k] = 0.5 * (a[k] + -mr[nq-1-k])
					}
					q[i][j][s] = out
				}
			}
		}
	}
	if opts.clipNonnegative {
		for i := 0; i < b; i++ {
			for j := 0; j < v; j++ {
				anyObs, nonneg := false, true
				for k, o := range targets.obs[i][j] {
					if o {
						anyObs = true
						nonneg = nonneg && targets.vals[i][j][k] >= 0
					}
				}
				if anyObs && nonneg {
					for _, step := range q[i][j] {
						for k := range step {
							step[k] = max(step[k], 0)
						}
					}
				}
			}
		}
	}
	return &Forecast{Quantiles: q, QuantileLevels: cfg.QuantileKnots}, nil
}

type resolvedOptions struct {
	maxContext, perShot int
	capK                float64
	symmetric           bool
	clipNonnegative     bool
}

func (m *Model) resolveOptions(o Options) resolvedOptions {
	r := resolvedOptions{maxContext: m.Config.ContextLength, perShot: 32, capK: 1e4, symmetric: true, clipNonnegative: true}
	if o.MaxContext > 0 {
		r.maxContext = min(o.MaxContext, m.Config.ContextLength)
	}
	if o.PerShotHorizonPatches > 0 {
		r.perShot = o.PerShotHorizonPatches
	}
	r.perShot = min(r.perShot, m.Config.BlockSize)
	if o.QuantileRealCapK != nil {
		r.capK = *o.QuantileRealCapK
	}
	if o.SymmetricAveraging != nil {
		r.symmetric = *o.SymmetricAveraging
	}
	if o.ClipNonnegative != nil {
		r.clipNonnegative = *o.ClipNonnegative
	}
	return r
}

func leftPadNaN(x []float64, pad int) []float64 {
	out := make([]float64, pad+len(x))
	for i := 0; i < pad; i++ {
		out[i] = math.NaN()
	}
	copy(out[pad:], x)
	return out
}

// encodingTargets returns per series the forward-filled target from its first observation on,
// over the last maxContext steps (NaN elsewhere), or nil when a series has several variates.
func (m *Model) encodingTargets(tg *batch, lengths []int, maxContext int) [][]float64 {
	out := make([][]float64, tg.b)
	if tg.v != 1 {
		return out
	}
	for i := 0; i < tg.b; i++ {
		vals, obs := tg.vals[i][0], tg.obs[i][0]
		row := make([]float64, tg.t)
		started := false
		last := 0.0
		for k := 0; k < tg.t; k++ {
			if obs[k] {
				started = true
				last = float64(vals[k])
			}
			if started && k >= tg.t-maxContext {
				row[k] = last
			} else {
				row[k] = math.NaN()
			}
		}
		out[i] = row
	}
	return out
}

// encodeCodes target-encodes codes (past part of length t, optional future) against target;
// raw codes are kept when there is no single target.
func encodeCodes(target, pastCodes, futureCodes []float64, t int) []float64 {
	if target == nil {
		out := append([]float64{}, pastCodes...)
		return append(out, futureCodes...)
	}
	past, future := targetEncode(target, pastCodes, futureCodes, 1)
	for k := range past {
		// A missing code before the target starts is padding, not the missing category.
		if math.IsNaN(target[k]) && math.IsNaN(pastCodes[k]) {
			past[k] = math.NaN()
		}
	}
	return append(past, future...)
}

// forecastRaw is one pass: assemble rows, decode block by block, cap. Returns (B, V, H, Q).
func (m *Model) forecastRaw(ctx context.Context, tg *batch, past, known [][][]float64, nPast, nKnown, h int, opts resolvedOptions, negate bool) ([][][][]float32, error) {
	cfg := m.Config
	ps, nq := cfg.PatchSize, cfg.numQuantiles()
	sign := float32(1)
	if negate {
		sign = -1
	}
	b, v := tg.b, tg.v
	vAll := v + nPast + nKnown
	horizon := ((h + ps - 1) / ps) * ps

	// Rows of every series: targets forward-filled, covariates zero-filled with their own masks.
	ctxLen := tg.t
	keep := min(ctxLen, opts.maxContext)
	start := ctxLen - keep
	t := ((keep + ps - 1) / ps) * ps
	pad := t - keep
	rows := b * vAll
	p := &packed{rows: rows, t: t, values: make([]float32, rows*t), observed: make([]bool, rows*t), groups: make([]int, rows)}
	hasCov := nPast+nKnown > 0
	if hasCov {
		p.roles = make([]int, rows)
	}
	var kf []float32
	var kfm []bool
	if nKnown > 0 {
		kf = make([]float32, rows*horizon)
		kfm = make([]bool, rows*horizon)
	}
	setRow := func(r int, vals []float32, obs []bool) {
		copy(p.values[r*t+pad:], vals[start:])
		copy(p.observed[r*t+pad:], obs[start:])
	}
	covRow := func(x []float64) ([]float32, []bool) {
		vals := make([]float32, len(x))
		obs := make([]bool, len(x))
		for k, f := range x {
			if !math.IsNaN(f) {
				vals[k] = sign * float32(f)
				obs[k] = true
			}
		}
		return vals, obs
	}
	for i := 0; i < b; i++ {
		for j := 0; j < v; j++ {
			r := i*vAll + j
			p.groups[r] = i
			vals := ffill(tg.vals[i][j], tg.obs[i][j])
			for k := range vals {
				vals[k] *= sign
			}
			setRow(r, vals, tg.obs[i][j])
		}
		for c := 0; c < nPast; c++ {
			r := i*vAll + v + c
			p.groups[r], p.roles[r] = i, RolePastCov
			vals, obs := covRow(past[i][c])
			setRow(r, vals, obs)
		}
		for c := 0; c < nKnown; c++ {
			r := i*vAll + v + nPast + c
			p.groups[r], p.roles[r] = i, RoleKnownCov
			vals, obs := covRow(known[i][c])
			setRow(r, vals[:ctxLen], obs[:ctxLen])
			copy(kf[r*horizon:], vals[ctxLen:])
			copy(kfm[r*horizon:], obs[ctxLen:])
		}
	}

	quant, err := m.blockDecode(ctx, p, h, kf, kfm, horizon, opts)
	if err != nil {
		return nil, err
	}

	// Cap at [min - k*s, max + k*s] of the observed (assembled) target context.
	out := make([][][][]float32, b)
	for i := 0; i < b; i++ {
		out[i] = make([][][]float32, v)
		for j := 0; j < v; j++ {
			r := i*vAll + j
			vals, obs := p.row(r)
			series := quant[r*h*nq : (r+1)*h*nq]
			if opts.capK > 0 {
				lo, hi := math.Inf(1), math.Inf(-1)
				for k, o := range obs {
					if o {
						lo = math.Min(lo, float64(vals[k]))
						hi = math.Max(hi, float64(vals[k]))
					}
				}
				if math.IsInf(lo, 1) {
					lo, hi = 0, 0
				}
				_, sc := causalLocScale(vals, obs, ps, cfg.MinimumScale)
				s := float64(sc[t-1]) * opts.capK
				lo32, hi32 := float32(lo-s), float32(hi+s)
				for k := range series {
					series[k] = min(max(series[k], lo32), hi32)
				}
			}
			steps := make([][]float32, h)
			for s := 0; s < h; s++ {
				step := series[s*nq : (s+1)*nq]
				for _, x := range step {
					if math.IsNaN(float64(x)) {
						return nil, errors.New("forecast produced NaN values; the input is degenerate")
					}
				}
				steps[s] = step
			}
			out[i][j] = steps
		}
	}
	return out, nil
}

// ffill forward-fills unobserved steps; leading unobserved steps become 0.
func ffill(vals []float32, obs []bool) []float32 {
	out := make([]float32, len(vals))
	var last float32
	for k := range vals {
		if obs[k] {
			last = vals[k]
		}
		out[k] = last
	}
	return out
}

// blockDecode forecasts h steps block by block, feeding each block's median back as context.
// Returns rows x h x Q.
func (m *Model) blockDecode(ctx context.Context, p *packed, h int, kf []float32, kfm []bool, horizon int, opts resolvedOptions) ([]float32, error) {
	cfg := m.Config
	ps, nq, mi := cfg.PatchSize, cfg.numQuantiles(), cfg.medianIndex()
	rows := p.rows
	out := make([]float32, rows*h*nq)
	remaining := (h + ps - 1) / ps
	offset := 0
	cur := p
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		blk := min(opts.perShot, remaining)
		blen := blk * ps
		var kfBlock []float32
		var kfmBlock []bool
		if kf != nil {
			kfBlock = make([]float32, rows*blen)
			kfmBlock = make([]bool, rows*blen)
			for r := 0; r < rows; r++ {
				copy(kfBlock[r*blen:], kf[r*horizon+offset:r*horizon+offset+blen])
				copy(kfmBlock[r*blen:], kfm[r*horizon+offset:r*horizon+offset+blen])
			}
		}
		quant, err := m.predictBlock(cur, blk, kfBlock, kfmBlock)
		if err != nil {
			return nil, err
		}
		m.refineBlock(quant, cur, blk)
		for r := 0; r < rows; r++ {
			for s := 0; s < blen && offset+s < h; s++ {
				copy(out[(r*h+offset+s)*nq:], quant[(r*blen+s)*nq:(r*blen+s+1)*nq])
			}
		}
		remaining -= blk
		offset += blen
		if remaining <= 0 {
			break
		}
		// Grow the context: targets get their median, known covariates their real future,
		// past-only covariates a blank.
		nt := cur.t + blen
		next := &packed{rows: rows, t: nt, values: make([]float32, rows*nt), observed: make([]bool, rows*nt), groups: cur.groups, roles: cur.roles}
		for r := 0; r < rows; r++ {
			vals, obs := cur.row(r)
			copy(next.values[r*nt:], vals)
			copy(next.observed[r*nt:], obs)
			role := RoleTarget
			if cur.roles != nil {
				role = cur.roles[r]
			}
			for s := 0; s < blen; s++ {
				idx := r*nt + cur.t + s
				switch role {
				case RoleKnownCov:
					next.values[idx] = kfBlock[r*blen+s]
					next.observed[idx] = kfmBlock[r*blen+s]
				case RolePastCov:
				default:
					med := quant[(r*blen+s)*nq+mi]
					if math.IsNaN(float64(med)) || math.IsInf(float64(med), 0) || math.Abs(float64(med)) >= extremeValueThreshold {
						med = 0
					}
					next.values[idx] = med
					next.observed[idx] = true
				}
			}
		}
		if nt > opts.maxContext {
			keep := max(ps, (opts.maxContext/ps)*ps)
			trimmed := &packed{rows: rows, t: keep, values: make([]float32, rows*keep), observed: make([]bool, rows*keep), groups: next.groups, roles: next.roles}
			for r := 0; r < rows; r++ {
				copy(trimmed.values[r*keep:], next.values[r*nt+nt-keep:(r+1)*nt])
				copy(trimmed.observed[r*keep:], next.observed[r*nt+nt-keep:(r+1)*nt])
			}
			next = trimmed
		}
		cur = next
	}
	return out, nil
}

// refineBlock moves a decoded block (rows x blk*ps x Q) from the anchor frame of the last
// context patch to the advancing refine frame it was trained in. Covariate rows keep theirs.
func (m *Model) refineBlock(quant []float32, cur *packed, blk int) {
	if blk <= 1 {
		return
	}
	cfg := m.Config
	ps, nq, mi := cfg.PatchSize, cfg.numQuantiles(), cfg.medianIndex()
	blen := blk * ps
	parallelFor(cur.rows, func(lo, hi int) {
		for r := lo; r < hi; r++ {
			if cur.roles != nil && cur.roles[r] != RoleTarget {
				continue
			}
			vals, obs := cur.row(r)
			loc, scale := causalLocScale(vals, obs, ps, cfg.MinimumScale)
			loc0, scale0 := loc[cur.t-1], scale[cur.t-1]
			q := quant[r*blen*nq : (r+1)*blen*nq]
			sinhMedian := make([]float64, blen)
			for s := 0; s < blen; s++ {
				sinhMedian[s] = (float64(q[s*nq+mi]) - float64(loc0)) / float64(scale0)
			}
			locs, scales := advancingBlockFrames(vals, obs, ps, sinhMedian, blk, cfg.MinimumScale)
			for s := ps; s < blen; s++ { // the first patch keeps the anchor frame
				lj, sj := float32(locs[s/ps]), float32(scales[s/ps])
				step := q[s*nq : (s+1)*nq]
				for k := range step {
					step[k] = (step[k]-loc0)/scale0*sj + lj
				}
				clampNonfinite(step)
			}
		}
	})
}
