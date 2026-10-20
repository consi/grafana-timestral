package datasource

import (
	"context"
	"math"
	"strconv"

	"github.com/consi/grafana-timestral/pkg/engine"
	"github.com/consi/grafana-timestral/pkg/model"
	"github.com/consi/grafana-timestral/pkg/timestral"
)

// nullable is a JSON array where null marks a missing value.
type nullable []*float64

func (n nullable) floats() []float64 {
	out := make([]float64, len(n))
	for i, v := range n {
		if v == nil {
			out[i] = math.NaN()
		} else {
			out[i] = *v
		}
	}
	return out
}

func rows(in []nullable) [][]float64 {
	out := make([][]float64, len(in))
	for i, r := range in {
		out[i] = r.floats()
	}
	return out
}

// SeriesInput is one series: its target variates (forecast jointly) and its covariates.
// Every series of a request must have the same number of each kind of row.
type SeriesInput struct {
	// Targets are the variates of the series, each over the same context (T steps).
	Targets []nullable `json:"targets"`
	// PastCovariates are known over the context only (T steps).
	PastCovariates []nullable `json:"past_covariates,omitempty"`
	// KnownCovariates are known over the context and the horizon (T + H steps).
	KnownCovariates []nullable `json:"known_covariates,omitempty"`
	// CategoricalPastCovariates are category codes over the context (T steps).
	CategoricalPastCovariates []nullable `json:"categorical_past_covariates,omitempty"`
	// CategoricalKnownCovariates are category codes over the context and horizon (T + H steps).
	CategoricalKnownCovariates []nullable `json:"categorical_known_covariates,omitempty"`
}

// ForecastRequest is the body of POST /forecast.
type ForecastRequest struct {
	PredictionLength int           `json:"prediction_length"`
	Series           []SeriesInput `json:"series"`
	// Quantiles to return, a subset of the model's levels; empty = all of them.
	Quantiles []float64         `json:"quantiles,omitempty"`
	Options   timestral.Options `json:"options"`
}

// VariateForecast is the forecast of one target variate.
type VariateForecast struct {
	Median    []float32            `json:"median"`
	Quantiles map[string][]float32 `json:"quantiles"`
}

// ForecastResponse is the reply of POST /forecast.
type ForecastResponse struct {
	Model          string    `json:"model"`
	QuantileLevels []float64 `json:"quantile_levels"`
	// Forecasts holds, per series, one forecast per target variate.
	Forecasts [][]VariateForecast `json:"forecasts"`
}

func quantileKey(q float64) string { return strconv.FormatFloat(q, 'f', -1, 64) }

func (r *ForecastRequest) input() timestral.Input {
	in := timestral.Input{PredictionLength: r.PredictionLength, Options: r.Options}
	has := func(f func(SeriesInput) []nullable) bool {
		for _, s := range r.Series {
			if len(f(s)) > 0 {
				return true
			}
		}
		return false
	}
	collect := func(f func(SeriesInput) []nullable) [][][]float64 {
		if !has(f) {
			return nil
		}
		out := make([][][]float64, len(r.Series))
		for i, s := range r.Series {
			out[i] = rows(f(s))
		}
		return out
	}
	in.Context = collect(func(s SeriesInput) []nullable { return s.Targets })
	in.PastCovariates = collect(func(s SeriesInput) []nullable { return s.PastCovariates })
	in.KnownCovariates = collect(func(s SeriesInput) []nullable { return s.KnownCovariates })
	in.CategoricalPast = collect(func(s SeriesInput) []nullable { return s.CategoricalPastCovariates })
	in.CategoricalKnown = collect(func(s SeriesInput) []nullable { return s.CategoricalKnownCovariates })
	return in
}

// runForecast validates and executes a request against the shared model.
func runForecast(ctx context.Context, req *ForecastRequest) (*ForecastResponse, error) {
	in := req.input()
	if err := checkInput(in); err != nil {
		return nil, badRequest(err)
	}
	var fc *timestral.Forecast
	err := engine.Shared.Run(ctx, func(m *timestral.Model) error {
		var err error
		fc, err = m.Forecast(ctx, in)
		return err
	})
	if err != nil {
		return nil, err
	}

	levels := req.Quantiles
	if len(levels) == 0 {
		levels = fc.QuantileLevels
	}
	resp := &ForecastResponse{Model: model.ID, QuantileLevels: levels, Forecasts: make([][]VariateForecast, len(fc.Quantiles))}
	for b, variates := range fc.Quantiles {
		for v := range variates {
			vf := VariateForecast{Quantiles: map[string][]float32{}}
			if vf.Median, err = fc.Column(b, v, 0.5); err != nil {
				return nil, err
			}
			for _, q := range levels {
				col, err := fc.Column(b, v, q)
				if err != nil {
					return nil, badRequest(err)
				}
				vf.Quantiles[quantileKey(q)] = col
			}
			resp.Forecasts[b] = append(resp.Forecasts[b], vf)
		}
	}
	return resp, nil
}
