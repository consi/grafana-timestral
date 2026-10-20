package datasource

import (
	"fmt"

	"github.com/consi/grafana-timestral/pkg/series"
	"github.com/consi/grafana-timestral/pkg/timestral"
)

// Limits protecting the Grafana host from oversized forecasts, shared by the resource API and
// data source queries.
const (
	maxSeries      = 128
	maxRows        = 1024 // targets + covariates over all series
	maxRowLength   = series.MaxContext + series.MaxHorizonSteps
	maxRequestBody = 256 << 20
)

// checkInput enforces the limits on a model input.
func checkInput(in timestral.Input) error {
	if in.PredictionLength < 1 || in.PredictionLength > series.MaxHorizonSteps {
		return fmt.Errorf("the horizon must be between 1 and %d steps", series.MaxHorizonSteps)
	}
	if len(in.Context) == 0 || len(in.Context) > maxSeries {
		return fmt.Errorf("between 1 and %d series per forecast", maxSeries)
	}
	rows := 0
	for _, group := range [][][][]float64{in.Context, in.PastCovariates, in.KnownCovariates, in.CategoricalPast, in.CategoricalKnown} {
		for _, s := range group {
			rows += len(s)
			for _, r := range s {
				if len(r) > maxRowLength {
					return fmt.Errorf("series longer than %d points", maxRowLength)
				}
			}
		}
	}
	if rows > maxRows {
		return fmt.Errorf("at most %d series and covariates per forecast", maxRows)
	}
	return nil
}

// badRequestError marks errors caused by the request rather than the server.
type badRequestError struct{ error }

func badRequest(err error) error { return badRequestError{err} }

func badRequestf(format string, args ...any) error { return badRequest(fmt.Errorf(format, args...)) }
