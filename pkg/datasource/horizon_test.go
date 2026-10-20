package datasource

import (
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

func TestHorizonAndDataEnd(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	past := backend.TimeRange{From: now.Add(-48 * time.Hour), To: now}
	future := backend.TimeRange{From: now.Add(-48 * time.Hour), To: now.Add(24 * time.Hour)}
	auto := &Query{Horizon: "auto"}

	if h, _ := auto.horizon(past, now); h != 12*time.Hour {
		t.Errorf("auto horizon on a past range = %v, want 25%%", h)
	}
	if h, _ := auto.horizon(future, now); h != 24*time.Hour {
		t.Errorf("auto horizon on a future range = %v, want the future part", h)
	}
	if h, _ := (&Query{Horizon: "3d"}).horizon(future, now); h != 72*time.Hour {
		t.Errorf("explicit horizon = %v", h)
	}
	if _, err := (&Query{Horizon: "soon"}).horizon(past, now); err == nil {
		t.Error("invalid horizon accepted")
	}
	if e := dataEnd(future, now); !e.Equal(now) {
		t.Errorf("history must stop at now, got %v", e)
	}
	if e := dataEnd(past, now); !e.Equal(past.To) {
		t.Errorf("history must use the range end, got %v", e)
	}
}
