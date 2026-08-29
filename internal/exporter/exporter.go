package exporter

import (
	"context"
	"errors"
	"time"

	"github.com/iyashjayesh/monigo/internal/logger"
	"github.com/iyashjayesh/monigo/internal/registry"
)

type Exporter interface {
	Export(ctx context.Context, metrics []*registry.MetricValue) error
	Name() string
}

type MultiExporter struct {
	exporters []Exporter
	health    *healthTracker
}

func NewMultiExporter(exporters ...Exporter) *MultiExporter {
	m := &MultiExporter{exporters: exporters, health: newHealthTracker()}
	// Seed a record per exporter so a configured-but-never-run exporter is
	// reported as "never" rather than being absent from the dashboard, which
	// would be indistinguishable from not being configured at all.
	for _, e := range exporters {
		m.health.mu.Lock()
		m.health.get(e.Name(), KindPush)
		m.health.mu.Unlock()
	}
	return m
}

// Health returns the current status of every push exporter.
func (m *MultiExporter) Health() []Health {
	return m.health.snapshot()
}

// Export fans out to all exporters, collecting errors without short-circuiting.
func (m *MultiExporter) Export(ctx context.Context, metrics []*registry.MetricValue) error {
	var errs []error
	for _, e := range m.exporters {
		at := time.Now()
		err := e.Export(ctx, metrics)
		m.health.record(e.Name(), at, err)
		if err != nil {
			logger.Log.Error("exporter failed", "name", e.Name(), "error", err)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Name returns a combined name for the multi-exporter.
func (m *MultiExporter) Name() string {
	return "multi"
}
