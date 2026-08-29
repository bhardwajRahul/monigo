package monigo

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// expectedPaths is every path the dashboard is supposed to answer on, for the
// default API base path.
func expectedPaths(apiPath string) []string {
	paths := []string{prometheusPath}
	for _, rt := range apiRoutes() {
		paths = append(paths, apiPath+rt.Suffix)
	}
	sort.Strings(paths)
	return paths
}

// servedStaticFallback reports whether a response is the static-file handler's
// answer rather than an API handler's. That is the shape the drift took: the
// unified handler fell through to serveHtmlSite, which replied
// 500 "Could not load static/metrics" -- a Prometheus scrape would see the
// target as down while the mux-registered dashboard answered the same path 200.
func servedStaticFallback(code int, body string) bool {
	return code == http.StatusNotFound || strings.Contains(body, "Could not load static/")
}

// TestEveryRouteIsReachableFromEveryRouter is the guardrail for the five-way
// registration split. MoniGo exposes the same endpoints through a ServeMux,
// two handler maps and two path switches; before apiRoutes() existed each kept
// its own list, and /metrics was present in three of them and missing from two.
//
// If a new registration style is added, add it here.
func TestEveryRouteIsReachableFromEveryRouter(t *testing.T) {
	apiPath := baseAPIPath
	want := expectedPaths(apiPath)

	// 1. Plain ServeMux, as StartDashboard and RegisterAPIHandlers build it.
	t.Run("ServeMux", func(t *testing.T) {
		mux := http.NewServeMux()
		registerAPIEndpoints(mux, apiPath)
		for _, p := range want {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
			if servedStaticFallback(rec.Code, rec.Body.String()) {
				t.Errorf("%s: unreachable (%d %q)", p, rec.Code, truncate(rec.Body.String()))
			}
		}
	})

	// 2. GetAPIHandlers -- the map handed to third-party routers.
	t.Run("GetAPIHandlers", func(t *testing.T) {
		assertMapCovers(t, GetAPIHandlers(), want)
	})

	// 3. GetSecuredAPIHandlers -- the same map with middleware applied.
	t.Run("GetSecuredAPIHandlers", func(t *testing.T) {
		assertMapCovers(t, GetSecuredAPIHandlers(&Monigo{}), want)
	})

	// 4. The unified handler behind RegisterDashboardHandlers. This is the one
	//    that answered 500 on /metrics.
	t.Run("UnifiedHandler", func(t *testing.T) {
		h := GetUnifiedHandler()
		for _, p := range want {
			rec := httptest.NewRecorder()
			h(rec, httptest.NewRequest(http.MethodGet, p, nil))
			if servedStaticFallback(rec.Code, rec.Body.String()) {
				t.Errorf("%s: fell through to the static handler (%d %q)",
					p, rec.Code, truncate(rec.Body.String()))
			}
		}
	})

	// 5. The Fiber handler.
	t.Run("FiberHandler", func(t *testing.T) {
		app := fiber.New()
		app.All("/*", GetFiberHandler())
		for _, p := range want {
			req := httptest.NewRequest(http.MethodGet, p, nil)
			resp, err := app.Test(req, 5000)
			if err != nil {
				t.Fatalf("%s: %v", p, err)
			}
			if resp.StatusCode == http.StatusNotFound {
				t.Errorf("%s: unreachable through the Fiber handler (404)", p)
			}
			resp.Body.Close()
		}
	})
}

func assertMapCovers(t *testing.T, got map[string]http.HandlerFunc, want []string) {
	t.Helper()
	for _, p := range want {
		if got[p] == nil {
			t.Errorf("%s: absent from the handler map", p)
		}
	}
	if len(got) != len(want) {
		t.Errorf("handler map has %d entries, expected %d -- an endpoint is "+
			"registered here that apiRoutes() does not declare", len(got), len(want))
	}
}

// TestLookupAndHandlerMapAgree keeps the two derivations of the route table in
// step. apiHandlerMap builds a map; lookupAPIHandler walks the slice. A route
// resolvable by one and not the other is the same class of bug the table was
// introduced to remove, one level down.
func TestLookupAndHandlerMapAgree(t *testing.T) {
	for _, apiPath := range []string{baseAPIPath, "/custom/base", "/metrics"} {
		t.Run(apiPath, func(t *testing.T) {
			for path := range apiHandlerMap(apiPath) {
				if lookupAPIHandler(path, apiPath) == nil {
					t.Errorf("%q is in apiHandlerMap but lookupAPIHandler misses it", path)
				}
				if !isAPIPath(path, apiPath) {
					t.Errorf("%q is a route but isAPIPath says it is not, so the "+
						"unified handler will send it to the static site", path)
				}
			}
		})
	}
}

// TestPrometheusPathSurvivesAnOverlappingBasePath pins the ordering inside
// lookupAPIHandler. A consumer who sets "/metrics" as their API base path
// would otherwise have the scrape endpoint shadowed by "/metrics/metrics".
func TestPrometheusPathSurvivesAnOverlappingBasePath(t *testing.T) {
	if lookupAPIHandler(prometheusPath, "/metrics") == nil {
		t.Fatal("the Prometheus endpoint is unreachable when the API base path is /metrics")
	}
}

// TestUnknownAPIPathIsNotFound guards the other direction: the table must not
// make the router answer on paths it does not declare.
func TestUnknownAPIPathIsNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	path := fmt.Sprintf("%s/no-such-endpoint", baseAPIPath)
	GetUnifiedHandler()(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("%s answered %d, expected 404", path, rec.Code)
	}
}

func truncate(s string) string {
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return strings.TrimSpace(s)
}
