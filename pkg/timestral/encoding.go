// Port of github.com/mistralai/timestral (Copyright 2026 Mistral AI, Apache-2.0), translated
// to Go and modified for inference in a Grafana plugin; see NOTICE.

package timestral

import (
	"math"
	"sort"
)

// targetEncode is the per-series smoothed mean-target encoding of categorical codes
// (timestral.covariate_encoding.target_encode): category c becomes
// (smooth*mean + sum_c) / (smooth + count_c) over the context. NaN codes form their own
// category; codes unseen in the context resolve to the series mean.
func targetEncode(target, pastCodes, futureCodes []float64, smooth float64) (past, future []float64) {
	var itemCount, itemSum float64
	obs := make([]bool, len(target))
	for i, v := range target {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			obs[i] = true
			itemCount++
			itemSum += v
		}
	}
	itemMean := 0.0
	if itemCount > 0 {
		itemMean = itemSum / itemCount
	}

	var cats []float64
	seen := map[float64]bool{}
	for _, c := range pastCodes {
		if !math.IsNaN(c) && !math.IsInf(c, 0) && !seen[c] {
			seen[c] = true
			cats = append(cats, c)
		}
	}
	sort.Float64s(cats)
	slotOf := make(map[float64]int, len(cats))
	for i, c := range cats {
		slotOf[c] = i
	}
	nanSlot, unseen := len(cats), len(cats)+1
	slot := func(c float64) int {
		if math.IsNaN(c) {
			return nanSlot
		}
		if s, ok := slotOf[c]; ok {
			return s
		}
		return unseen
	}

	sums := make([]float64, nanSlot+1)
	counts := make([]float64, nanSlot+1)
	for i, c := range pastCodes {
		s := slot(c)
		if s == unseen {
			s = 0 // unreachable for past codes, which define the categories
		}
		if obs[i] {
			sums[s] += target[i]
			counts[s]++
		}
	}
	lookup := make([]float64, nanSlot+2)
	for s := 0; s <= nanSlot; s++ {
		lookup[s] = (smooth*itemMean + sums[s]) / (smooth + counts[s])
	}
	lookup[unseen] = itemMean

	past = make([]float64, len(pastCodes))
	for i, c := range pastCodes {
		s := slot(c)
		if s == unseen {
			s = 0
		}
		past[i] = float64(float32(lookup[s]))
	}
	if futureCodes != nil {
		future = make([]float64, len(futureCodes))
		for i, c := range futureCodes {
			future[i] = float64(float32(lookup[slot(c)]))
		}
	}
	return past, future
}
