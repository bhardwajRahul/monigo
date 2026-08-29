package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/iyashjayesh/monigo/exporters"
	"github.com/iyashjayesh/monigo/internal/exporter"
)

// exporterView is the wire shape.
//
// The fields are spelled out rather than embedding exporter.Health for two
// reasons. `omitempty` does nothing for a time.Time -- a struct is never
// "empty" to encoding/json -- so an unset timestamp would serialise as
// "0001-01-01T00:00:00Z" and the dashboard would faithfully render "last
// scraped in the year 1". Pointers omit properly.
//
// And a pull exporter has no failure count: a scrape that fails, fails at the
// client and MoniGo never hears about it. Emitting failures:0 there would
// assert a health signal that does not exist, so those fields are omitted for
// the pull kind rather than sent as zeros.
type exporterView struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	State string `json:"state"`
	Total uint64 `json:"total"`

	LastSuccess *time.Time `json:"last_success,omitempty"`

	// Push only.
	LastAttempt         *time.Time `json:"last_attempt,omitempty"`
	ConsecutiveFailures *int       `json:"consecutive_failures,omitempty"`
	Failures            *uint64    `json:"failures,omitempty"`
	LastError           string     `json:"last_error,omitempty"`
}

type exportersResponse struct {
	Exporters []exporterView `json:"exporters"`
}

// GetExportersStatus reports the state of every configured exporter.
//
// Prometheus is always listed: its collector is registered in this package's
// init, so the scrape endpoint exists whether or not anyone is scraping it.
// Push exporters are listed only when one is configured -- an empty push list
// means nothing is set up to push, which the dashboard shows differently from
// a configured exporter that is failing.
func GetExportersStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	views := []exporterView{
		toView(exporters.NewMonigoCollector().ScrapeHealth()),
	}
	for _, h := range exporter.ActiveHealth() {
		views = append(views, toView(h))
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(exportersResponse{Exporters: views}); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

func toView(h exporter.Health) exporterView {
	v := exporterView{
		Name:        h.Name,
		Kind:        h.Kind,
		State:       h.State(),
		Total:       h.Total,
		LastSuccess: nonZero(h.LastSuccess),
	}
	if h.Kind == exporter.KindPush {
		failures := h.Failures
		consecutive := h.ConsecutiveFailures
		v.LastAttempt = nonZero(h.LastAttempt)
		v.ConsecutiveFailures = &consecutive
		v.Failures = &failures
		v.LastError = h.LastError
	}
	return v
}

// nonZero returns nil for an unset timestamp so it is omitted entirely.
func nonZero(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
