// Package datasource is the Timestral data source and the plugin's only backend. It holds the
// model, serves the forecasts of the app's panel and actions (resources), and runs queries of
// other data sources through Grafana to forecast them in any visualization and in alert and
// recording rules.
package datasource

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/consi/grafana-timestral/pkg/engine"
	"github.com/consi/grafana-timestral/pkg/model"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
	"github.com/grafana/grafana-plugin-sdk-go/backend/resource/httpadapter"
)

// PluginID is the data source's plugin id; its own queries cannot be used as inputs.
const PluginID = "consi-timestral-datasource"

var (
	_ backend.QueryDataHandler      = (*Datasource)(nil)
	_ backend.CallResourceHandler   = (*Datasource)(nil)
	_ backend.CheckHealthHandler    = (*Datasource)(nil)
	_ instancemgmt.InstanceDisposer = (*Datasource)(nil)
)

// Settings are the data source's jsonData.
type Settings struct {
	// GrafanaURL overrides the URL used to reach Grafana's API (default: Grafana's app URL).
	GrafanaURL string `json:"grafanaUrl,omitempty"`
}

// Datasource is one configured Timestral data source instance.
type Datasource struct {
	backend.CallResourceHandler
	settings Settings
	token    string
	client   *http.Client
}

// NewDatasource creates an instance and starts loading the model.
func NewDatasource(_ context.Context, s backend.DataSourceInstanceSettings) (instancemgmt.Instance, error) {
	engine.Shared.Preload()
	ds := &Datasource{client: &http.Client{Timeout: 2 * time.Minute}}
	if len(s.JSONData) > 0 {
		if err := json.Unmarshal(s.JSONData, &ds.settings); err != nil {
			return nil, fmt.Errorf("settings: %w", err)
		}
	}
	ds.token = s.DecryptedSecureJSONData["serviceAccountToken"]
	ds.CallResourceHandler = httpadapter.New(ds.routes())
	log.DefaultLogger.Debug("Data source instance created",
		"uid", s.UID, "grafanaUrlOverride", ds.settings.GrafanaURL != "", "hasToken", ds.token != "")
	return ds, nil
}

// Dispose keeps the shared model loaded.
func (d *Datasource) Dispose() {}

func (d *Datasource) grafanaURL(ctx context.Context) (string, error) {
	if d.settings.GrafanaURL != "" {
		return strings.TrimRight(d.settings.GrafanaURL, "/"), nil
	}
	u, err := backend.GrafanaConfigFromContext(ctx).AppURL()
	if err != nil {
		return "", fmt.Errorf("grafana URL unknown, set it in the data source settings: %w", err)
	}
	return strings.TrimRight(u, "/"), nil
}

// dsQuery runs queries of other data sources through Grafana's /api/ds/query.
func (d *Datasource) dsQuery(ctx context.Context, orgID int64, from, to time.Time, queries []map[string]any) (*backend.QueryDataResponse, error) {
	if d.token == "" {
		return nil, badRequestf("no service account token configured: Timestral needs one (role Viewer) to run its input queries")
	}
	base, err := d.grafanaURL(ctx)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]any{
		"from":    strconv.FormatInt(from.UnixMilli(), 10),
		"to":      strconv.FormatInt(to.UnixMilli(), 10),
		"queries": queries,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/ds/query", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+d.token)
	if orgID > 0 {
		req.Header.Set("X-Grafana-Org-Id", strconv.FormatInt(orgID, 10))
	}
	logger := log.DefaultLogger.FromContext(ctx)
	start := time.Now()
	resp, err := d.client.Do(req)
	if err != nil {
		logger.Warn("Grafana query API unreachable", "url", base, "error", err)
		return nil, upstreamError{fmt.Errorf("query Grafana: %w", err)}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	logger.Debug("Ran input queries through Grafana",
		"queries", len(queries), "status", resp.StatusCode, "bytes", len(raw), "duration", time.Since(start))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		logger.Warn("Grafana rejected the service account token", "status", resp.StatusCode)
	}
	var out backend.QueryDataResponse
	if err := json.Unmarshal(raw, &out); err != nil || (resp.StatusCode >= 300 && len(out.Responses) == 0) {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, upstreamError{fmt.Errorf("Grafana /api/ds/query returned %s: %s", resp.Status, msg)}
	}
	return &out, nil
}

// CheckHealth verifies the model and that the token can run queries.
func (d *Datasource) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	loaded, err := engine.Shared.Status()
	if err != nil {
		return &backend.CheckHealthResult{Status: backend.HealthStatusError, Message: "model: " + err.Error()}, nil
	}
	if !loaded {
		if _, err := engine.Shared.Get(ctx); err != nil {
			return &backend.CheckHealthResult{Status: backend.HealthStatusError, Message: "model: " + err.Error()}, nil
		}
	}
	now := time.Now()
	res, err := d.dsQuery(ctx, req.PluginContext.OrgID, now.Add(-time.Minute), now, []map[string]any{{
		"refId":      "A",
		"datasource": map[string]string{"type": "__expr__", "uid": "__expr__"},
		"type":       "math",
		"expression": "1",
	}})
	if err != nil {
		return &backend.CheckHealthResult{Status: backend.HealthStatusError, Message: err.Error()}, nil
	}
	if r, ok := res.Responses["A"]; ok && r.Error != nil {
		return &backend.CheckHealthResult{Status: backend.HealthStatusError, Message: "query check: " + r.Error.Error()}, nil
	}
	return &backend.CheckHealthResult{
		Status:  backend.HealthStatusOk,
		Message: fmt.Sprintf("%s loaded; Grafana API reachable with the service account token", model.ID),
	}, nil
}

// upstreamError marks failures of the Grafana API the input queries run through.
type upstreamError struct{ error }

// errorStatus maps an error to the status reported to panels and alert rules.
func errorStatus(err error) backend.Status {
	var bad badRequestError
	var up upstreamError
	switch {
	case errors.As(err, &bad):
		return backend.StatusBadRequest
	case errors.As(err, &up):
		return backend.StatusBadGateway
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return backend.StatusTimeout
	}
	return backend.StatusInternal
}

// QueryData forecasts each query.
func (d *Datasource) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
	resp := backend.NewQueryDataResponse()
	logger := log.DefaultLogger.FromContext(ctx)
	for _, q := range req.Queries {
		start := time.Now()
		frames, err := d.query(ctx, req.PluginContext, q)
		logger.Debug("Query forecast",
			"refId", q.RefID, "frames", len(frames), "duration", time.Since(start), "error", err)
		if err != nil {
			resp.Responses[q.RefID] = backend.ErrDataResponse(errorStatus(err), err.Error())
			continue
		}
		resp.Responses[q.RefID] = backend.DataResponse{Frames: frames}
	}
	return resp, nil
}
