// Port of github.com/mistralai/timestral (Copyright 2026 Mistral AI, Apache-2.0), translated
// to Go and modified for inference in a Grafana plugin; see NOTICE.

package timestral

import (
	"fmt"
	"math"
)

// Row roles in a packed batch.
const (
	RoleTarget   = 0
	RolePastCov  = 1
	RoleKnownCov = 2
)

// packed is a batch of rows of equal length T: one row per variate/covariate of every series.
type packed struct {
	rows, t  int
	values   []float32 // rows*t
	observed []bool    // rows*t
	groups   []int     // series id per row
	roles    []int     // nil when every row is a target
}

func (p *packed) row(r int) ([]float32, []bool) {
	return p.values[r*p.t : (r+1)*p.t], p.observed[r*p.t : (r+1)*p.t]
}

// scale normalizes every row causally per patch; returns (scaled, loc, scale), each rows*t.
func (m *Model) scale(values []float32, observed []bool, rows, t int) (scaled, loc, scale []float32) {
	scaled = make([]float32, rows*t)
	loc = make([]float32, rows*t)
	scale = make([]float32, rows*t)
	for r := 0; r < rows; r++ {
		x, o := values[r*t:(r+1)*t], observed[r*t:(r+1)*t]
		l, s := causalLocScale(x, o, m.Config.PatchSize, m.Config.MinimumScale)
		copy(loc[r*t:], l)
		copy(scale[r*t:], s)
		copy(scaled[r*t:], affineAsinh(x, l, s, o, m.Config.UseAsinh))
	}
	return scaled, loc, scale
}

// keyValidity is the (rows, patches) encoder key validity: a patch is a key once its row
// has started (leading all-missing patches are padding).
func (m *Model) keyValidity(observed []bool, rows, t int) (obsPerPatch, keyValid []bool) {
	ps := m.Config.PatchSize
	np := t / ps
	obsPerPatch = make([]bool, rows*np)
	keyValid = make([]bool, rows*np)
	for r := 0; r < rows; r++ {
		started := false
		for p := 0; p < np; p++ {
			any := false
			for j := 0; j < ps; j++ {
				if observed[r*t+p*ps+j] {
					any = true
					break
				}
			}
			obsPerPatch[r*np+p] = any
			started = started || any
			keyValid[r*np+p] = any || (m.Config.AttendMissingPatches && started)
		}
	}
	return obsPerPatch, keyValid
}

// groupFirstObserved returns, per row, the first patch any row of its series observes (0 if none).
func groupFirstObserved(obsPerPatch []bool, groups []int, np int) []int {
	first := map[int]int{}
	for r, g := range groups {
		for p := 0; p < np; p++ {
			if obsPerPatch[r*np+p] {
				if f, ok := first[g]; !ok || p < f {
					first[g] = p
				}
				break
			}
		}
	}
	out := make([]int, len(groups))
	for r, g := range groups {
		out[r] = first[g]
	}
	return out
}

// predictBlock forecasts hPatches patches after the packed context and returns raw-space
// quantiles, rows x (hPatches*ps) x Q flattened. knownFuture/knownMask are rows x (hPatches*ps)
// (nil when there are no known-future covariates).
func (m *Model) predictBlock(in *packed, hPatches int, knownFuture []float32, knownMask []bool) ([]float32, error) {
	if in.t%m.Config.PatchSize != 0 {
		return nil, fmt.Errorf("context length %d is not a multiple of the patch size", in.t)
	}
	allTargets := true
	for _, r := range in.roles {
		allTargets = allTargets && r == RoleTarget
	}
	if in.roles == nil || !m.Config.CovariateEnabled || allTargets {
		return m.predictContextOnly(in, hPatches), nil
	}
	if hPatches > m.Config.BlockSize {
		return nil, fmt.Errorf("decode block %d exceeds block size %d", hPatches, m.Config.BlockSize)
	}
	return m.predictWindow(in, hPatches, knownFuture, knownMask), nil
}

// predictContextOnly runs the encoder over the context and hPatches forecast-query tokens after it.
func (m *Model) predictContextOnly(in *packed, h int) []float32 {
	cfg := m.Config
	ps, d, nq := cfg.PatchSize, cfg.DModel, cfg.numQuantiles()
	rows, t := in.rows, in.t
	ll := t / ps
	seq := ll + h

	scaled, loc, scale := m.scale(in.values, in.observed, rows, t)
	enc := m.embed(scaled, in.observed, rows, t)
	x := make([]float32, rows*seq*d)
	for r := 0; r < rows; r++ {
		copy(x[r*seq*d:], enc[r*ll*d:(r+1)*ll*d])
		for i := ll; i < seq; i++ {
			copy(x[(r*seq+i)*d:], m.query)
		}
	}

	obsPerPatch, keyValid := m.keyValidity(in.observed, rows, t)
	firstObs := groupFirstObserved(obsPerPatch, in.groups, ll)
	mk := masks{rows: rows, seq: seq, positions: make([]int, rows*seq)}
	for r := 0; r < rows; r++ {
		for i := 0; i < seq; i++ {
			mk.positions[r*seq+i] = i - firstObs[r]
		}
	}
	mk.timeAllowed = func(r, q, k int) bool {
		if q == k {
			return true
		}
		if q < ll { // encoder: causal over valid keys, never the horizon
			return k < q && keyValid[r*ll+k]
		}
		if k < ll { // decoder reads the whole valid context
			return keyValid[r*ll+k]
		}
		return k <= q // causal within the decoder
	}
	if rows > 1 {
		mk.variateAllowed = func(s, q, k int) bool {
			if q == k {
				return true
			}
			if in.groups[q] != in.groups[k] {
				return false
			}
			return s >= ll || keyValid[k*ll+s]
		}
	}

	hs := m.transformer(x, mk)
	dec := make([]float32, rows*h*d)
	for r := 0; r < rows; r++ {
		copy(dec[r*h*d:], hs[(r*seq+ll)*d:(r*seq+seq)*d])
	}
	preds := m.headForward(dec, rows*h)
	stride := h * ps * nq
	for r := 0; r < rows; r++ {
		anchor := r*t + (ll-1)*ps
		denormalize(preds[r*stride:(r+1)*stride], nq, loc[anchor], scale[anchor], cfg.UseAsinh)
	}
	return preds
}

// predictWindow is the covariate path: the encoder covers context plus horizon so known-future
// values are keys; target and past-only rows keep a blank horizon. Decoder tokens before the
// horizon are inactive (they only attend themselves and nothing attends them), so only the
// horizon's decoder tokens are materialized.
func (m *Model) predictWindow(in *packed, h int, knownFuture []float32, knownMask []bool) []float32 {
	cfg := m.Config
	ps, d, nq := cfg.PatchSize, cfg.DModel, cfg.numQuantiles()
	rows := in.rows
	hl := h * ps
	w := in.t + hl
	n := w / ps
	ll := n - h

	full := make([]float32, rows*w)
	fullObs := make([]bool, rows*w)
	for r := 0; r < rows; r++ {
		v, o := in.row(r)
		copy(full[r*w:], v)
		copy(fullObs[r*w:], o)
		if knownFuture != nil {
			copy(full[r*w+in.t:], knownFuture[r*hl:(r+1)*hl])
			copy(fullObs[r*w+in.t:], knownMask[r*hl:(r+1)*hl])
		}
	}
	isTarget := func(r int) bool { return in.roles[r] == RoleTarget }
	isKnown := func(r int) bool { return in.roles[r] == RoleKnownCov }

	scaled, loc, scale := m.scale(full, fullObs, rows, w)
	for r := 0; r < rows; r++ {
		if !isKnown(r) {
			continue
		}
		x, o := full[r*w:(r+1)*w], fullObs[r*w:(r+1)*w]
		l, s, binary := instanceNormStats(x, o, cfg.MinimumScale)
		for i := 0; i < w; i++ {
			v := float32(0)
			if o[i] {
				v = (x[i] - l) / float32(max(float64(s), float32Eps))
				if cfg.UseAsinh && !binary {
					v = float32(math.Asinh(float64(v)))
				}
			}
			scaled[r*w+i], loc[r*w+i], scale[r*w+i] = v, l, s
		}
	}

	enc := m.embed(scaled, fullObs, rows, w)
	seq := n + h // n encoder tokens, then the h active decoder tokens (patches ll..n-1)
	x := make([]float32, rows*seq*d)
	for r := 0; r < rows; r++ {
		copy(x[r*seq*d:], enc[r*n*d:(r+1)*n*d])
		for i := n; i < seq; i++ {
			copy(x[(r*seq+i)*d:], m.query)
		}
	}

	obsPerPatch, keyValid := m.keyValidity(fullObs, rows, w)
	firstObs := groupFirstObserved(obsPerPatch, in.groups, n)
	mk := masks{rows: rows, seq: seq, positions: make([]int, rows*seq)}
	for r := 0; r < rows; r++ {
		for i := 0; i < n; i++ {
			mk.positions[r*seq+i] = i - firstObs[r]
		}
		for j := 0; j < h; j++ {
			mk.positions[r*seq+n+j] = ll + j - firstObs[r]
		}
	}
	mk.timeAllowed = func(r, q, k int) bool {
		if q == k {
			return true
		}
		if q < n {
			if k >= n {
				return false
			}
			if isKnown(r) { // known-future covariates read their whole row
				return keyValid[r*n+k]
			}
			return k < q && keyValid[r*n+k]
		}
		if k < n { // a horizon token reads valid encoder keys before the block anchor
			return k < ll && keyValid[r*n+k]
		}
		return k <= q // causal within the block
	}
	mk.variateAllowed = func(s, q, k int) bool {
		if q == k {
			return true
		}
		if in.groups[q] != in.groups[k] {
			return false
		}
		if !isTarget(q) && isTarget(k) { // covariates never read targets
			return false
		}
		if isKnown(q) && !isKnown(k) { // known-future rows never read causal rows
			return false
		}
		return s >= n || keyValid[k*n+s]
	}

	hs := m.transformer(x, mk)
	dec := make([]float32, rows*h*d)
	for r := 0; r < rows; r++ {
		copy(dec[r*h*d:], hs[(r*seq+n)*d:(r*seq+seq)*d])
	}
	preds := m.headForward(dec, rows*h)
	stride := h * ps * nq
	for r := 0; r < rows; r++ {
		anchor := r*w + (ll-1)*ps
		denormalize(preds[r*stride:(r+1)*stride], nq, loc[anchor], scale[anchor], cfg.UseAsinh)
	}
	return preds
}
