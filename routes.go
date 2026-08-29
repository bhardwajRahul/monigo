package monigo

import (
	"net/http"
	"strings"

	"github.com/iyashjayesh/monigo/api"
)

// The dashboard's API surface, declared once.
//
// MoniGo exposes the same endpoints through five different registration
// styles -- a plain ServeMux, two handler maps for third-party routers, and
// two path switches behind the unified and Fiber handlers. Each one used to
// carry its own copy of the endpoint list, so adding a route meant editing
// five places and nothing failed when you edited four.
//
// That drift had already happened: the Prometheus endpoint was registered in
// the three map-shaped sites but missing from both switches, so `GET /metrics`
// answered 200 under RegisterAPIHandlers and 500 under
// RegisterDashboardHandlers and Fiber.
//
// Everything below derives from apiRoutes(). Adding an endpoint here reaches
// every router at once, and TestEveryRouteIsReachableFromEveryRouter fails if
// a registration style is ever added that does not.

// apiRoute is one endpoint served beneath the configured API base path.
type apiRoute struct {
	Suffix  string // appended to the API base path, e.g. "/service-info"
	Handler http.HandlerFunc
}

// prometheusPath is served at the root rather than under the API base path:
// scrapers are conventionally pointed at /metrics, and moving it would break
// every existing scrape config.
const prometheusPath = "/metrics"

// apiRoutes returns the endpoints served under the API base path, in a stable
// order. It deliberately excludes prometheusPath, which is not base-path
// relative -- see apiHandlerMap and lookupAPIHandler, which add it.
func apiRoutes() []apiRoute {
	return []apiRoute{
		{"/metrics", api.GetServiceStatistics},
		{"/service-info", api.GetServiceInfoAPI},
		{"/service-metrics", api.GetServiceMetricsFromStorage},
		{"/go-routines-stats", api.GetGoRoutinesStats},
		{"/function", api.GetFunctionTraceDetails},
		{"/function-details", api.ViewFunctionMetrics},
		{"/reports", api.GetReportData},
		{"/exporters", api.GetExportersStatus},
	}
}

// apiHandlerMap builds the full path-to-handler map for a given base path,
// including the root-level Prometheus endpoint.
func apiHandlerMap(apiPath string) map[string]http.HandlerFunc {
	routes := apiRoutes()
	handlers := make(map[string]http.HandlerFunc, len(routes)+1)
	for _, rt := range routes {
		handlers[apiPath+rt.Suffix] = rt.Handler
	}
	handlers[prometheusPath] = api.PrometheusMetricsHandler
	return handlers
}

// lookupAPIHandler resolves a request path to its handler, or nil when the
// path is not part of the API surface.
//
// The exact match on prometheusPath is tested first so that a caller who
// configures "/metrics" as their API base path still gets a working scrape
// endpoint rather than shadowing it with "/metrics/metrics".
func lookupAPIHandler(path, apiPath string) http.HandlerFunc {
	if path == prometheusPath {
		return api.PrometheusMetricsHandler
	}
	for _, rt := range apiRoutes() {
		if path == apiPath+rt.Suffix {
			return rt.Handler
		}
	}
	return nil
}

// isAPIPath reports whether a request should be dispatched to the API rather
// than to the static site.
func isAPIPath(path, apiPath string) bool {
	return path == prometheusPath || strings.HasPrefix(path, apiPath)
}
