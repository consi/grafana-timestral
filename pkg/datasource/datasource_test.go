package datasource

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
)

// fakeGrafana answers /api/ds/query with a daily-seasonal series per refId.
func fakeGrafana(t *testing.T, gotAuth *string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotAuth = r.Header.Get("Authorization")
		var body struct {
			Queries []map[string]any `json:"queries"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		resp := backend.NewQueryDataResponse()
		start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		for _, q := range body.Queries {
			ref := q["refId"].(string)
			n := 7 * 24 * 12 // a week of 5m points
			times := make([]time.Time, n)
			vals := make([]float64, n)
			for i := range vals {
				times[i] = start.Add(time.Duration(i) * 5 * time.Minute)
				vals[i] = 50 + 20*math.Sin(2*math.Pi*float64(i)/288) + float64(i)/100
			}
			v := data.NewField("Value", data.Labels{"instance": ref}, vals)
			resp.Responses[ref] = backend.DataResponse{Frames: data.Frames{data.NewFrame("", data.NewField("Time", nil, times), v)}}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestQueryData(t *testing.T) {
	var auth string
	srv := fakeGrafana(t, &auth)
	defer srv.Close()

	settings, _ := json.Marshal(Settings{GrafanaURL: srv.URL})
	inst, err := NewDatasource(context.Background(), backend.DataSourceInstanceSettings{
		JSONData:                settings,
		DecryptedSecureJSONData: map[string]string{"serviceAccountToken": "tok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ds := inst.(*Datasource)

	run := func(q map[string]any) data.Frames {
		raw, _ := json.Marshal(q)
		res, err := ds.QueryData(context.Background(), &backend.QueryDataRequest{Queries: []backend.DataQuery{{
			RefID: "A", JSON: raw, Interval: 5 * time.Minute, MaxDataPoints: 2016,
			TimeRange: backend.TimeRange{From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)},
		}}})
		if err != nil {
			t.Fatal(err)
		}
		r := res.Responses["A"]
		if r.Error != nil {
			t.Fatal(r.Error)
		}
		return r.Frames
	}
	inputs := []map[string]any{
		{"refId": "X", "datasource": map[string]string{"type": "prometheus", "uid": "p"}, "query": map[string]any{"expr": "up"}, "role": "target"},
		{"refId": "Y", "datasource": map[string]string{"type": "prometheus", "uid": "p"}, "query": map[string]any{"expr": "rate"}, "role": "past"},
	}

	frames := run(map[string]any{"inputs": inputs, "horizon": "1d", "band": "80", "calendar": []string{"hour_of_day"}})
	if auth != "Bearer tok" {
		t.Fatalf("auth header %q", auth)
	}
	if len(frames) != 2 {
		t.Fatalf("want history + forecast frames, got %d", len(frames))
	}
	fc := frames[1]
	if fc.Rows() != 288 || len(fc.Fields) != 4 {
		t.Fatalf("forecast frame %d rows, %d fields", fc.Rows(), len(fc.Fields))
	}
	med, _ := fc.Fields[1].NullableFloatAt(0)
	lo, _ := fc.Fields[2].NullableFloatAt(0)
	hi, _ := fc.Fields[3].NullableFloatAt(0)
	if *lo > *med || *med > *hi || *med < 20 || *med > 110 {
		t.Fatalf("implausible first step: %v %v %v", *lo, *med, *hi)
	}
	if fc.Fields[1].Labels["forecast"] != "median" || fc.Fields[1].Labels["instance"] != "X" {
		t.Fatalf("labels %v", fc.Fields[1].Labels)
	}

	frames = run(map[string]any{"inputs": inputs[:1], "horizon": "2d", "output": "eta", "threshold": 75.0})
	if len(frames) != 1 || len(frames[0].Fields) != 2 {
		t.Fatalf("eta frames %d", len(frames))
	}
	eta, _ := frames[0].Fields[0].FloatAt(0)
	// Peaks reach ~84 every day around 06:00, so the median should cross 75 within the 2 days.
	if eta >= NotWithinHorizon || eta > 2*86400 {
		t.Fatalf("eta %v", eta)
	}
	t.Logf("ETA to 75: %.1fh", eta/3600)
}
