// Port of github.com/mistralai/timestral (Copyright 2026 Mistral AI, Apache-2.0), translated
// to Go and modified for inference in a Grafana plugin; see NOTICE.

package timestral

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"unsafe"
)

type tensorInfo struct {
	Dtype   string   `json:"dtype"`
	Shape   []int    `json:"shape"`
	Offsets [2]int64 `json:"data_offsets"`
}

// Tensor is a dense float32 tensor loaded from a safetensors file.
type Tensor struct {
	Shape []int
	Data  []float32
}

// parseSafetensors decodes every tensor of an in-memory safetensors file as float32.
// Little-endian F32 tensors alias data (no copy); F16 and BF16 are converted.
func parseSafetensors(data []byte) (map[string]Tensor, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("not a safetensors file")
	}
	headerLen := binary.LittleEndian.Uint64(data)
	if headerLen > 100<<20 || 8+headerLen > uint64(len(data)) {
		return nil, fmt.Errorf("safetensors header too large (%d bytes)", headerLen)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data[8:8+headerLen], &raw); err != nil {
		return nil, fmt.Errorf("parse header: %w", err)
	}
	body := data[8+headerLen:]

	out := make(map[string]Tensor, len(raw))
	for name, msg := range raw {
		if name == "__metadata__" {
			continue
		}
		var info tensorInfo
		if err := json.Unmarshal(msg, &info); err != nil {
			return nil, fmt.Errorf("tensor %s: %w", name, err)
		}
		n := 1
		for _, d := range info.Shape {
			n *= d
		}
		lo, hi := info.Offsets[0], info.Offsets[1]
		if lo < 0 || hi < lo || hi > int64(len(body)) {
			return nil, fmt.Errorf("tensor %s: offsets out of range", name)
		}
		buf := body[lo:hi]
		var values []float32
		switch info.Dtype {
		case "F32":
			if len(buf) != 4*n {
				return nil, fmt.Errorf("tensor %s: size mismatch", name)
			}
			values = float32View(buf)
		case "BF16":
			values = make([]float32, n)
			for i := range values {
				values[i] = math.Float32frombits(uint32(binary.LittleEndian.Uint16(buf[2*i:])) << 16)
			}
		case "F16":
			values = make([]float32, n)
			for i := range values {
				values[i] = float16To32(binary.LittleEndian.Uint16(buf[2*i:]))
			}
		default:
			return nil, fmt.Errorf("tensor %s: unsupported dtype %s", name, info.Dtype)
		}
		out[name] = Tensor{Shape: info.Shape, Data: values}
	}
	return out, nil
}

// float32View reinterprets little-endian float32 bytes without copying when the platform is
// little-endian and the data is aligned; otherwise it decodes a copy.
func float32View(buf []byte) []float32 {
	n := len(buf) / 4
	if n == 0 {
		return nil
	}
	if littleEndian && uintptr(unsafe.Pointer(&buf[0]))%4 == 0 {
		return unsafe.Slice((*float32)(unsafe.Pointer(&buf[0])), n)
	}
	out := make([]float32, n)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[4*i:]))
	}
	return out
}

var littleEndian = func() bool {
	x := uint16(1)
	return *(*byte)(unsafe.Pointer(&x)) == 1
}()

func float16To32(h uint16) float32 {
	sign := uint32(h>>15) << 31
	exp := uint32(h>>10) & 0x1f
	mant := uint32(h) & 0x3ff
	switch {
	case exp == 0 && mant == 0:
		return math.Float32frombits(sign)
	case exp == 0:
		// Subnormal: normalize.
		for mant&0x400 == 0 {
			mant <<= 1
			exp--
		}
		exp++
		mant &= 0x3ff
	case exp == 0x1f:
		return math.Float32frombits(sign | 0x7f800000 | mant<<13)
	}
	return math.Float32frombits(sign | (exp+112)<<23 | mant<<13)
}
