package monigo

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/iyashjayesh/monigo/models"
)

// These fixtures used to live under static/API, compiled into every consumer's
// binary and referenced by nothing. They document the API's wire shape, so they
// moved to testdata/ -- out of the embed, still in the repo.
//
// Keeping them buys something the dashboard otherwise cannot have. There is no
// JS test runner, so nothing checks that the field names the front end reads
// are the field names the server sends. Decoding each fixture into the real
// struct with DisallowUnknownFields turns a model rename into a failed build
// rather than a value that silently renders as an em dash.
func TestAPIFixturesMatchTheirModels(t *testing.T) {
	cases := []struct {
		file string
		into func() any
	}{
		{"response/service-info.json", func() any { return &models.ServiceInfo{} }},
		{"response/go-routines-stats.json", func() any { return &models.GoRoutinesStatistic{} }},
		{"request/service-metrics.json", func() any { return &models.FetchDataPoints{} }},
		{"request/reports.json", func() any { return &models.ReportsRequest{} }},
	}

	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", "api", c.file))
			if err != nil {
				t.Fatalf("reading fixture: %v", err)
			}
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if err := dec.Decode(c.into()); err != nil {
				t.Errorf("fixture does not match its model: %v\n"+
					"Either the fixture is stale, or a model field was renamed and the "+
					"dashboard JavaScript that reads it is now looking for a key the "+
					"server no longer sends.", err)
			}
		})
	}
}

// Every fixture must at least be valid JSON, including those with no single
// struct to decode into (metrics.json is assembled from several).
func TestAPIFixturesAreValidJSON(t *testing.T) {
	err := filepath.Walk(filepath.Join("testdata", "api"),
		func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || filepath.Ext(p) != ".json" {
				return err
			}
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			var doc interface{}
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Errorf("%s is not valid JSON: %v", p, err)
			}
			return nil
		})
	if err != nil {
		t.Fatalf("walking fixtures: %v", err)
	}
}
