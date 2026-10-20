// Package series turns Grafana data frames into regular grids for Timestral and plans the
// forecast requests (mirrors src/panels/forecast/forecast.ts, which does the same in the browser).
package series

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/data"
)

// MaxContext is the longest context Timestral reads; NativeHorizonSteps is the longest horizon
// it decodes in one block, so longer horizons coarsen the grid instead.
const (
	MaxContext         = 32768
	NativeHorizonSteps = 1024
	MaxHorizonSteps    = 16384
)

// Source is one numeric series from a frame.
type Source struct {
	Name   string
	Labels data.Labels
	Times  []int64 // unix ms
	Values []float64
}

// Grid is a series on a regular time grid; NaN marks missing values.
type Grid struct {
	Name   string
	Labels data.Labels
	Start  int64 // unix ms
	Step   int64 // ms
	Values []float64
}

// End is the timestamp of the last grid cell.
func (g Grid) End() int64 { return g.Start + int64(len(g.Values)-1)*g.Step }

// FromFrames extracts every numeric series that has a time field; long frames are widened first.
func FromFrames(frames data.Frames) ([]Source, error) {
	var out []Source
	for _, f := range frames {
		if f == nil || len(f.Fields) == 0 {
			continue
		}
		if f.TimeSeriesSchema().Type == data.TimeSeriesTypeLong {
			wide, err := data.LongToWide(f, nil)
			if err != nil {
				return nil, fmt.Errorf("convert long frame %q: %w", f.Name, err)
			}
			f = wide
		}
		timeIdx := -1
		for i, fld := range f.Fields {
			if fld.Type().Time() {
				timeIdx = i
				break
			}
		}
		if timeIdx < 0 {
			continue
		}
		tf := f.Fields[timeIdx]
		times := make([]int64, tf.Len())
		for i := range times {
			v, ok := tf.ConcreteAt(i)
			if !ok {
				times[i] = math.MinInt64
				continue
			}
			times[i] = v.(time.Time).UnixMilli()
		}
		for i, fld := range f.Fields {
			if i == timeIdx || !fld.Type().Numeric() {
				continue
			}
			vals := make([]float64, fld.Len())
			for k := range vals {
				p, err := fld.NullableFloatAt(k)
				if err != nil || p == nil {
					vals[k] = math.NaN()
				} else {
					vals[k] = *p
				}
			}
			out = append(out, Source{Name: displayName(f, fld), Labels: fld.Labels, Times: times, Values: vals})
		}
	}
	return out, nil
}

func displayName(f *data.Frame, fld *data.Field) string {
	if fld.Config != nil {
		if fld.Config.DisplayNameFromDS != "" {
			return fld.Config.DisplayNameFromDS
		}
		if fld.Config.DisplayName != "" {
			return fld.Config.DisplayName
		}
	}
	if len(fld.Labels) > 0 {
		keys := make([]string, 0, len(fld.Labels))
		for k := range fld.Labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = fmt.Sprintf("%s=%q", k, fld.Labels[k])
		}
		name := fld.Name
		if name == "Value" || name == "" {
			name = f.Name
		}
		return strings.TrimSpace(name + "{" + strings.Join(parts, ", ") + "}")
	}
	if fld.Name == "" || fld.Name == "Value" {
		if f.Name != "" {
			return f.Name
		}
	}
	return fld.Name
}

// ToGrid resamples onto a regular grid with the median sample spacing; gaps become NaN, and
// leading/trailing missing values are trimmed. ok is false when fewer than 2 points remain.
func ToGrid(s Source) (Grid, bool) {
	type pt struct {
		t int64
		v float64
	}
	pts := make([]pt, 0, len(s.Times))
	for i, t := range s.Times {
		if t == math.MinInt64 {
			continue
		}
		pts = append(pts, pt{t, s.Values[i]})
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].t < pts[j].t })
	if len(pts) < 2 {
		return Grid{}, false
	}
	var diffs []int64
	for i := 1; i < len(pts); i++ {
		if d := pts[i].t - pts[i-1].t; d > 0 {
			diffs = append(diffs, d)
		}
	}
	if len(diffs) == 0 {
		return Grid{}, false
	}
	sort.Slice(diffs, func(i, j int) bool { return diffs[i] < diffs[j] })
	step := diffs[len(diffs)/2]
	// Keep only the most recent MaxContext cells before allocating: irregular or bursty data
	// can have a tiny median step over a long span.
	lastT := pts[len(pts)-1].t
	earliest := lastT - int64(MaxContext-1)*step
	for len(pts) > 0 && pts[0].t < earliest {
		pts = pts[1:]
	}
	start := pts[0].t
	n := int(math.Round(float64(lastT-start)/float64(step))) + 1
	vals := make([]float64, n)
	for i := range vals {
		vals[i] = math.NaN()
	}
	for _, p := range pts {
		if !math.IsNaN(p.v) && !math.IsInf(p.v, 0) {
			vals[int(math.Round(float64(p.t-start)/float64(step)))] = p.v
		}
	}
	offset := 0
	first := -1
	for i, v := range vals {
		if !math.IsNaN(v) {
			first = i
			break
		}
	}
	if first < 0 {
		return Grid{}, false
	}
	vals = vals[first:]
	offset += first
	last := len(vals) - 1
	for last > 0 && math.IsNaN(vals[last]) {
		last--
	}
	vals = vals[:last+1]
	observed := 0
	for _, v := range vals {
		if !math.IsNaN(v) {
			observed++
		}
	}
	if observed < 2 {
		return Grid{}, false
	}
	return Grid{Name: s.Name, Labels: s.Labels, Start: start + int64(offset)*step, Step: step, Values: vals}, true
}

// Align samples a series onto `length` cells of a grid; points sharing a cell are averaged.
func Align(start, step int64, length int, times []int64, values []float64) []float64 {
	sum := make([]float64, length)
	cnt := make([]int, length)
	for i, t := range times {
		v := values[i]
		if t == math.MinInt64 || math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		idx := int(math.Round(float64(t-start) / float64(step)))
		if idx >= 0 && idx < length {
			sum[idx] += v
			cnt[idx]++
		}
	}
	out := make([]float64, length)
	for i := range out {
		if cnt[i] > 0 {
			out[i] = sum[i] / float64(cnt[i])
		} else {
			out[i] = math.NaN()
		}
	}
	return out
}

func blockMean(values []float64, factor int) []float64 {
	var out []float64
	for end := len(values); end > 0; end -= factor {
		var sum float64
		n := 0
		for _, v := range values[max(0, end-factor):end] {
			if !math.IsNaN(v) {
				sum += v
				n++
			}
		}
		if n > 0 {
			out = append(out, sum/float64(n))
		} else {
			out = append(out, math.NaN())
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// Prepare coarsens the grid when the horizon exceeds one Timestral block and returns the grid
// and the number of steps covering horizon.
func Prepare(g Grid, horizon time.Duration) (Grid, int) {
	ms := horizon.Milliseconds()
	factor := int(math.Ceil(math.Round(float64(ms)/float64(g.Step)) / NativeHorizonSteps))
	if factor > 1 {
		vals := blockMean(g.Values, factor)
		step := g.Step * int64(factor)
		g = Grid{Name: g.Name, Labels: g.Labels, Start: g.End() - int64(len(vals)-1)*step, Step: step, Values: vals}
	}
	steps := int(math.Round(float64(ms) / float64(g.Step)))
	return g, min(max(steps, 1), MaxHorizonSteps)
}

// Calendar features usable as known-future covariates.
const (
	HourOfDay = "hour_of_day"
	DayOfWeek = "day_of_week"
	Weekend   = "weekend"
)

// CalendarIsCategorical tells whether a calendar feature is a category code.
func CalendarIsCategorical(feature string) bool { return feature != Weekend }

// CalendarRow computes a calendar feature for `length` cells of a grid in loc.
func CalendarRow(feature string, start, step int64, length int, loc *time.Location) ([]float64, error) {
	out := make([]float64, length)
	for i := range out {
		t := time.UnixMilli(start + int64(i)*step).In(loc)
		switch feature {
		case HourOfDay:
			out[i] = float64(t.Hour())
		case DayOfWeek:
			out[i] = float64(t.Weekday())
		case Weekend:
			if wd := t.Weekday(); wd == time.Saturday || wd == time.Sunday {
				out[i] = 1
			}
		default:
			return nil, fmt.Errorf("unknown calendar feature %q", feature)
		}
	}
	return out, nil
}
