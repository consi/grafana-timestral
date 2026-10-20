// Package engine holds the process-wide Timestral model of the plugin backend.
package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/consi/grafana-timestral/pkg/model"
	"github.com/consi/grafana-timestral/pkg/timestral"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
)

// Shared is the process-wide model: data source instances are recreated on settings changes,
// but the model is built once.
var Shared = &Holder{sem: make(chan struct{}, 1)}

// Holder loads the model lazily and serializes forecasts (one already uses every core).
type Holder struct {
	once  sync.Once
	done  chan struct{}
	model *timestral.Model
	err   error
	// sem serializes forecasts: one forecast already uses every core.
	sem chan struct{}
}

// Preload starts loading the model in the background (idempotent).
func (h *Holder) Preload() {
	h.once.Do(func() {
		h.done = make(chan struct{})
		go func() {
			defer close(h.done)
			start := time.Now()
			h.model, h.err = timestral.LoadBytes(model.Config, model.Weights)
			if h.err != nil {
				log.DefaultLogger.Error("Timestral model failed to load", "error", h.err)
				return
			}
			log.DefaultLogger.Info("Timestral model loaded", "model", model.ID, "duration", time.Since(start))
		}()
	})
}

// Get waits for the model to be loaded.
func (h *Holder) Get(ctx context.Context) (*timestral.Model, error) {
	h.Preload()
	select {
	case <-h.done:
		return h.model, h.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Status reports the load state without blocking.
func (h *Holder) Status() (loaded bool, err error) {
	h.Preload()
	select {
	case <-h.done:
		return h.err == nil, h.err
	default:
		return false, nil
	}
}

// Run executes fn with exclusive use of the model.
func (h *Holder) Run(ctx context.Context, fn func(m *timestral.Model) error) error {
	m, err := h.Get(ctx)
	if err != nil {
		return err
	}
	queued := time.Now()
	select {
	case h.sem <- struct{}{}:
	case <-ctx.Done():
		return fmt.Errorf("waiting for another forecast to finish: %w", ctx.Err())
	}
	defer func() { <-h.sem }()
	start := time.Now()
	err = fn(m)
	log.DefaultLogger.FromContext(ctx).Debug("Forecast computed",
		"queued", start.Sub(queued), "duration", time.Since(start), "error", err)
	return err
}
