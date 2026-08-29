package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iyashjayesh/monigo/internal/exporter"
	"github.com/iyashjayesh/monigo/internal/registry"
)

func getExporters(t *testing.T) (int, string, []map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	GetExportersStatus(rec, httptest.NewRequest(http.MethodGet, "/exporters", nil))

	var body struct {
		Exporters []map[string]any `json:"exporters"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v\n%s", err, rec.Body.String())
	}
	return rec.Code, rec.Body.String(), body.Exporters
}

// Prometheus's collector is registered in this package's init, so the scrape
// endpoint exists whether or not anyone scrapes it. Omitting it would read as
// "not available".
func TestPrometheusIsAlwaysListed(t *testing.T) {
	exporter.SetActive(nil)
	code, raw, list := getExporters(t)
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, raw)
	}
	for _, e := range list {
		if e["name"] == "prometheus" {
			if e["kind"] != "pull" {
				t.Errorf("prometheus reported as kind %v, want pull", e["kind"])
			}
			return
		}
	}
	t.Errorf("prometheus absent from %s", raw)
}

// A zero time.Time ignores `omitempty` -- it is a struct, never "empty" to
// encoding/json -- and would serialise as 0001-01-01T00:00:00Z, which the
// dashboard would faithfully render as a timestamp in the year 1.
func TestUnsetTimestampsAreOmittedNotSentAsYearOne(t *testing.T) {
	exporter.SetActive(nil)
	_, raw, _ := getExporters(t)
	if strings.Contains(raw, "0001-01-01") {
		t.Errorf("a zero timestamp reached the wire: %s", raw)
	}
}

// A scrape that fails, fails at the collector; MoniGo never hears about it.
// Emitting failures:0 would assert a health signal that does not exist.
func TestPullExportersCarryNoFailureFields(t *testing.T) {
	exporter.SetActive(nil)
	_, raw, list := getExporters(t)
	for _, e := range list {
		if e["kind"] != "pull" {
			continue
		}
		for _, pushOnly := range []string{"failures", "consecutive_failures", "last_attempt", "last_error"} {
			if _, present := e[pushOnly]; present {
				t.Errorf("pull exporter %v carries push-only field %q: %s",
					e["name"], pushOnly, raw)
			}
		}
	}
}

func TestPushExporterReportsItsFailure(t *testing.T) {
	m := exporter.NewMultiExporter(failingExporter{})
	_ = m.Export(t.Context(), nil)
	exporter.SetActive(m)
	defer exporter.SetActive(nil)

	_, raw, list := getExporters(t)
	for _, e := range list {
		if e["name"] != "boom" {
			continue
		}
		if e["state"] != exporter.StateFailing {
			t.Errorf("state = %v, want %q", e["state"], exporter.StateFailing)
		}
		if !strings.Contains(raw, "collector unreachable") {
			t.Errorf("the error text never reached the client: %s", raw)
		}
		return
	}
	t.Errorf("the push exporter is absent from %s", raw)
}

func TestNonGETIsRejected(t *testing.T) {
	rec := httptest.NewRecorder()
	GetExportersStatus(rec, httptest.NewRequest(http.MethodPost, "/exporters", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST returned %d, want 405", rec.Code)
	}
}

type failingExporter struct{}

func (failingExporter) Name() string { return "boom" }
func (failingExporter) Export(_ context.Context, _ []*registry.MetricValue) error {
	return errors.New("collector unreachable")
}
