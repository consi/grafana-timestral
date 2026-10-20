package datasource

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/consi/grafana-timestral/pkg/engine"
	"github.com/consi/grafana-timestral/pkg/model"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
)

// Resource API used by the app's panel, Explore and panel-menu forecasts:
//   GET  /status    model state
//   POST /forecast  forecast raw series (ForecastRequest)

func (d *Datasource) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", d.handleStatus)
	mux.HandleFunc("/forecast", d.handleForecast)
	return mux
}

// StatusResponse describes the model.
type StatusResponse struct {
	Model  string `json:"model"`
	Loaded bool   `json:"loaded"`
	Error  string `json:"error,omitempty"`
}

func (d *Datasource) handleStatus(w http.ResponseWriter, _ *http.Request) {
	loaded, err := engine.Shared.Status()
	st := StatusResponse{Model: model.ID, Loaded: loaded}
	if err != nil {
		st.Error = err.Error()
	}
	writeJSON(w, http.StatusOK, st)
}

func (d *Datasource) handleForecast(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	logger := log.DefaultLogger.FromContext(r.Context())
	var req ForecastRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody)).Decode(&req); err != nil {
		logger.Debug("Invalid forecast request", "error", err)
		writeError(w, http.StatusBadRequest, err)
		return
	}
	start := time.Now()
	resp, err := runForecast(r.Context(), &req)
	if err != nil {
		status := http.StatusInternalServerError
		var bad badRequestError
		if errors.As(err, &bad) {
			status = http.StatusBadRequest
			logger.Debug("Rejected forecast request", "error", err)
		} else if r.Context().Err() == nil {
			logger.Warn("Forecast failed", "error", err)
		}
		writeError(w, status, err)
		return
	}
	logger.Debug("Forecast request served",
		"series", len(req.Series), "steps", req.PredictionLength, "quantiles", len(resp.QuantileLevels),
		"duration", time.Since(start))
	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"message": err.Error()})
}
