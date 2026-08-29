package monigo

import (
	"context"
	"time"

	"github.com/iyashjayesh/monigo/core"
	"github.com/iyashjayesh/monigo/internal/exporter"
	"github.com/iyashjayesh/monigo/internal/logger"
	"github.com/iyashjayesh/monigo/internal/pipeline"
	"github.com/iyashjayesh/monigo/internal/registry"
)

// Wiring for push-based exporters.
//
// Registry, Pipeline and MultiExporter each existed and each was unit-tested,
// but nothing ever constructed them, so OTelExporter.Export was unreachable and
// WithOTelEndpoint pushed nothing while logging that it had initialised fine.
// This file is the missing connection.

// defaultExportInterval is how often the registry is refreshed and handed to
// the push exporters.
//
// It matches the OTel PeriodicReader interval in NewOTelExporter, so every
// push carries a sample taken since the last one. DataPointsSyncFrequency is
// deliberately not reused: that governs flushes to the local time-series
// store, defaults to 5m, and at that spacing the reader would re-send the same
// value ten times over before it changed.
const defaultExportInterval = 30 * time.Second

// pushExporters returns the configured push-based exporters. Prometheus is
// absent by design: it is scraped, not pushed to, so it has no place in an
// export pipeline.
func (m *Monigo) pushExporters() []exporter.Exporter {
	var exps []exporter.Exporter
	if m.otelExporter != nil {
		exps = append(exps, m.otelExporter)
	}
	return exps
}

// startExportPipeline wires registry -> pipeline -> exporters and starts it.
// It is a no-op when nothing is configured to push.
func (m *Monigo) startExportPipeline() {
	m.exportPipeline = startExportPipelineWith(
		m.pushExporters(), m.ServiceName, defaultExportInterval)
}

// startExportPipelineWith is startExportPipeline with the exporter list and
// interval supplied, so the wiring can be exercised without a live collector.
// Returns nil when there is nothing to push to.
func startExportPipelineWith(exps []exporter.Exporter, serviceName string, interval time.Duration) *pipeline.Pipeline {
	if len(exps) == 0 {
		return nil
	}

	reg := registry.NewRegistry()
	multi := exporter.NewMultiExporter(exps...)
	exporter.SetActive(multi)
	p := pipeline.NewPipeline(reg, multi, interval).
		WithCollector(func() { publishRuntimeMetrics(reg, serviceName) })

	// Export once up front, before the ticker starts.
	//
	// This is not just about filling the opening interval. The OTel exporter
	// creates its instruments lazily on the first Export, and its own reader
	// runs on an independent clock: if the first Export waited for the first
	// tick, the reader's opening cycle would find no instruments and ship an
	// empty batch, putting real data two full cycles out. Measured against a
	// live OTLP receiver, this moves first delivery from ~60s to ~30s.
	publishRuntimeMetrics(reg, serviceName)
	if err := multi.Export(context.Background(), reg.GetAll()); err != nil {
		// Logged, not fatal: a collector that is not up yet is ordinary, and
		// the pipeline retries on every tick.
		logger.Log.Warn("initial metric export failed", "error", err)
	}
	p.Start(context.Background())

	names := make([]string, 0, len(exps))
	for _, e := range exps {
		names = append(names, e.Name())
	}
	logger.Log.Info("export pipeline started", "exporters", names, "interval", interval)
	return p
}

// stopExportPipeline stops the pipeline if one is running. Safe to call when
// none was started.
func (m *Monigo) stopExportPipeline() {
	if m.exportPipeline != nil {
		m.exportPipeline.Stop()
		m.exportPipeline = nil
		exporter.SetActive(nil)
	}
}

// publishRuntimeMetrics snapshots the runtime into the registry.
//
// The metric names and values deliberately mirror MonigoCollector.Collect in
// exporters/prometheus.go one for one, so a scrape and a push report identical
// numbers under identical names. Anything added to one belongs in the other.
//
// Everything is published as a gauge, including the two disk totals that
// Prometheus exposes as counters. That is not an oversight: the OTel exporter
// maps registry.Counter onto Float64Counter and calls Add(value), which is
// additive, while Registry.IncrementCounter already accumulates internally.
// Feeding a cumulative total through that pair would re-add the whole total on
// every cycle and grow quadratically. A gauge carrying the true cumulative
// value is the honest reading; making the counter path safe is separate work.
func publishRuntimeMetrics(r *registry.Registry, serviceName string) {
	stats := core.GetServiceStats(context.Background())
	labels := map[string]string{"service": serviceName}

	r.SetGauge("monigo_cpu_usage_percent", stats.LoadStatistics.SystemCPULoadRaw, labels)
	r.SetGauge("monigo_memory_usage_bytes", stats.MemoryStatistics.MemoryUsedBySystemRaw, labels)
	r.SetGauge("monigo_goroutines_count", float64(stats.CoreStatistics.Goroutines), labels)
	r.SetGauge("monigo_disk_read_bytes_total", float64(stats.DiskIO.ReadBytes), labels)
	r.SetGauge("monigo_disk_write_bytes_total", float64(stats.DiskIO.WriteBytes), labels)
}
