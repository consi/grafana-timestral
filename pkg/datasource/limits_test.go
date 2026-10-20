package datasource

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/consi/grafana-timestral/pkg/series"
	"github.com/consi/grafana-timestral/pkg/timestral"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

func TestCheckInput(t *testing.T) {
	row := make([]float64, 10)
	ok := timestral.Input{PredictionLength: 5, Context: [][][]float64{{row}}}
	if err := checkInput(ok); err != nil {
		t.Fatal(err)
	}
	cases := map[string]timestral.Input{
		"no horizon":   {Context: ok.Context},
		"long horizon": {PredictionLength: series.MaxHorizonSteps + 1, Context: ok.Context},
		"no series":    {PredictionLength: 5},
		"long row":     {PredictionLength: 5, Context: [][][]float64{{make([]float64, maxRowLength+1)}}},
		"many rows":    {PredictionLength: 5, Context: [][][]float64{make([][]float64, maxRows+1)}},
	}
	for name, in := range cases {
		if err := checkInput(in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestErrorStatus(t *testing.T) {
	cases := map[backend.Status]error{
		backend.StatusBadRequest: fmt.Errorf("wrapped: %w", badRequestf("bad")),
		backend.StatusBadGateway: upstreamError{errors.New("down")},
		backend.StatusTimeout:    fmt.Errorf("x: %w", context.Canceled),
		backend.StatusInternal:   errors.New("boom"),
	}
	for want, err := range cases {
		if got := errorStatus(err); got != want {
			t.Errorf("%v: got %v, want %v", err, got, want)
		}
	}
}

func TestLocation(t *testing.T) {
	for _, tz := range []string{"", "utc", "browser", "Europe/Warsaw", "America/New_York"} {
		if _, err := (&Query{Timezone: tz}).location(); err != nil {
			t.Errorf("%q: %v", tz, err)
		}
	}
	if _, err := (&Query{Timezone: "Mars/Olympus"}).location(); err == nil {
		t.Error("unknown zone accepted")
	}
}
