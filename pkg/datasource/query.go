package datasource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/consi/grafana-timestral/pkg/engine"
	"github.com/consi/grafana-timestral/pkg/series"
	"github.com/consi/grafana-timestral/pkg/timestral"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/gtime"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
	"github.com/grafana/grafana-plugin-sdk-go/data"
)

// Input roles.
const (
	RoleTarget           = "target"
	RolePast             = "past"
	RoleKnown            = "known"
	RoleCategoricalPast  = "categorical_past"
	RoleCategoricalKnown = "categorical_known"
	RoleIgnore           = "ignore"
)

// Output modes.
const (
	OutputForecast = "forecast"
	OutputETA      = "eta"
)

// Input is a query of another data source and how its series are used.
type Input struct {
	RefID      string          `json:"refId"`
	Datasource DatasourceRef   `json:"datasource"`
	Query      json.RawMessage `json:"query"`
	Role       string          `json:"role"`
}

// DatasourceRef identifies a data source.
type DatasourceRef struct {
	Type string `json:"type"`
	UID  string `json:"uid"`
}

// Query is the Timestral query model (see src/datasource/types.ts).
type Query struct {
	Inputs             []Input  `json:"inputs"`
	Horizon            string   `json:"horizon"`
	Band               string   `json:"band"`
	Output             string   `json:"output"`
	IncludeHistory     *bool    `json:"includeHistory"`
	Threshold          *float64 `json:"threshold"`
	ThresholdDirection string   `json:"thresholdDirection"`
	Calendar           []string `json:"calendar"`
	Timezone           string   `json:"timezone"`
	Joint              bool     `json:"joint"`
	MaxSeries          int      `json:"maxSeries"`
	SymmetricAveraging *bool    `json:"symmetricAveraging"`
	ClipNonnegative    *bool    `json:"clipNonnegative"`
	MaxContext         int      `json:"maxContext"`
	BlockPatches       int      `json:"blockPatches"`
	QuantileCapK       *float64 `json:"quantileCapK"`
}

var bands = map[string][][2]float64{
	"50":  {{0.25, 0.75}},
	"80":  {{0.1, 0.9}},
	"90":  {{0.05, 0.95}},
	"98":  {{0.01, 0.99}},
	"fan": {{0.01, 0.99}, {0.05, 0.95}, {0.1, 0.9}, {0.25, 0.75}},
}

func (q *Query) bandLevels() [][2]float64 {
	if q.Band == "none" {
		return nil
	}
	if b, ok := bands[q.Band]; ok {
		return b
	}
	return bands["80"]
}

// dataEnd is where history ends: the end of the range, or now when the range reaches into the future.
func dataEnd(tr backend.TimeRange, now time.Time) time.Time {
	if tr.To.After(now) {
		return now
	}
	return tr.To
}

// horizon is the explicit duration, or for "auto" the future part of the range when it reaches
// past now (so a dashboard range like now-7d..now+1d is filled), else 25% of the range.
func (q *Query) horizon(tr backend.TimeRange, now time.Time) (time.Duration, error) {
	if q.Horizon == "" || q.Horizon == "auto" {
		if future := tr.To.Sub(now); future > time.Minute {
			return future, nil
		}
		return max(tr.Duration()/4, time.Minute), nil
	}
	d, err := gtime.ParseDuration(q.Horizon)
	if err != nil {
		return 0, fmt.Errorf("invalid horizon %q: %w", q.Horizon, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("horizon must be positive")
	}
	return d, nil
}

// target is one forecasted series and where its forecast lands in the model output.
type target struct {
	grid      series.Grid   // the model's grid (possibly coarser than the data)
	source    series.Source // the original points, returned as history
	seriesIdx int
	variate   int
}

type job struct {
	input   timestral.Input
	targets []target
}

func (d *Datasource) query(ctx context.Context, pc backend.PluginContext, dq backend.DataQuery) (data.Frames, error) {
	var q Query
	if err := json.Unmarshal(dq.JSON, &q); err != nil {
		return nil, badRequestf("invalid query: %w", err)
	}
	if len(q.Inputs) == 0 {
		return nil, badRequestf("add at least one input query to forecast")
	}
	if q.Output == "" {
		q.Output = OutputForecast
	}
	if q.Output == OutputETA && q.Threshold == nil {
		return nil, badRequestf("time-to-threshold output needs a threshold")
	}
	now := time.Now()
	horizon, err := q.horizon(dq.TimeRange, now)
	if err != nil {
		return nil, badRequest(err)
	}
	histTo := dataEnd(dq.TimeRange, now)
	if !histTo.After(dq.TimeRange.From) {
		return nil, badRequestf("the time range has no history before now to forecast from")
	}
	loc, err := q.location()
	if err != nil {
		return nil, badRequest(err)
	}

	targets, covs, err := d.runInputs(ctx, pc, dq, &q, histTo)
	if err != nil {
		return nil, err
	}
	jobs, err := plan(&q, targets, covs, horizon, loc)
	if err != nil {
		return nil, badRequest(err)
	}
	nCovs := 0
	for _, c := range covs {
		nCovs += len(c)
	}
	log.DefaultLogger.FromContext(ctx).Debug("Forecast planned",
		"refId", dq.RefID, "output", q.Output, "horizon", horizon, "targets", len(targets),
		"covariates", nCovs, "calendar", len(q.Calendar), "jobs", len(jobs))

	opts := timestral.Options{
		MaxContext:            q.MaxContext,
		PerShotHorizonPatches: q.BlockPatches,
		QuantileRealCapK:      q.QuantileCapK,
		SymmetricAveraging:    q.SymmetricAveraging,
		ClipNonnegative:       q.ClipNonnegative,
	}
	var frames data.Frames
	colorIdx := 0
	includeHistory := q.IncludeHistory == nil || *q.IncludeHistory
	for _, j := range jobs {
		j.input.Options = opts
		if err := checkInput(j.input); err != nil {
			return nil, badRequest(err)
		}
		var fc *timestral.Forecast
		err := engine.Shared.Run(ctx, func(m *timestral.Model) error {
			var err error
			fc, err = m.Forecast(ctx, j.input)
			return err
		})
		if err != nil {
			return nil, err
		}
		for _, t := range j.targets {
			color := palette[colorIdx%len(palette)]
			colorIdx++
			col := func(level float64) ([]float64, error) {
				vals, err := fc.Column(t.seriesIdx, t.variate, level)
				if err != nil {
					return nil, err
				}
				out := make([]float64, len(vals))
				for i, v := range vals {
					out[i] = float64(v)
				}
				return out, nil
			}
			var frame *data.Frame
			if q.Output == OutputETA {
				frame, err = etaFrame(&q, t, col, histTo)
			} else {
				if includeHistory {
					frames = append(frames, historyFrame(t, color))
				}
				frame, err = forecastFrame(&q, t, col, color)
			}
			if err != nil {
				return nil, err
			}
			frames = append(frames, frame)
		}
	}
	return frames, nil
}

// location is the time zone for calendar features: an IANA name, or UTC when unset.
func (q *Query) location() (*time.Location, error) {
	switch q.Timezone {
	case "", "utc", "UTC", "browser":
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(q.Timezone)
	if err != nil {
		return nil, fmt.Errorf("unknown time zone %q", q.Timezone)
	}
	return loc, nil
}

// runInputs runs every input query in one request through Grafana and splits the returned
// series into targets and covariates by role.
func (d *Datasource) runInputs(ctx context.Context, pc backend.PluginContext, dq backend.DataQuery, q *Query, histTo time.Time) ([]series.Source, map[string][]series.Source, error) {
	queries := make([]map[string]any, 0, len(q.Inputs))
	roles := map[string]string{}
	for i, in := range q.Inputs {
		if in.Role == RoleIgnore {
			continue
		}
		if in.Datasource.UID == "" {
			return nil, nil, badRequestf("input %d has no data source", i+1)
		}
		if in.Datasource.Type == PluginID || in.Datasource.UID == "__expr__" || in.Datasource.UID == "-- Mixed --" {
			return nil, nil, badRequestf("input %d: Timestral, expression and mixed queries cannot be inputs", i+1)
		}
		body := map[string]any{}
		if len(in.Query) > 0 {
			if err := json.Unmarshal(in.Query, &body); err != nil {
				return nil, nil, badRequestf("input %d: %w", i+1, err)
			}
		}
		refID := in.RefID
		if refID == "" {
			refID = fmt.Sprintf("I%d", i)
		}
		body["refId"] = refID
		body["datasource"] = in.Datasource
		body["intervalMs"] = max(dq.Interval.Milliseconds(), 1)
		body["maxDataPoints"] = max(dq.MaxDataPoints, 100)
		queries = append(queries, body)
		roles[refID] = in.Role
	}
	res, err := d.dsQuery(ctx, pc.OrgID, dq.TimeRange.From, histTo, queries)
	if err != nil {
		return nil, nil, err
	}

	limit := q.MaxSeries
	if limit <= 0 {
		limit = 20
	}
	limit = min(limit, maxSeries)
	var targets []series.Source
	covs := map[string][]series.Source{}
	refIDs := make([]string, 0, len(roles))
	for refID := range roles {
		refIDs = append(refIDs, refID)
	}
	sort.Strings(refIDs)
	for _, refID := range refIDs {
		r, ok := res.Responses[refID]
		if !ok {
			continue
		}
		if r.Error != nil {
			return nil, nil, badRequestf("input %s: %w", refID, r.Error)
		}
		srcs, err := series.FromFrames(r.Frames)
		if err != nil {
			return nil, nil, badRequestf("input %s: %w", refID, err)
		}
		if role := roles[refID]; role == "" || role == RoleTarget {
			targets = append(targets, srcs...)
		} else {
			covs[role] = append(covs[role], srcs...)
		}
	}
	if len(targets) > limit {
		targets = targets[:limit]
	}
	if len(targets) == 0 {
		return nil, nil, badRequestf("the target inputs returned no time series")
	}
	return targets, covs, nil
}

// plan builds the model inputs: one Timestral series per target (or one joint series), with
// covariates aligned to its grid, grouped by horizon length.
func plan(q *Query, targets []series.Source, covs map[string][]series.Source, horizon time.Duration, loc *time.Location) ([]*job, error) {
	type unit struct {
		grid    series.Grid
		steps   int
		targets []series.Grid
		sources []series.Source
	}
	var grids []series.Grid
	var sources []series.Source
	for _, t := range targets {
		if g, ok := series.ToGrid(t); ok {
			grids = append(grids, g)
			sources = append(sources, t)
		}
	}
	if len(grids) == 0 {
		return nil, errors.New("the target series need at least 2 points")
	}
	var units []unit
	if q.Joint {
		g, steps := series.Prepare(grids[0], horizon)
		u := unit{grid: g, steps: steps, sources: sources}
		for i, t := range grids {
			if i == 0 {
				u.targets = append(u.targets, g)
				continue
			}
			times := make([]int64, len(t.Values))
			for k := range times {
				times[k] = t.Start + int64(k)*t.Step
			}
			u.targets = append(u.targets, series.Grid{Name: t.Name, Labels: t.Labels, Start: g.Start, Step: g.Step,
				Values: series.Align(g.Start, g.Step, len(g.Values), times, t.Values)})
		}
		units = []unit{u}
	} else {
		for _, t := range grids {
			g, steps := series.Prepare(t, horizon)
			units = append(units, unit{grid: g, steps: steps, targets: []series.Grid{g}, sources: []series.Source{sources[len(units)]}})
		}
	}

	byLen := map[int]*job{}
	var order []int
	for _, u := range units {
		n := len(u.grid.Values)
		align := func(srcs []series.Source, length int) [][]float64 {
			out := make([][]float64, len(srcs))
			for i, s := range srcs {
				out[i] = series.Align(u.grid.Start, u.grid.Step, length, s.Times, s.Values)
			}
			return out
		}
		known := align(covs[RoleKnown], n+u.steps)
		catKnown := align(covs[RoleCategoricalKnown], n+u.steps)
		for _, f := range q.Calendar {
			row, err := series.CalendarRow(f, u.grid.Start, u.grid.Step, n+u.steps, loc)
			if err != nil {
				return nil, err
			}
			if series.CalendarIsCategorical(f) {
				catKnown = append(catKnown, row)
			} else {
				known = append(known, row)
			}
		}
		j, ok := byLen[u.steps]
		if !ok {
			j = &job{input: timestral.Input{PredictionLength: u.steps}}
			byLen[u.steps] = j
			order = append(order, u.steps)
		}
		ctx := make([][]float64, len(u.targets))
		for i, t := range u.targets {
			ctx[i] = t.Values
		}
		seriesIdx := len(j.input.Context)
		j.input.Context = append(j.input.Context, ctx)
		appendRows(&j.input.PastCovariates, align(covs[RolePast], n), seriesIdx)
		appendRows(&j.input.KnownCovariates, known, seriesIdx)
		appendRows(&j.input.CategoricalPast, align(covs[RoleCategoricalPast], n), seriesIdx)
		appendRows(&j.input.CategoricalKnown, catKnown, seriesIdx)
		for v, t := range u.targets {
			j.targets = append(j.targets, target{grid: t, source: u.sources[v], seriesIdx: seriesIdx, variate: v})
		}
	}
	jobs := make([]*job, len(order))
	for i, l := range order {
		jobs[i] = byLen[l]
	}
	return jobs, nil
}

// appendRows adds a series' covariate rows, keeping the slice nil when there are none.
func appendRows(dst *[][][]float64, rows [][]float64, seriesIdx int) {
	if len(rows) == 0 {
		return
	}
	for len(*dst) < seriesIdx {
		*dst = append(*dst, nil)
	}
	*dst = append(*dst, rows)
}

func seriesLabels(t target, extra string) data.Labels {
	l := data.Labels{}
	for k, v := range t.grid.Labels {
		l[k] = v
	}
	if len(l) == 0 {
		l["series"] = t.grid.Name
	}
	if extra != "" {
		l["forecast"] = extra
	}
	return l
}

func levelName(l float64) string { return "p" + strconv.Itoa(int(math.Round(l*100))) }

func optional(v float64) *float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}

// palette is Grafana's classic palette; history, median and bands of a series share a color.
var palette = []string{"#7EB26D", "#EAB839", "#6ED0E0", "#EF843C", "#E24D42", "#1F78C1", "#BA43A6", "#705DA0", "#508642", "#CCA300"}

func fixedColor(c string) map[string]interface{} {
	return map[string]interface{}{"mode": "fixed", "fixedColor": c}
}

// historyFrame returns the target's original points (not the model's possibly coarser grid).
func historyFrame(t target, color string) *data.Frame {
	src := t.source
	times := make([]time.Time, 0, len(src.Times))
	vals := make([]*float64, 0, len(src.Times))
	for i, ts := range src.Times {
		if ts == math.MinInt64 {
			continue
		}
		times = append(times, time.UnixMilli(ts))
		vals = append(vals, optional(src.Values[i]))
	}
	f := data.NewField("Value", seriesLabels(t, "history"), vals)
	f.Config = &data.FieldConfig{DisplayNameFromDS: t.grid.Name, Color: fixedColor(color)}
	frame := data.NewFrame(t.grid.Name, data.NewField("Time", nil, times), f)
	frame.Meta = &data.FrameMeta{Type: data.FrameTypeTimeSeriesWide, TypeVersion: data.FrameTypeVersion{0, 1}}
	return frame
}

// forecastFrame returns the median and band quantiles, styled for time series panels: dashed
// median, bands filled between their edges and hidden from the legend.
func forecastFrame(q *Query, t target, col func(float64) ([]float64, error), color string) (*data.Frame, error) {
	median, err := col(0.5)
	if err != nil {
		return nil, err
	}
	times := make([]time.Time, len(median))
	end := t.grid.End()
	for i := range times {
		times[i] = time.UnixMilli(end + int64(i+1)*t.grid.Step)
	}
	frame := data.NewFrame(t.grid.Name+" · forecast", data.NewField("Time", nil, times))
	add := func(name, display string, vals []float64, custom map[string]interface{}) {
		ptrs := make([]*float64, len(vals))
		for i, v := range vals {
			ptrs[i] = optional(v)
		}
		f := data.NewField("Value", seriesLabels(t, name), ptrs)
		f.Config = &data.FieldConfig{DisplayNameFromDS: display, Color: fixedColor(color), Custom: custom}
		frame.Fields = append(frame.Fields, f)
	}
	add("median", t.grid.Name+" · forecast", median, map[string]interface{}{
		"lineStyle": map[string]interface{}{"fill": "dash", "dash": []int{10, 6}},
		"lineWidth": 2,
	})
	b := q.bandLevels()
	opacity := 20
	if len(b) > 1 {
		opacity = 12
	}
	hidden := map[string]interface{}{"legend": true, "tooltip": false, "viz": false}
	for _, band := range b {
		lo, err := col(band[0])
		if err != nil {
			return nil, err
		}
		hi, err := col(band[1])
		if err != nil {
			return nil, err
		}
		loName := t.grid.Name + " · " + levelName(band[0])
		add(levelName(band[0]), loName, lo, map[string]interface{}{"lineWidth": 0, "fillOpacity": 0, "hideFrom": hidden})
		add(levelName(band[1]), t.grid.Name+" · "+levelName(band[1]), hi, map[string]interface{}{
			"lineWidth": 0, "fillOpacity": opacity, "fillBelowTo": loName, "hideFrom": hidden,
		})
	}
	frame.Meta = &data.FrameMeta{Type: data.FrameTypeTimeSeriesWide, TypeVersion: data.FrameTypeVersion{0, 1}}
	return frame, nil
}

// NotWithinHorizon is the ETA when the forecast does not cross the threshold. It is finite (so
// alert comparisons like "ETA < 3h" are plain numeric and JSON-safe) and mapped to a text.
const NotWithinHorizon = 1e300

// etaFrame returns the seconds from the end of the data (now, or the range end for a range in
// the past) until the median, and the band edge on the threshold side, first cross the
// threshold; NotWithinHorizon when they do not within the horizon.
func etaFrame(q *Query, t target, col func(float64) ([]float64, error), from time.Time) (*data.Frame, error) {
	above := q.ThresholdDirection != "below"
	thr := *q.Threshold
	end := t.grid.End()
	eta := func(vals []float64) float64 {
		for i, v := range vals {
			if (above && v >= thr) || (!above && v <= thr) {
				at := time.UnixMilli(end + int64(i+1)*t.grid.Step)
				return math.Max(at.Sub(from).Seconds(), 0)
			}
		}
		return NotWithinHorizon
	}
	frame := data.NewFrame(t.grid.Name + " · time to threshold")
	add := func(name string, level float64) error {
		vals, err := col(level)
		if err != nil {
			return err
		}
		mapFrom := data.ConfFloat64(NotWithinHorizon)
		f := data.NewField("Value", seriesLabels(t, name), []float64{eta(vals)})
		f.Config = &data.FieldConfig{
			DisplayNameFromDS: t.grid.Name + " · ETA (" + name + ")",
			Unit:              "dtdurations",
			Mappings: data.ValueMappings{data.RangeValueMapper{
				From:   &mapFrom,
				Result: data.ValueMappingResult{Text: "not within horizon"},
			}},
		}
		frame.Fields = append(frame.Fields, f)
		return nil
	}
	if err := add("median", 0.5); err != nil {
		return nil, err
	}
	if b := q.bandLevels(); len(b) > 0 {
		edge := b[0][1]
		if !above {
			edge = b[0][0]
		}
		if err := add(levelName(edge), edge); err != nil {
			return nil, err
		}
	}
	frame.Meta = &data.FrameMeta{Type: data.FrameTypeNumericWide, TypeVersion: data.FrameTypeVersion{0, 1}}
	return frame, nil
}
