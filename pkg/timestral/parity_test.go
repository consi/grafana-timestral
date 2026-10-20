package timestral

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/consi/grafana-timestral/pkg/model"
)

// Parity against the reference PyTorch implementation (scripts/gen_parity_fixtures.py).

type fixture struct {
	Model string `json:"model"`
	Cases []struct {
		Name                       string          `json:"name"`
		PredictionLength           int             `json:"prediction_length"`
		Context                    [][][]*float64  `json:"context"`
		PastCovariates             [][][]*float64  `json:"past_covariates"`
		KnownCovariates            [][][]*float64  `json:"known_covariates"`
		CategoricalPastCovariates  [][][]*float64  `json:"categorical_past_covariates"`
		CategoricalKnownCovariates [][][]*float64  `json:"categorical_known_covariates"`
		Quantiles                  [][][][]float64 `json:"quantiles"`
		Options                    Options         `json:"options"`
	} `json:"cases"`
}

func toNaN(x [][][]*float64) [][][]float64 {
	if x == nil {
		return nil
	}
	out := make([][][]float64, len(x))
	for i := range x {
		out[i] = make([][]float64, len(x[i]))
		for j := range x[i] {
			out[i][j] = make([]float64, len(x[i][j]))
			for k, v := range x[i][j] {
				if v == nil {
					out[i][j][k] = math.NaN()
				} else {
					out[i][j][k] = *v
				}
			}
		}
	}
	return out
}

// testModel builds the checkpoint compiled into the plugin (scripts/fetch-model.sh).
func testModel(t testing.TB) *Model {
	t.Helper()
	m, err := LoadBytes(model.Config, model.Weights)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

//go:embed testdata/parity_Timestral-209M.json
var parityFixture []byte

func TestParity(t *testing.T) {
	var fx fixture
	if err := json.Unmarshal(parityFixture, &fx); err != nil {
		t.Fatal(err)
	}
	m := testModel(t)
	for _, c := range fx.Cases {
		t.Run(c.Name, func(t *testing.T) {
			got, err := m.Forecast(context.Background(), Input{
				Context:          toNaN(c.Context),
				PredictionLength: c.PredictionLength,
				PastCovariates:   toNaN(c.PastCovariates),
				KnownCovariates:  toNaN(c.KnownCovariates),
				CategoricalPast:  toNaN(c.CategoricalPastCovariates),
				CategoricalKnown: toNaN(c.CategoricalKnownCovariates),
				Options:          c.Options,
			})
			if err != nil {
				t.Fatal(err)
			}
			// Relative error against the spread of the reference forecast.
			var maxErr, maxRef float64
			for i := range c.Quantiles {
				for j := range c.Quantiles[i] {
					for s := range c.Quantiles[i][j] {
						for q, want := range c.Quantiles[i][j][s] {
							g := float64(got.Quantiles[i][j][s][q])
							maxErr = math.Max(maxErr, math.Abs(g-want))
							maxRef = math.Max(maxRef, math.Abs(want))
						}
					}
				}
			}
			rel := maxErr / math.Max(maxRef, 1e-9)
			t.Logf("max abs err %.3g (max |ref| %.3g, rel %.2e)", maxErr, maxRef, rel)
			if rel > 2e-3 {
				t.Errorf("parity failure: relative error %.2e", rel)
			}
		})
	}
}

func TestForecastCancelled(t *testing.T) {
	m := testModel(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	series := make([]float64, 300)
	for i := range series {
		series[i] = float64(i % 24)
	}
	if _, err := m.Forecast(ctx, Input{Context: [][][]float64{{series}}, PredictionLength: 48}); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}
