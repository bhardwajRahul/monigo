package exporter

import (
	"sync"
	"time"
)

// Health describes what an exporter is doing, for display on the dashboard.
//
// Push and pull exporters do not share a vocabulary, and flattening them into
// one "OK / FAIL" would misreport half of them. MoniGo never exports to
// Prometheus -- Prometheus scrapes it -- so "last export succeeded" is
// meaningless there; the honest reading is when it was last scraped. Kind says
// which set of fields is meaningful.
type Health struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // KindPush or KindPull

	// Push only. LastAttempt differs from LastSuccess while an exporter is
	// failing, which is what makes "retrying" distinguishable from "idle".
	LastAttempt         time.Time `json:"last_attempt,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	LastError           string    `json:"last_error,omitempty"`

	// Both kinds. For a pull exporter LastSuccess is the last scrape.
	LastSuccess time.Time `json:"last_success,omitempty"`
	Total       uint64    `json:"total"`
	Failures    uint64    `json:"failures"`
}

const (
	KindPush = "push"
	KindPull = "pull"
)

// State is the verdict a dashboard shows. Derived rather than stored, so it
// cannot fall out of step with the counters behind it.
const (
	StateNever    = "never"    // configured, nothing has happened yet
	StateOK       = "ok"       // last attempt succeeded
	StateRetrying = "retrying" // failing now, has succeeded before
	StateFailing  = "failing"  // failing and has never succeeded
)

// State reports the exporter's current condition.
func (h Health) State() string {
	if h.ConsecutiveFailures > 0 {
		if h.LastSuccess.IsZero() {
			return StateFailing
		}
		return StateRetrying
	}
	if h.Total == 0 || h.LastSuccess.IsZero() {
		return StateNever
	}
	return StateOK
}

// healthTracker records outcomes per exporter name.
type healthTracker struct {
	mu sync.Mutex
	by map[string]*Health
}

func newHealthTracker() *healthTracker {
	return &healthTracker{by: make(map[string]*Health)}
}

func (t *healthTracker) get(name, kind string) *Health {
	h, ok := t.by[name]
	if !ok {
		h = &Health{Name: name, Kind: kind}
		t.by[name] = h
	}
	return h
}

// record notes the outcome of one push attempt.
func (t *healthTracker) record(name string, at time.Time, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	h := t.get(name, KindPush)
	h.LastAttempt = at
	h.Total++
	if err != nil {
		h.Failures++
		h.ConsecutiveFailures++
		h.LastError = err.Error()
		return
	}
	h.LastSuccess = at
	h.ConsecutiveFailures = 0
	h.LastError = ""
}

func (t *healthTracker) snapshot() []Health {
	t.mu.Lock()
	defer t.mu.Unlock()

	out := make([]Health, 0, len(t.by))
	for _, h := range t.by {
		out = append(out, *h)
	}
	return out
}

// The active MultiExporter, published so the API layer can report exporter
// health without importing the root package (which imports api, and would
// cycle). Set when the export pipeline starts; nil when nothing pushes.
var (
	activeMu sync.RWMutex
	active   *MultiExporter
)

// SetActive publishes the running MultiExporter for status reporting.
// Passing nil clears it, which is what a stopped pipeline should do.
func SetActive(m *MultiExporter) {
	activeMu.Lock()
	defer activeMu.Unlock()
	active = m
}

// ActiveHealth returns the health of the running push exporters, or nil when
// no pipeline is running. Nil means "nothing configured to push", which the
// dashboard reports differently from "configured and failing".
func ActiveHealth() []Health {
	activeMu.RLock()
	defer activeMu.RUnlock()
	if active == nil {
		return nil
	}
	return active.Health()
}
