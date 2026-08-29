package monigo

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/iyashjayesh/monigo/internal/exporter"
	"github.com/iyashjayesh/monigo/internal/registry"
)

// recordingExporter stands in for a push exporter and remembers what it was
// handed.
type recordingExporter struct {
	mu    sync.Mutex
	calls int
	names map[string]bool
	err   error
}

func newRecordingExporter() *recordingExporter {
	return &recordingExporter{names: map[string]bool{}}
}

func (r *recordingExporter) Export(_ context.Context, metrics []*registry.MetricValue) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	for _, m := range metrics {
		r.names[m.Name] = true
	}
	return r.err
}

func (r *recordingExporter) Name() string { return "recording" }

func (r *recordingExporter) snapshot() (int, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.names))
	for n := range r.names {
		out = append(out, n)
	}
	sort.Strings(out)
	return r.calls, out
}

// TestAConfiguredExporterActuallyReceivesMetrics is the regression test for the
// gap that made every push exporter inert: Registry, Pipeline and
// MultiExporter were each unit-tested, and nothing constructed them, so
// Export() was unreachable from a running MoniGo. Every existing test covered a
// part; none covered the wiring.
func TestAConfiguredExporterActuallyReceivesMetrics(t *testing.T) {
	rec := newRecordingExporter()

	p := startExportPipelineWith([]exporter.Exporter{rec}, "test-svc", 20*time.Millisecond)
	if p == nil {
		t.Fatal("no pipeline was started for a configured exporter")
	}
	defer p.Stop()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if calls, _ := rec.snapshot(); calls > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	calls, names := rec.snapshot()
	if calls == 0 {
		t.Fatal("the exporter was never called: the pipeline is not wired to it")
	}
	if len(names) == 0 {
		t.Fatal("the exporter was called with no metrics")
	}
	t.Logf("%d export(s), metrics: %v", calls, names)
}

// TestPushedMetricsMatchTheScrapedOnes pins the two exporter paths together.
// A push and a scrape that disagree about names would be worse than either
// being absent, because the two would be silently incomparable.
func TestPushedMetricsMatchTheScrapedOnes(t *testing.T) {
	reg := registry.NewRegistry()
	publishRuntimeMetrics(reg, "test-svc")

	got := map[string]bool{}
	for _, m := range reg.GetAll() {
		got[m.Name] = true
	}

	// The set MonigoCollector.Collect exposes in exporters/prometheus.go.
	want := []string{
		"monigo_cpu_usage_percent",
		"monigo_memory_usage_bytes",
		"monigo_goroutines_count",
		"monigo_disk_read_bytes_total",
		"monigo_disk_write_bytes_total",
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("%s is scraped by the Prometheus collector but never pushed", name)
		}
	}
	if len(got) != len(want) {
		t.Errorf("published %d metrics, the Prometheus collector exposes %d -- "+
			"the two paths have drifted", len(got), len(want))
	}
}

// TestEveryPublishedMetricCarriesTheServiceLabel guards against the ambiguity
// that appears the moment two services push to one backend.
func TestEveryPublishedMetricCarriesTheServiceLabel(t *testing.T) {
	reg := registry.NewRegistry()
	publishRuntimeMetrics(reg, "svc-a")
	for _, m := range reg.GetAll() {
		if m.Labels["service"] != "svc-a" {
			t.Errorf("%s carries service=%q, expected %q", m.Name, m.Labels["service"], "svc-a")
		}
	}
}

// TestNoExportersMeansNoPipeline: a MoniGo with nothing configured to push
// should not start a goroutine that collects stats every 30s for no reader.
func TestNoExportersMeansNoPipeline(t *testing.T) {
	if p := startExportPipelineWith(nil, "test-svc", time.Second); p != nil {
		t.Error("a pipeline was started with no exporters configured")
		p.Stop()
	}
	m := &Monigo{ServiceName: "test-svc"}
	if exps := m.pushExporters(); len(exps) != 0 {
		t.Errorf("pushExporters() returned %d exporters for an unconfigured MoniGo", len(exps))
	}
}

// TestPrometheusIsNotInThePushPipeline: Prometheus is scraped. Pushing to it
// would double-report and imply a delivery guarantee that does not exist.
func TestPrometheusIsNotInThePushPipeline(t *testing.T) {
	m := &Monigo{ServiceName: "test-svc"}
	for _, e := range m.pushExporters() {
		if e.Name() == "prometheus" {
			t.Error("the Prometheus exporter is in the push pipeline; it is scrape-based")
		}
	}
}

// TestStoppingTwiceIsSafe -- Shutdown may run after an explicit Stop, and the
// signal handler can race a manual call.
func TestStoppingTwiceIsSafe(t *testing.T) {
	rec := newRecordingExporter()
	m := &Monigo{ServiceName: "test-svc"}
	m.exportPipeline = startExportPipelineWith([]exporter.Exporter{rec}, "test-svc", time.Hour)
	m.stopExportPipeline()
	m.stopExportPipeline() // must not panic on a nil pipeline
}
