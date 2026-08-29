package exporter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/iyashjayesh/monigo/internal/registry"
)

func TestStateReflectsWhatHasActuallyHappened(t *testing.T) {
	past := time.Now().Add(-time.Minute)

	cases := []struct {
		name string
		h    Health
		want string
	}{
		{"configured, nothing yet", Health{Kind: KindPush}, StateNever},
		{"one success", Health{Kind: KindPush, Total: 1, LastSuccess: past}, StateOK},
		{
			// Failing but has succeeded before: recoverable, not broken.
			"failing after a success",
			Health{Kind: KindPush, Total: 3, LastSuccess: past, ConsecutiveFailures: 2},
			StateRetrying,
		},
		{
			// Never once succeeded: usually a wrong endpoint or no route,
			// which is a different problem from an intermittent one.
			"never succeeded",
			Health{Kind: KindPush, Total: 3, ConsecutiveFailures: 3},
			StateFailing,
		},
		{"scraped once", Health{Kind: KindPull, Total: 1, LastSuccess: past}, StateOK},
		{"never scraped", Health{Kind: KindPull}, StateNever},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.h.State(); got != c.want {
				t.Errorf("State() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestASuccessClearsTheFailureStreakButNotTheTotal(t *testing.T) {
	tr := newHealthTracker()
	boom := errors.New("connection refused")

	tr.record("otel", time.Now(), boom)
	tr.record("otel", time.Now(), boom)
	h := tr.snapshot()[0]
	if h.ConsecutiveFailures != 2 || h.Failures != 2 || h.Total != 2 {
		t.Fatalf("after two failures: consecutive=%d failures=%d total=%d",
			h.ConsecutiveFailures, h.Failures, h.Total)
	}
	if h.State() != StateFailing {
		t.Errorf("state = %q, want %q", h.State(), StateFailing)
	}

	tr.record("otel", time.Now(), nil)
	h = tr.snapshot()[0]
	if h.ConsecutiveFailures != 0 {
		t.Errorf("a success left a streak of %d", h.ConsecutiveFailures)
	}
	if h.Failures != 2 {
		t.Errorf("a success erased history: failures=%d, want 2", h.Failures)
	}
	if h.LastError != "" {
		t.Errorf("a success left the stale error %q", h.LastError)
	}
	if h.State() != StateOK {
		t.Errorf("state = %q, want %q", h.State(), StateOK)
	}
}

// A configured exporter that has never run must still appear. Absent from the
// list is indistinguishable from not configured, which is the one thing an
// operator checking this page needs to tell apart.
func TestAConfiguredExporterAppearsBeforeItHasEverRun(t *testing.T) {
	m := NewMultiExporter(stubExporter{name: "otel-otlp"})
	got := m.Health()
	if len(got) != 1 {
		t.Fatalf("Health() returned %d entries, want 1", len(got))
	}
	if got[0].Name != "otel-otlp" || got[0].State() != StateNever {
		t.Errorf("got %q/%q, want otel-otlp/never", got[0].Name, got[0].State())
	}
}

func TestActiveHealthIsNilWhenNothingIsRunning(t *testing.T) {
	SetActive(nil)
	if got := ActiveHealth(); got != nil {
		t.Errorf("ActiveHealth() = %v with no pipeline, want nil", got)
	}
}

type stubExporter struct {
	name string
	err  error
}

func (s stubExporter) Name() string                                              { return s.name }
func (s stubExporter) Export(_ context.Context, _ []*registry.MetricValue) error { return s.err }
