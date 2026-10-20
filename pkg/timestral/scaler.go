// Port of github.com/mistralai/timestral (Copyright 2026 Mistral AI, Apache-2.0), translated
// to Go and modified for inference in a Grafana plugin; see NOTICE.

package timestral

import "math"

const (
	float32Eps  = 1.1920928955078125e-07
	float64Tiny = 2.2250738585072014e-308
)

// causalLocScale returns per-step (loc, scale) of one row: every step of a patch gets the
// causal mean/std (Welford, float64) of all observed values up to the end of that patch.
func causalLocScale(x []float32, obs []bool, ps int, minScale float64) (loc, scale []float32) {
	t := len(x)
	means := make([]float64, t)
	scales := make([]float64, t)
	var cumW, cumX, m2, prevMean float64
	for i := 0; i < t; i++ {
		w := 0.0
		if obs[i] {
			w = 1
		}
		xv := float64(x[i])
		cumW += w
		cumX += w * xv
		den := math.Max(cumW, 1)
		mean := cumX / den
		m2 += (xv - prevMean) * (xv - mean) * w
		variance := m2 / math.Max(den-1, 1)
		means[i] = mean
		scales[i] = math.Max(math.Sqrt(variance), minScale)
		prevMean = mean
	}
	loc = make([]float32, t)
	scale = make([]float32, t)
	for p := 0; p < t/ps; p++ {
		end := (p+1)*ps - 1
		for j := p * ps; j <= end; j++ {
			loc[j] = float32(means[end])
			scale[j] = float32(scales[end])
		}
	}
	return loc, scale
}

// affineAsinh is asinh((x - loc) / scale) on observed steps and 0 elsewhere.
func affineAsinh(x, loc, scale []float32, obs []bool, useAsinh bool) []float32 {
	out := make([]float32, len(x))
	for i := range x {
		if !obs[i] {
			continue
		}
		v := (x[i] - loc[i]) / float32(math.Max(float64(scale[i]), float32Eps))
		if useAsinh {
			v = float32(math.Asinh(float64(v)))
		}
		out[i] = v
	}
	return out
}

// instanceNormStats is the non-causal whole-row normalization used for known-future covariates.
// 0/1 indicator rows keep loc=0, scale=1.
func instanceNormStats(x []float32, obs []bool, minScale float64) (loc, scale float32, binary bool) {
	var n, sum float32
	allZeroOne, anyObs := true, false
	for i, v := range x {
		if obs[i] {
			n++
			sum += v
			anyObs = true
		}
		if !(v == 0 || v == 1 || !obs[i]) {
			allZeroOne = false
		}
	}
	n = float32(math.Max(float64(n), 1))
	loc = sum / n
	var ss float32
	for i, v := range x {
		if obs[i] {
			d := v - loc
			ss += d * d
		}
	}
	scale = float32(math.Max(math.Sqrt(float64(ss/n)), minScale))
	if allZeroOne && anyObs {
		return 0, 1, true
	}
	return loc, scale, false
}

// clampNonfinite maps +/-inf in a quantile vector to its largest/smallest finite value
// (0 when none is finite); NaN is left untouched.
func clampNonfinite(v []float32) {
	hasInf := false
	for _, x := range v {
		if math.IsInf(float64(x), 0) {
			hasInf = true
			break
		}
	}
	if !hasInf {
		return
	}
	maxF, minF := math.Inf(-1), math.Inf(1)
	for _, x := range v {
		f := float64(x)
		if !math.IsInf(f, 0) && !math.IsNaN(f) {
			maxF = math.Max(maxF, f)
			minF = math.Min(minF, f)
		}
	}
	for i, x := range v {
		f := float64(x)
		switch {
		case math.IsInf(f, 1):
			f = maxF
		case math.IsInf(f, -1):
			f = minF
		}
		if math.IsInf(f, 0) {
			f = 0
		}
		v[i] = float32(f)
	}
}

// denormalize inverts the asinh normalization of a (steps, Q) block with one (loc, scale).
func denormalize(preds []float32, nq int, loc, scale float32, useAsinh bool) {
	for i := range preds {
		x := preds[i]
		if useAsinh {
			x = float32(math.Sinh(float64(x)))
		}
		preds[i] = x*scale + loc
	}
	for off := 0; off < len(preds); off += nq {
		clampNonfinite(preds[off : off+nq])
	}
}

type welford struct{ w, mean, m2 float64 }

func (a welford) combine(b welford) welford {
	w := a.w + b.w
	ws := math.Max(w, float64Tiny)
	delta := b.mean - a.mean
	out := welford{w: w, mean: a.mean + delta*b.w/ws, m2: a.m2 + b.m2 + delta*delta*a.w*b.w/ws}
	if w == 0 {
		out.mean, out.m2 = 0, 0
	}
	return out
}

// advancingBlockFrames returns per-horizon-patch (loc, scale) of a decoded block in the
// advancing refine frame: patch j uses the statistics of the observed context plus the
// block's medians of patches 0..j-1, each denormalized in its own frame.
// sinhMedian is (nPred, ps): the medians in the anchor frame with asinh undone.
func advancingBlockFrames(context []float32, obs []bool, ps int, sinhMedian []float64, nPred int, minScale float64) (locs, scales []float64) {
	var acc welford
	for p := 0; p < len(context)/ps; p++ {
		var st welford
		for j := p * ps; j < (p+1)*ps; j++ {
			if obs[j] {
				st.w++
				st.mean += float64(context[j])
			}
		}
		if st.w > 0 {
			st.mean /= st.w
			for j := p * ps; j < (p+1)*ps; j++ {
				if obs[j] {
					d := float64(context[j]) - st.mean
					st.m2 += d * d
				}
			}
		}
		acc = acc.combine(st)
	}
	offset := acc.mean
	clip := math.Sqrt(math.MaxFloat32) / 1e3
	locs = make([]float64, nPred)
	scales = make([]float64, nPred)
	folded := make([]float64, ps)
	for h := 0; h < nPred; h++ {
		variance := math.Max(acc.m2/math.Max(acc.w-1, 1), 0)
		scale := math.Max(math.Sqrt(variance), minScale)
		locs[h], scales[h] = acc.mean, scale
		var bMean float64
		for j := 0; j < ps; j++ {
			f := sinhMedian[h*ps+j]*scale + acc.mean - offset
			f = math.Max(-clip, math.Min(clip, f)) + offset
			folded[j] = f
			bMean += f
		}
		bMean /= float64(ps)
		var bM2 float64
		for _, f := range folded {
			bM2 += (f - bMean) * (f - bMean)
		}
		acc = acc.combine(welford{w: float64(ps), mean: bMean, m2: bM2})
	}
	return locs, scales
}
