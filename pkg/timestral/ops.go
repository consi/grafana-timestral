// Port of github.com/mistralai/timestral (Copyright 2026 Mistral AI, Apache-2.0), translated
// to Go and modified for inference in a Grafana plugin; see NOTICE.

package timestral

import (
	"math"
	"runtime"
	"sync"
)

// parallelFor runs fn(i) for i in [0, n) on up to GOMAXPROCS goroutines, in contiguous chunks.
func parallelFor(n int, fn func(lo, hi int)) {
	workers := runtime.GOMAXPROCS(0)
	if workers > n {
		workers = n
	}
	if workers <= 1 {
		fn(0, n)
		return
	}
	var wg sync.WaitGroup
	chunk := (n + workers - 1) / workers
	for lo := 0; lo < n; lo += chunk {
		hi := min(lo+chunk, n)
		wg.Add(1)
		go func(lo, hi int) {
			defer wg.Done()
			fn(lo, hi)
		}(lo, hi)
	}
	wg.Wait()
}

func dot(a, b []float32) float32 {
	n := len(a)
	b = b[:n]
	var s0, s1, s2, s3 float32
	i := 0
	for ; i+4 <= n; i += 4 {
		s0 += a[i] * b[i]
		s1 += a[i+1] * b[i+1]
		s2 += a[i+2] * b[i+2]
		s3 += a[i+3] * b[i+3]
	}
	for ; i < n; i++ {
		s0 += a[i] * b[i]
	}
	return (s0 + s1) + (s2 + s3)
}

// dot4 computes the dot products of four rows of x with one row of w, sharing the loads of w.
func dot4(x0, x1, x2, x3, w []float32) (float32, float32, float32, float32) {
	n := len(w)
	x0, x1, x2, x3 = x0[:n], x1[:n], x2[:n], x3[:n]
	var a0, a1, a2, a3, b0, b1, b2, b3 float32
	i := 0
	for ; i+2 <= n; i += 2 {
		w0, w1 := w[i], w[i+1]
		a0 += x0[i] * w0
		b0 += x0[i+1] * w1
		a1 += x1[i] * w0
		b1 += x1[i+1] * w1
		a2 += x2[i] * w0
		b2 += x2[i+1] * w1
		a3 += x3[i] * w0
		b3 += x3[i+1] * w1
	}
	for ; i < n; i++ {
		a0 += x0[i] * w[i]
		a1 += x1[i] * w[i]
		a2 += x2[i] * w[i]
		a3 += x3[i] * w[i]
	}
	return a0 + b0, a1 + b1, a2 + b2, a3 + b3
}

// Linear is y = x W^T + b with W stored (out, in) row-major, as in PyTorch.
type Linear struct {
	W       []float32
	B       []float32
	In, Out int
}

// Forward applies the layer to n rows of x (n x In) and returns n x Out.
func (l *Linear) Forward(x []float32, n int) []float32 {
	y := make([]float32, n*l.Out)
	const tileOut = 64
	tiles := (l.Out + tileOut - 1) / tileOut
	rowBlocks := (n + 3) / 4
	// Parallelize over (row block, output tile) pairs; each tile of W stays hot in cache
	// while it is reused for 4 rows of x.
	parallelFor(rowBlocks*tiles, func(lo, hi int) {
		for job := lo; job < hi; job++ {
			rb, t := job/tiles, job%tiles
			r0 := rb * 4
			j0, j1 := t*tileOut, min((t+1)*tileOut, l.Out)
			if r0+4 <= n {
				x0 := x[r0*l.In : (r0+1)*l.In]
				x1 := x[(r0+1)*l.In : (r0+2)*l.In]
				x2 := x[(r0+2)*l.In : (r0+3)*l.In]
				x3 := x[(r0+3)*l.In : (r0+4)*l.In]
				for j := j0; j < j1; j++ {
					a, b, c, d := dot4(x0, x1, x2, x3, l.W[j*l.In:(j+1)*l.In])
					y[r0*l.Out+j], y[(r0+1)*l.Out+j], y[(r0+2)*l.Out+j], y[(r0+3)*l.Out+j] = a, b, c, d
				}
			} else {
				for r := r0; r < n; r++ {
					xr := x[r*l.In : (r+1)*l.In]
					for j := j0; j < j1; j++ {
						y[r*l.Out+j] = dot(xr, l.W[j*l.In:(j+1)*l.In])
					}
				}
			}
		}
	})
	if l.B != nil {
		for r := 0; r < n; r++ {
			row := y[r*l.Out : (r+1)*l.Out]
			for j := range row {
				row[j] += l.B[j]
			}
		}
	}
	return y
}

func silu(x float32) float32 { return x / (1 + float32(math.Exp(float64(-x)))) }

func sigmoid(x float32) float32 { return 1 / (1 + float32(math.Exp(float64(-x)))) }

// softplus matches torch.nn.functional.softplus (beta=1, threshold=20).
func softplus(x float32) float32 {
	if x > 20 {
		return x
	}
	return float32(math.Log1p(math.Exp(float64(x))))
}

// ResidualBlock is output(silu(hidden(x))) + residual(x).
type ResidualBlock struct {
	Hidden, Output, Residual Linear
}

func (r *ResidualBlock) Forward(x []float32, n int) []float32 {
	h := r.Hidden.Forward(x, n)
	for i := range h {
		h[i] = silu(h[i])
	}
	out := r.Output.Forward(h, n)
	res := r.Residual.Forward(x, n)
	for i := range out {
		out[i] += res[i]
	}
	return out
}

// RMSNorm normalizes each row by its root mean square, then scales.
type RMSNorm struct {
	Scale []float32
	Eps   float32
}

func (r *RMSNorm) Forward(x []float32, n int) []float32 {
	d := len(r.Scale)
	out := make([]float32, len(x))
	for i := 0; i < n; i++ {
		row := x[i*d : (i+1)*d]
		var ss float32
		for _, v := range row {
			ss += v * v
		}
		inv := float32(1 / math.Sqrt(float64(ss/float32(d)+r.Eps)))
		o := out[i*d : (i+1)*d]
		for j, v := range row {
			o[j] = v * inv * r.Scale[j]
		}
	}
	return out
}

// SwiGLU is w3(gate * silu(up)) with [gate, up] = chunk(w12(x), 2).
type SwiGLU struct {
	W12, W3 Linear
	Hidden  int
}

func (m *SwiGLU) Forward(x []float32, n int) []float32 {
	gu := m.W12.Forward(x, n)
	h := make([]float32, n*m.Hidden)
	for i := 0; i < n; i++ {
		g := gu[i*2*m.Hidden : i*2*m.Hidden+m.Hidden]
		u := gu[i*2*m.Hidden+m.Hidden : (i+1)*2*m.Hidden]
		o := h[i*m.Hidden : (i+1)*m.Hidden]
		for j := range o {
			o[j] = g[j] * silu(u[j])
		}
	}
	return m.W3.Forward(h, n)
}

// softmaxAttend computes softmax(q.k / sqrt(d)) . v over the keys allowed by `allowed`,
// accumulating into out. keys/values are addressed through the key accessor.
func softmaxAttend(q []float32, nKeys int, allowed func(k int) bool, key, value func(k int) []float32, scale float32, scores []float32, out []float32) {
	maxS := float32(math.Inf(-1))
	for k := 0; k < nKeys; k++ {
		if !allowed(k) {
			scores[k] = float32(math.Inf(-1))
			continue
		}
		s := dot(q, key(k)) * scale
		scores[k] = s
		if s > maxS {
			maxS = s
		}
	}
	for i := range out {
		out[i] = 0
	}
	var sum float32
	for k := 0; k < nKeys; k++ {
		if math.IsInf(float64(scores[k]), -1) {
			continue
		}
		p := float32(math.Exp(float64(scores[k] - maxS)))
		sum += p
		v := value(k)
		for i := range out {
			out[i] += p * v[i]
		}
	}
	if sum > 0 {
		inv := 1 / sum
		for i := range out {
			out[i] *= inv
		}
	}
}
