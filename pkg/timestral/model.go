// Port of github.com/mistralai/timestral (Copyright 2026 Mistral AI, Apache-2.0), translated
// to Go and modified for inference in a Grafana plugin; see NOTICE.

// Package timestral is a pure-Go inference port of Mistral's Timestral time-series
// foundation models (https://github.com/mistralai/timestral). It loads the released
// `config.json` + `model.safetensors` checkpoints and reproduces the reference forecaster.
package timestral

import (
	"encoding/json"
	"fmt"
	"math"
)

// Config mirrors timestral.config.TimestralConfig.
type Config struct {
	PatchSize               int       `json:"patch_size"`
	DModel                  int       `json:"d_model"`
	NumHeads                int       `json:"num_heads"`
	MLPHiddenDim            int       `json:"mlp_hidden_dim"`
	BlockSchedule           []string  `json:"block_schedule"`
	QuantileKnots           []float64 `json:"quantile_knots"`
	BlockSize               int       `json:"block_size"`
	ContextLength           int       `json:"context_length"`
	RopeTheta               float64   `json:"rope_theta"`
	RopePartialFactor       float64   `json:"rope_partial_factor"`
	NormEps                 float64   `json:"norm_eps"`
	AttnBias                bool      `json:"attn_bias"`
	MLPBias                 bool      `json:"mlp_bias"`
	TimeAttnGate            string    `json:"time_attn_gate"`
	VariateAttnGate         string    `json:"variate_attn_gate"`
	MinimumScale            float64   `json:"minimum_scale"`
	UseAsinh                bool      `json:"use_asinh"`
	StabilizeWithGlobal     bool      `json:"stabilize_with_global"`
	CovariateEnabled        bool      `json:"covariate_enabled"`
	SplitContextHorizonAttn bool      `json:"split_context_horizon_attn"`
	AttendMissingPatches    bool      `json:"attend_missing_patches"`
	QuantileWidthFactor     float64   `json:"quantile_width_factor"`
}

func defaultConfig() Config {
	return Config{
		RopeTheta: 10000, RopePartialFactor: 0.5, NormEps: 1e-6, TimeAttnGate: "sdpa_sigmoid",
		VariateAttnGate: "sdpa_sigmoid", MinimumScale: 1e-6, UseAsinh: true, CovariateEnabled: true,
		SplitContextHorizonAttn: true, AttendMissingPatches: true, QuantileWidthFactor: 2,
	}
}

func (c Config) headDim() int      { return c.DModel / c.NumHeads }
func (c Config) ropeDim() int      { return int(float64(c.headDim()) * c.RopePartialFactor) }
func (c Config) numQuantiles() int { return len(c.QuantileKnots) }

func (c Config) medianIndex() int {
	for i, q := range c.QuantileKnots {
		if q == 0.5 {
			return i
		}
	}
	return -1
}

// layerAxes lists the attention axes of each layer kind in order; true = time.
var layerAxes = map[string][]bool{
	"time":         {true},
	"variate":      {false},
	"time_variate": {true, false},
	"variate_time": {false, true},
}

// SupportsKnownCovariates reports whether some time op is followed by both another time op
// and a variate op, so a known covariate's future can reach a target.
func (c Config) SupportsKnownCovariates() bool {
	if !c.CovariateEnabled {
		return false
	}
	var ops []bool
	for _, kind := range c.BlockSchedule {
		ops = append(ops, layerAxes[kind]...)
	}
	for i, isTime := range ops {
		rest := ops[i+1:]
		anyTime, allTime := false, true
		for _, t := range rest {
			anyTime = anyTime || t
			allTime = allTime && t
		}
		if isTime && anyTime && !allTime {
			return true
		}
	}
	return false
}

func (c Config) validate() error {
	if c.PatchSize <= 0 || c.DModel <= 0 || c.NumHeads <= 0 || c.DModel%c.NumHeads != 0 {
		return fmt.Errorf("invalid model dimensions")
	}
	if c.ropeDim() <= 0 || c.ropeDim()%2 != 0 {
		return fmt.Errorf("invalid rope dim %d", c.ropeDim())
	}
	if c.medianIndex() < 0 {
		return fmt.Errorf("quantile_knots must contain 0.5")
	}
	if c.StabilizeWithGlobal {
		return fmt.Errorf("stabilize_with_global is not supported by the advancing refine frame")
	}
	for _, k := range c.BlockSchedule {
		if _, ok := layerAxes[k]; !ok {
			return fmt.Errorf("unknown layer kind %q", k)
		}
	}
	return nil
}

type attention struct {
	qkv, o *Linear
	gate   *Linear
	isTime bool
}

type block struct {
	norms   []RMSNorm
	attns   []attention
	mlpNorm RMSNorm
	mlp     SwiGLU
}

// Model is a loaded Timestral network.
type Model struct {
	Config    Config
	patchProj ResidualBlock
	query     []float32
	layers    []block
	outNorm   RMSNorm
	head      ResidualBlock
	invFreq   []float32 // head_dim/2, zero past rope_dim/2
}

// LoadBytes builds a model from the contents of `config.json` and `model.safetensors`.
// weights must stay alive and unmodified for the lifetime of the model.
func LoadBytes(configJSON, weights []byte) (*Model, error) {
	cfg := defaultConfig()
	if err := json.Unmarshal(configJSON, &cfg); err != nil {
		return nil, fmt.Errorf("config.json: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	tensors, err := parseSafetensors(weights)
	if err != nil {
		return nil, err
	}
	return newModel(cfg, tensors)
}

func newModel(cfg Config, w map[string]Tensor) (*Model, error) {
	used := map[string]bool{}
	var missing []string
	get := func(name string, shape ...int) []float32 {
		t, ok := w[name]
		if !ok {
			missing = append(missing, name)
			return nil
		}
		used[name] = true
		if len(shape) > 0 && fmt.Sprint(t.Shape) != fmt.Sprint(shape) {
			missing = append(missing, fmt.Sprintf("%s (shape %v, want %v)", name, t.Shape, shape))
		}
		return t.Data
	}
	linear := func(prefix string, in, out int, bias bool) Linear {
		l := Linear{W: get(prefix+".weight", out, in), In: in, Out: out}
		if bias {
			l.B = get(prefix+".bias", out)
		}
		return l
	}
	residual := func(prefix string, in, hidden, out int, bias bool) ResidualBlock {
		return ResidualBlock{
			Hidden:   linear(prefix+".hidden_layer", in, hidden, bias),
			Output:   linear(prefix+".output_layer", hidden, out, bias),
			Residual: linear(prefix+".residual_layer", in, out, bias),
		}
	}
	d, ps, q := cfg.DModel, cfg.PatchSize, cfg.numQuantiles()
	eps := float32(cfg.NormEps)
	m := &Model{Config: cfg}
	m.patchProj = residual("representation.patch_proj", 2*ps, 4*d, d, true)
	m.query = get("representation.forecast_query", d)
	for i, kind := range cfg.BlockSchedule {
		p := fmt.Sprintf("transformer.layers.%d", i)
		var b block
		for j, isTime := range layerAxes[kind] {
			b.norms = append(b.norms, RMSNorm{Scale: get(fmt.Sprintf("%s.attn_norms.%d.scale", p, j), d), Eps: eps})
			ap := fmt.Sprintf("%s.attns.%d", p, j)
			qkv := linear(ap+".wQKV", d, 3*d, cfg.AttnBias)
			o := linear(ap+".wO", d, d, cfg.AttnBias)
			a := attention{qkv: &qkv, o: &o, isTime: isTime}
			gate := cfg.VariateAttnGate
			if isTime {
				gate = cfg.TimeAttnGate
			}
			if gate == "sdpa_sigmoid" {
				g := linear(ap+"._gate", d, d, cfg.AttnBias)
				a.gate = &g
			}
			b.attns = append(b.attns, a)
		}
		b.mlpNorm = RMSNorm{Scale: get(p+".mlp_norm.scale", d), Eps: eps}
		b.mlp = SwiGLU{
			W12:    linear(p+".mlp.w12", d, 2*cfg.MLPHiddenDim, cfg.MLPBias),
			W3:     linear(p+".mlp.w3", cfg.MLPHiddenDim, d, cfg.MLPBias),
			Hidden: cfg.MLPHiddenDim,
		}
		m.layers = append(m.layers, b)
	}
	m.outNorm = RMSNorm{Scale: get("transformer.out_norm.scale", d), Eps: eps}
	m.head = residual("head.projection", d, 4*d, ps*q, false)
	if len(missing) > 0 {
		return nil, fmt.Errorf("checkpoint does not match config: %v", missing)
	}
	for name := range w {
		if !used[name] {
			return nil, fmt.Errorf("unexpected tensor in checkpoint: %s", name)
		}
	}

	hd, rd := cfg.headDim(), cfg.ropeDim()
	m.invFreq = make([]float32, hd/2)
	for i := 0; i < rd/2; i++ {
		m.invFreq[i] = float32(1 / math.Pow(cfg.RopeTheta, float64(float32(2*i)/float32(rd))))
	}
	return m, nil
}

// masks describes the attention structure of one forward pass over S tokens per row.
type masks struct {
	rows, seq int
	// timeAllowed reports whether token q of row r may attend token k of the same row.
	timeAllowed func(r, q, k int) bool
	// variateAllowed reports whether row q may attend row k at token slot s; nil = all rows.
	variateAllowed func(s, q, k int) bool
	positions      []int // rows*seq
}

// transformer runs the layer stack on x (rows*seq x d_model) and returns the final normed states.
func (m *Model) transformer(x []float32, mk masks) []float32 {
	cfg := m.Config
	d, nh, hd := cfg.DModel, cfg.NumHeads, cfg.headDim()
	n := mk.rows * mk.seq

	// RoPE tables (cos, sin) per token, head_dim wide; dims past rope_dim rotate by 0.
	cos := make([]float32, n*hd)
	sin := make([]float32, n*hd)
	half := hd / 2
	for t := 0; t < n; t++ {
		pos := float32(mk.positions[t])
		for i := 0; i < half; i++ {
			f := float64(m.invFreq[i] * pos)
			c, s := float32(math.Cos(f)), float32(math.Sin(f))
			cos[t*hd+i], cos[t*hd+half+i] = c, c
			sin[t*hd+i], sin[t*hd+half+i] = s, s
		}
	}

	for li := range m.layers {
		layer := &m.layers[li]
		for ai := range layer.attns {
			a := &layer.attns[ai]
			xn := layer.norms[ai].Forward(x, n)
			qkv := a.qkv.Forward(xn, n)
			att := make([]float32, n*d)
			if a.isTime {
				m.timeAttention(qkv, att, cos, sin, mk, nh, hd)
			} else {
				m.variateAttention(qkv, att, mk, nh, hd)
			}
			if a.gate != nil {
				g := a.gate.Forward(xn, n)
				for i := range att {
					att[i] *= sigmoid(g[i])
				}
			}
			o := a.o.Forward(att, n)
			for i := range x {
				x[i] += o[i]
			}
		}
		h := layer.mlp.Forward(layer.mlpNorm.Forward(x, n), n)
		for i := range x {
			x[i] += h[i]
		}
	}
	return m.outNorm.Forward(x, n)
}

// extractHead copies q, k or v of one head for a set of tokens from the fused projection.
// The fused weight layout is (qkv, head_dim, n_heads): element (which, dim, head) sits at
// which*d + dim*n_heads + head.
func extractHead(qkv []float32, tokens []int, which, head, d, nh, hd int) []float32 {
	out := make([]float32, len(tokens)*hd)
	for i, t := range tokens {
		base := t*3*d + which*d + head
		for j := 0; j < hd; j++ {
			out[i*hd+j] = qkv[base+j*nh]
		}
	}
	return out
}

func rotate(x []float32, tokens []int, cos, sin []float32, hd int) {
	half := hd / 2
	tmp := make([]float32, hd)
	for i, t := range tokens {
		v := x[i*hd : (i+1)*hd]
		c, s := cos[t*hd:(t+1)*hd], sin[t*hd:(t+1)*hd]
		for j := 0; j < half; j++ {
			tmp[j] = v[j]*c[j] - v[j+half]*s[j]
			tmp[j+half] = v[j+half]*c[j+half] + v[j]*s[j+half]
		}
		copy(v, tmp)
	}
}

func (m *Model) timeAttention(qkv, att, cos, sin []float32, mk masks, nh, hd int) {
	d, seq := m.Config.DModel, mk.seq
	scale := float32(1 / math.Sqrt(float64(hd)))
	parallelFor(mk.rows*nh, func(lo, hi int) {
		scores := make([]float32, seq)
		out := make([]float32, hd)
		tokens := make([]int, seq)
		for job := lo; job < hi; job++ {
			r, h := job/nh, job%nh
			for s := range tokens {
				tokens[s] = r*seq + s
			}
			q := extractHead(qkv, tokens, 0, h, d, nh, hd)
			k := extractHead(qkv, tokens, 1, h, d, nh, hd)
			v := extractHead(qkv, tokens, 2, h, d, nh, hd)
			rotate(q, tokens, cos, sin, hd)
			rotate(k, tokens, cos, sin, hd)
			for s := 0; s < seq; s++ {
				softmaxAttend(q[s*hd:(s+1)*hd], seq,
					func(kk int) bool { return mk.timeAllowed(r, s, kk) },
					func(kk int) []float32 { return k[kk*hd : (kk+1)*hd] },
					func(kk int) []float32 { return v[kk*hd : (kk+1)*hd] },
					scale, scores, out)
				copy(att[(r*seq+s)*d+h*hd:], out)
			}
		}
	})
}

func (m *Model) variateAttention(qkv, att []float32, mk masks, nh, hd int) {
	d, seq, rows := m.Config.DModel, mk.seq, mk.rows
	scale := float32(1 / math.Sqrt(float64(hd)))
	parallelFor(seq*nh, func(lo, hi int) {
		scores := make([]float32, rows)
		out := make([]float32, hd)
		tokens := make([]int, rows)
		for job := lo; job < hi; job++ {
			s, h := job/nh, job%nh
			for r := range tokens {
				tokens[r] = r*seq + s
			}
			v := extractHead(qkv, tokens, 2, h, d, nh, hd)
			if rows == 1 {
				copy(att[s*d+h*hd:], v)
				continue
			}
			q := extractHead(qkv, tokens, 0, h, d, nh, hd)
			k := extractHead(qkv, tokens, 1, h, d, nh, hd)
			for r := 0; r < rows; r++ {
				allowed := func(kk int) bool { return true }
				if mk.variateAllowed != nil {
					allowed = func(kk int) bool { return mk.variateAllowed(s, r, kk) }
				}
				softmaxAttend(q[r*hd:(r+1)*hd], rows, allowed,
					func(kk int) []float32 { return k[kk*hd : (kk+1)*hd] },
					func(kk int) []float32 { return v[kk*hd : (kk+1)*hd] },
					scale, scores, out)
				copy(att[(r*seq+s)*d+h*hd:], out)
			}
		}
	})
}

// embed turns (rows, T) normalized values and observation masks into (rows, T/ps, d) tokens.
func (m *Model) embed(scaled []float32, observed []bool, rows, t int) []float32 {
	ps := m.Config.PatchSize
	np := t / ps
	in := make([]float32, rows*np*2*ps)
	for r := 0; r < rows; r++ {
		for p := 0; p < np; p++ {
			dst := in[(r*np+p)*2*ps:]
			for j := 0; j < ps; j++ {
				idx := r*t + p*ps + j
				dst[j] = scaled[idx]
				if !observed[idx] {
					dst[ps+j] = 1
				}
			}
		}
	}
	return m.patchProj.Forward(in, rows*np)
}

// monotone turns raw head outputs (Q per step) into non-crossing quantiles, in place.
func (m *Model) monotone(raw []float32) {
	cfg := m.Config
	levels := cfg.QuantileKnots
	nq := len(levels)
	mi := cfg.medianIndex()
	widths := make([]float32, nq)
	for i := mi + 1; i < nq; i++ {
		widths[i] = float32(levels[i] - levels[i-1])
	}
	for i := 0; i < mi; i++ {
		widths[i] = float32(levels[i+1] - levels[i])
	}
	wf := float32(cfg.QuantileWidthFactor)
	big := float32(math.MaxFloat32)
	inc := make([]float32, nq)
	for off := 0; off < len(raw); off += nq {
		v := raw[off : off+nq]
		for i := range v {
			inc[i] = wf * widths[i] * softplus(v[i])
		}
		med := v[mi]
		var up float32
		for i := mi + 1; i < nq; i++ {
			up += inc[i]
			v[i] = med + up
		}
		var down float32
		for i := mi - 1; i >= 0; i-- {
			down += inc[i]
			v[i] = med - down
		}
		for i := range v {
			if v[i] > big {
				v[i] = big
			} else if v[i] < -big {
				v[i] = -big
			}
		}
	}
}

// headForward maps decoder states (n tokens) to normalized quantiles (n*ps, Q).
func (m *Model) headForward(h []float32, n int) []float32 {
	raw := m.head.Forward(h, n)
	m.monotone(raw)
	return raw
}
