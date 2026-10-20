package series

import (
	"math"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/data"
)

func eq(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !(a[i] == b[i] || (math.IsNaN(a[i]) && math.IsNaN(b[i]))) {
			return false
		}
	}
	return true
}

var nan = math.NaN()

func TestToGrid(t *testing.T) {
	g, ok := ToGrid(Source{Times: []int64{0, 10, 20, 40, 50}, Values: []float64{1, 2, 3, 5, 6}})
	if !ok || g.Step != 10 || !eq(g.Values, []float64{1, 2, 3, nan, 5, 6}) {
		t.Fatalf("got %+v", g)
	}
	g, ok = ToGrid(Source{Times: []int64{0, 10, 20, 30, 40}, Values: []float64{nan, 2, nan, 4, nan}})
	if !ok || g.Start != 10 || !eq(g.Values, []float64{2, nan, 4}) {
		t.Fatalf("trim: got %+v", g)
	}
	if _, ok := ToGrid(Source{Times: []int64{0}, Values: []float64{1}}); ok {
		t.Fatal("single point accepted")
	}
}

func TestAlignAndPrepare(t *testing.T) {
	if got := Align(100, 10, 4, []int64{90, 100, 121, 124, 130}, []float64{9, 5, 6, 8, 7}); !eq(got, []float64{5, nan, 7, 7}) {
		t.Fatalf("align: %v", got)
	}
	g := Grid{Start: 0, Step: 60_000, Values: []float64{1, 2, 3, 4, 5, nan, 7}}
	c, steps := Prepare(g, 2000*time.Minute)
	if c.Step != 120_000 || steps != 1000 || !eq(c.Values, []float64{1, 2.5, 4.5, 7}) || c.End() != g.End() {
		t.Fatalf("prepare: %+v %d", c, steps)
	}
}

func TestCalendar(t *testing.T) {
	monday := time.Date(2026, 10, 19, 23, 0, 0, 0, time.UTC).UnixMilli()
	h, _ := CalendarRow(HourOfDay, monday, 3600_000, 2, time.UTC)
	d, _ := CalendarRow(DayOfWeek, monday, 3600_000, 2, time.UTC)
	w, _ := CalendarRow(Weekend, time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC).UnixMilli(), 86400_000, 3, time.UTC)
	if !eq(h, []float64{23, 0}) || !eq(d, []float64{1, 2}) || !eq(w, []float64{1, 1, 0}) {
		t.Fatalf("calendar: %v %v %v", h, d, w)
	}
}

func TestFromFramesLong(t *testing.T) {
	t0 := time.UnixMilli(0)
	long := data.NewFrame("",
		data.NewField("time", nil, []time.Time{t0, t0, t0.Add(time.Minute), t0.Add(time.Minute)}),
		data.NewField("host", nil, []string{"a", "b", "a", "b"}),
		data.NewField("value", nil, []float64{1, 2, 3, 4}),
	)
	srcs, err := FromFrames(data.Frames{long})
	if err != nil || len(srcs) != 2 {
		t.Fatalf("long frame: %v %d", err, len(srcs))
	}
	if srcs[0].Labels["host"] != "a" || !eq(srcs[0].Values, []float64{1, 3}) {
		t.Fatalf("got %+v", srcs[0])
	}
}

func TestToGridClampsBeforeAllocating(t *testing.T) {
	// A burst of millisecond-spaced points after a month-long gap: the median step is 1 ms,
	// so the full span would need billions of cells. Only the last MaxContext cells are kept.
	times := []int64{0}
	values := []float64{1}
	base := int64(30 * 24 * time.Hour / time.Millisecond)
	for i := int64(0); i < 100; i++ {
		times = append(times, base+i)
		values = append(values, float64(i))
	}
	g, ok := ToGrid(Source{Times: times, Values: values})
	if !ok || len(g.Values) != 100 || g.Step != 1 {
		t.Fatalf("got %d cells, step %d", len(g.Values), g.Step)
	}
}
