# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed
- **`WithOTelEndpoint` connected to the collector and never sent a metric.**
  `Registry`, `Pipeline` and `MultiExporter` were each implemented and each
  unit-tested, and nothing ever constructed them, so `OTelExporter.Export` was
  unreachable from a running service. The exporter was created, logged as
  `OTel exporter initialized`, and shut down again without being asked for
  anything in between; because its instruments are registered lazily on the
  first `Export`, its periodic reader collected an empty set forever. Measured
  against a real OTLP receiver: **0 metrics before, 5 within 30s after**

## [1.6.0] - 2026-08-29

### Added
- **Per-function call counts and approximate latency percentiles.** `CALLS`,
  `P50` and `P95` on the Functions table. The distribution comes from a
  fixed-bucket histogram -- 216 bytes per function regardless of call volume,
  against ~10 MB at the function cap for keeping raw samples -- so the values
  are bucket upper bounds rather than interpolations, shown as `~4ms` meaning
  "at most 4ms". Below 20 calls the columns read `—`: p95 of three calls is the
  slowest of three, not a percentile

### Fixed
- **`GET /metrics` answered HTTP 500 under `RegisterDashboardHandlers` and 404
  under Fiber**, while the same path answered 200 under `RegisterAPIHandlers`.
  The Prometheus endpoint is served at the root rather than beneath the API
  base path, and the unified handlers dispatched to the API only on that
  prefix, so the scrape endpoint fell through to the static file server. Any
  consumer mounting the dashboard through `RegisterDashboardHandlers` or
  `RegisterSecuredDashboardHandlers` -- or through Fiber -- had Prometheus
  marking the target down
- The API surface was declared separately in five registration sites, which is
  how the above drifted. `routes.go` now declares it once and all five derive
  from it, and a test drives every route through every registration style
- **A window predating service start returned HTTP 500.** Both read endpoints
  clamp the requested start up to the service start time, so a window *ending*
  before startup became `start > end`, which the storage layer rejects. The
  dashboard reads a failed series fetch as a lost connection, so asking for an
  old range reported the service as having stopped answering
- The two storage backends disagreed about an empty result: `tstorage` returned
  an error where the in-memory backend returned an empty slice. Both now answer
  `(nil, nil)`
- **`pprof.StartCPUProfile`'s error was discarded.** CPU profiling is a
  process-wide singleton, so a second traced call sampled concurrently got
  "already in use", the error vanished, and the caller recorded a profile path
  pointing at the zero-byte file `os.Create` had just truncated -- or at one the
  other call was writing
- The Reports range control offered a `5m` button its renderer had no entry for;
  clicking it threw out of the handler and silently stopped the page. Its LIVE
  pill was permanently green and wired to nothing
- The runtime chart never refreshed while LIVE was on, and the "not enough
  history" message hardcoded a 5m sync interval regardless of configuration
- `tstorage` data from a test run was committed to the repo, so running the
  suite modified a tracked file and left the working tree dirty

### Known gaps
One thing the design canvas shows that the dashboard deliberately does not,
because the data behind it does not exist yet. It is tracked rather than faked,
and the page is absent rather than filled with plausible numbers:

- **Exporters page** — needs Prometheus and OTel status surfaced server-side (#67)

## [1.5.0] - 2026-08-29

The dashboard rebuilt on the new design, and a set of measurements that
were reporting things they had not measured. The security fix that landed
alongside this work is in 1.4.1, released separately.

### Changed
- **The dashboard was rebuilt on the new design.** A 226px rail carrying the
  service identity, navigation and storage footprint; a top bar with the page
  title, window control, live indicator and theme toggle; and the failure states
  the old dashboard had no answer for -- an `as of` stamp, a staleness banner
  naming the last good reading, and dimmed content so stale numbers cannot be
  read as current
- **The embedded payload went from 7.9 MB to 352 KB.** Every byte of it shipped
  inside every binary importing this library and was downloaded by every
  `go get`, whether or not the dashboard was ever opened. The vendored Bootstrap
  template (4.3 MB) was held up by 26 class names and one custom scrollbar;
  ECharts (1.0 MB) drew a single chart, now inline SVG; 1.8 MB of PNG tile icons
  and a loading GIF were replaced by an icon sprite
- The default theme is light, and the theme mechanism moved from a class to
  `body[data-theme]`. Stored preferences carry over unchanged
- `GetCPUPrecent` and `GetCPULoad` no longer block. Both called
  `cpu.Percent(time.Second, ...)`, which *sleeps* for a second to take two
  samples, and `GET /metrics` reaches both -- so every dashboard poll spent two
  seconds in the handler before it could answer. **2.05s to 0.01s.** The reading
  is now taken once, non-blocking, and shared

### Added
- `common.LibraryVersion()` reports the version compiled into the binary, read
  from build info rather than declared as a constant. The dashboard shows it in
  the rail. A constant is a promise nobody keeps: the footer claimed `v1.0.0`
  for four releases after it stopped being true
- `GoroutineLeakReport.Groups` and `GroupsTotal` carry every distinct call
  stack, not only the offending ones. A breakdown by state cannot be computed
  from the offenders alone
- Guardrail tests for the invariants the front end has no other way to check:
  undefined CSS custom properties (including in HTML `style` attributes),
  self-referential tokens, tokens defined twice, `hidden` defeated by a
  `display` rule, dead local page links, hardcoded versions, self-asserted
  credentials, and the embedded payload budget

- **The sidebar gains the design's shell**: a service identity block (name, PID, Go version and a connection dot), a `MONITOR` section label above the navigation, live counts on the Function Metrics and Go Routines items, and a storage footer showing the backend, retention window and on-disk footprint. All of it is chrome -- true of the instrument rather than of any one page -- so every page shows it and no page fetches it twice
- `/service-info` exposes `retention_period`, `storage_type` and `storage_on_disk`. `storage_on_disk` is omitted for the in-memory backend, since reporting `0 B` there would read as "nothing stored" rather than "not applicable"
- `common.GetRetentionPeriodString()` and `timeseries.GetStorageType()`
### Fixed
- **Traced-function memory was measured wrong, and reported when it had not been
  measured at all.** The delta came from `runtime.MemStats.Alloc`, which is bytes
  currently *live*, so a collection during the call dropped it and the
  difference went negative; that was guarded and otherwise reported as zero, so
  a heavy allocator that triggered a GC recorded as allocating nothing. Using
  `TotalAlloc`, which is cumulative, `main.highMemoryUsage` goes from a measured
  0 to a measured 766.52 MB. Separately, profiling runs on one call in
  `SamplingRate` (100 by default), so 99 calls in 100 had never been measured and
  showed a confident `0`; `memory_usage_sampled` now distinguishes the two and
  the dashboard renders an em dash for the unmeasured case
- The goroutine delta clamped negatives to zero, so a call that let goroutines
  finish looked identical to one that did nothing. It is also process-wide
  rather than per-function -- any other goroutine starting or finishing during
  the call moves it -- which is now stated at the type rather than implied away
  by the column heading
- The function detail panel labelled values "cumulative". Every `FunctionMetrics`
  field is overwritten by each traced call, so they all describe the most recent
  one
- **Staleness no longer decides whether goroutines are leaking.** A listener
  parked in netpoll and a signal handler waiting for SIGTERM are both
  permanently blocked and both healthy, and every Go server has them, so a
  staleness-driven verdict declared a leak on every process. The threshold had
  been pushed to 24 hours to stop it firing, which removed the only signal it
  carried. `LeakSuspected` now follows growth, which is what separates a leak
  from a long wait; stale counts remain as context, and the threshold returns to
  30 minutes
- A function row could be opened but not closed. `hidden` works by applying
  `display: none` from the UA stylesheet, which loses to every author rule, so
  `display: flex` silently defeated it. The same rule made every unopened row
  reserve 31px for an empty box
- The health ring shaded itself on a threshold invented in the front end while
  the text beside it used the server's verdict, so a brown ring could sit next
  to the word "Healthy". The server holds the configured limits and is the only
  thing that knows what healthy means for a service
- Three light-theme components put coloured text on a tint of its own colour,
  which holds on a dark ground and fails on a light one

### Note on v2.0.0
The `v2.0.0` tag remains unreachable and cannot be retracted. `retract v2.0.0`
in a module whose path has no `/v2` suffix is itself rejected -- *"version
v2.0.0 invalid: should be v0 or v1, not v2"* -- so the same rule that stops the
proxy serving the tag stops it being formally withdrawn. It is inert either way:
`go get @latest` resolves to the newest v1, and `go list -m -versions` does not
list it. It survives only on the GitHub releases page.

## [1.4.1] - 2026-08-29

Released on its own, ahead of the dashboard work, because none of it
depends on that and a credential the dashboard hands itself should not
wait behind a UI project.

### Security
- **The dashboard no longer asserts its own privilege.** `authenticatedFetch`
  attached `X-User-Role: admin` and a hardcoded `monigo-admin-secret` to every
  request that carried no API key, both lifted from
  `example/security-examples/custom-auth`, whose auth function grants access for
  precisely those. A consumer following that documented example got a dashboard
  that satisfied their own auth check on its own say-so. A browser cannot vouch
  for itself; custom authentication belongs in the middleware
- The API key moves from the query string to an `X-API-Key` header. As a query
  parameter it was recorded in browser history, sent in the `Referer` to any
  external link the page carried, and written to every access log in between.
  `APIKeyMiddleware` already accepted both forms

## [1.4.0] - 2026-08-28

### Added
- Design token layer in `static/css/monigo-styles.css`: 122 custom properties covering type, spacing, radius, elevation, five surface planes, four ink tones, five semantic states and an eight-colour chart series palette, in matched light and dark sets. Purely additive -- nothing consumes them yet, and removing both rules from the live stylesheet changes the computed style of zero of 713 sampled elements. Every documented contrast ratio is verified: text pairs clear 4.5:1, control borders and the focus ring clear 3:1

### Changed
- **Type and density follow the design's scale.** Metric tiles are now an uppercase micro-label over one large tabular number, card titles and body text sit on a six-step scale topping out at 24px rather than the vendored template's defaults, card padding is 16px, and table rows are tighter with a sticky header. Every `font-size` and `border-radius` in the project stylesheet now comes from a token: 12 ad-hoc sizes and 5 ad-hoc radii reduced to 6 and 3
- Metric labels drop their trailing colons, since an uppercase eyebrow does not take one
- **The dashboard adopts the new design's palette and collapses the dark theme onto it.** Colours now come from the design system: a cyan accent (`#00B4E6` dark / `#00758F` light) replacing the orange, five surface planes per theme, and five semantic states. Hex literals in real declarations drop from 105 to 2; `body.dark-theme` component rules from 53 to 3. Every text pair clears WCAG AA and every control boundary clears 3:1, verified in a running dashboard in both themes
- Focus rings restored. The vendored `.btn:focus { outline: none; box-shadow: none }` stripped every focus indicator with no replacement, so tabbing the dashboard moved nothing visually
- Stack traces and pprof output render in IBM Plex Mono at a fixed size on a recessed surface. The previous rule asked for `'Fira Code'`, which is not shipped and never resolved
- Table numerics use `tabular-nums`, so live columns stop jittering on each poll
- Cards, tables and muted text now read their colours from the design tokens rather than hardcoded hex literals. Colours shift slightly onto the corrected palette and every affected text pair gains contrast -- dark muted text goes from 5.78:1 to 8.38:1, dark card text from 13.34:1 to 14.98:1

## [1.3.0] - 2026-08-28

> Tagged at `a58a30f`, a branch commit that was subsequently squash-merged, so
> the tag is not an ancestor of `main`. Its contents are everything through the
> design token layer -- which is more than this section originally described, so
> the entries for the offline dashboard, the memory-chart fixes, the payload
> reduction and the asset guardrails have been folded in here rather than left
> under Unreleased. Published versions are immutable on the module proxy, so the
> tag stands; the record is corrected instead.

### Security
- `BasicAuthMiddleware` and `APIKeyMiddleware` now compare credentials with `crypto/subtle.ConstantTimeCompare` -- `!=` short-circuits on the first differing byte and leaks length and prefix information to a timing oracle
- `ViewFunctionMetrics` validates the function name and report type before invoking `go tool pprof` -- a name beginning with `-` was interpreted as a flag by pprof, and the report type was interpolated straight into the argument list
- `BasicAuthMiddleware` now sends a `WWW-Authenticate` challenge when a request carries no credentials at all

### Fixed
- **Silently truncated goroutine dumps**: `CollectGoRoutinesInfo` used a fixed 1 MB buffer with a single `runtime.Stack` call. `runtime.Stack` truncates without reporting it, so an application with thousands of goroutines got a clipped trace and undercounted -- precisely the situation goroutine leak detection needs to be accurate in. It now grows the buffer and retries, capped at 64 MB
- **Inverted health score**: `calculateServiceHealth` clamped the score to `100` -- the best possible value -- when usage exceeded every threshold, so a fully degraded service reported as healthy. It now returns `0`, matching `calculateSystemHealth`
- **Unbounded memory growth**: `InMemoryStorage.InsertRows` ignored the retention period and never evicted, so `WithStorageType("memory")` grew for the life of the process
- **`WithStorageType` had no effect**: the storage singleton was initialised by `SetDataPointsSyncFrequency` before `SetStorageType` was applied, so `"memory"` silently fell through to disk
- **Data race** on `serviceHealthThresholds` between `ConfigureServiceThresholds` and the metrics collection goroutine
- **Panic** in `common.ConvertToMB` on memory strings shorter than three characters, reachable from health calculation; `calculateMemoryUsagePercentage` also guards against a zero total
- **Panic** in `TraceFunctionWithArgs` / `TraceFunctionWithReturns` when passed a `nil` argument -- `reflect.ValueOf(nil).Type()` panics. Nillable parameter kinds now accept `nil` as the zero value
- `CloseStorage()` was permanently terminal via `sync.Once`; a subsequent `GetStorageInstance()` returned the closed handle instead of re-initialising
- Goroutine leak: each call to `SetDataPointsSyncFrequency` started a ticker goroutine without stopping the previous one
- Goroutine leak: `RateLimitMiddleware`'s cleanup goroutine is now stopped by `Shutdown()`, not only by the caller invoking the returned `stop`
- File descriptor leak in `WriteHeapProfile`; nil check in `StopCPUProfile`
- `registry.GetAll()` documented a snapshot copy but shared the `Labels` map with the live registry
- **The dashboard no longer requires internet access.** Font Awesome 4.7.0 and html2canvas were loaded from `cdnjs`, and the vendored template CSS pulled a Lato webfont from Google Fonts, so on an airgapped host -- where a lot of production Go services run -- icons were blank boxes and the screenshot feature was dead. All three fetches are gone: icons are an inline SVG sprite, html2canvas is vendored, and the font imports are removed
- **Mobile navigation was impossible in every network condition.** The sidebar and navbar menu buttons were `<i>` elements carrying `las la-bars` and `ri-menu-*` classes -- Line Awesome and Remix Icon -- and neither font was ever loaded: `font-family: remixicon` appeared in the vendored CSS with no `@font-face` rule and no font file anywhere in `static/`. They rendered as zero-size empty elements, so the sidebar could not be opened below 1300px and the navbar below 992px. Both are now real 22px controls
- Removed `static/css/core/intro.css` (192 KB), referenced by no page and carrying a third Google Fonts import
- **Memory Distribution pie plotted values of different magnitudes against each other.** The chart parsed a number out of the pre-formatted display strings (`"2.68 MB"`, `"12.93 GB"`), which discards the unit -- so a service using 2.68 MB rendered as 14.3% of the pie instead of 0.02%, a ~700x overstatement of its footprint
- **Heap Memory Usage chart mixed units under an "MB" axis.** It read `mem_stats_records[].record_value`, a display number whose unit lives in a separate `record_unit` field that the chart ignored. Once heap use crosses 1 GB, `HeapSys` reports in GB while `HeapAlloc` is still in MB, and the taller bar renders shorter. It now reads `raw_mem_stats_records`, which is in one consistent unit
- **CPU Statistics pie plotted `total_cores` as a slice alongside its own components**, so the chart always summed to twice the real core count. The third slice is now idle cores

### Added
- Goroutine leak detection: every collection cycle evaluates all goroutine stacks for stale goroutines (blocked past a threshold, default 24h, configurable via `WithStaleGoroutineThreshold`) and for call stacks growing monotonically across the last 5 cycles. The verdict is carried on `GoRoutinesStatistic.LeakReport` and `ServiceStats.GoroutineLeakReport`, surfaced as a warning panel on the Go Routines Stats page, and raises the alert webhook when configured
- `core.AnalyzeGoroutineLeaks()`, `core.SetStaleGoroutineThreshold()`, `core.GetStaleGoroutineThreshold()`, and `core.ResetLeakDetectionState()`
- Health breach webhook alerts via `WithAlertWebhook(url)` -- an opt-in `POST` fired when the system or service health score drops below 70. Dispatch is off the collection path, requests time out after 10 seconds, and deliveries are rate limited to one per 5 minutes across both alert types. `Build()` rejects a URL that is not `http://` or `https://`
- `common.GetServiceName()` -- accessor for the configured service name
- `core.GetThresholds()` -- thread-safe accessor for the configured health thresholds
- `Build()` validates `MaxCPUUsage` and `MaxMemoryUsage` are within 0-100 and `MaxGoRoutines` is non-negative
- Regression tests for every fix above
- Unformatted memory values on the metrics API: `total_system_memory_bytes`, `memory_used_by_system_bytes`, `memory_used_by_service_bytes`, `available_memory_bytes`, `stack_memory_usage_bytes`, `gc_pause_duration_ms`. Additive -- the existing formatted string fields are unchanged. Anything doing arithmetic on or plotting these metrics must use the raw fields, since the strings carry a unit suffix

### Changed
- `monigo.gif` re-encoded from 65 MB to 24 MB -- the module zip is downloaded on every `go get`
- Argument marshalling shared between `TraceFunctionWithArgs` and `TraceFunctionWithReturns` instead of duplicated
- **The embedded dashboard shrank from 17.1 MB to 7.9 MB (-54%), and from 71 files to 46.** Everything under `static/` is compiled into the consuming service's binary by `//go:embed static/*` and downloaded by every `go get`, so this is weight every user carried whether or not they ever opened the dashboard. Removed: `assets/ss/d1-d10.png` (7.6 MB), Product Hunt marketing images, an unused animated logo and dropdown arrow, and a favicon variant set -- 28 files, none referenced by any page, stylesheet, script or document. Note this does not shrink existing clones, since the blobs remain in git history; the module zip is what `go get` downloads

## [2.0.0] - 2026-02-10

> **Never published.** This release was tagged `v2.0.0`, but the Go module proxy
> rejects it: `go.mod` declares `module github.com/iyashjayesh/monigo` with no
> `/v2` suffix, and Go requires the major version to appear in the module path
> from v2 onwards. `go get` therefore continued to serve `v1.2.0`, and everything
> below was unreachable to anyone installing the library.
>
> ```
> $ curl -s https://proxy.golang.org/github.com/iyashjayesh/monigo/@v/v2.0.0.info
> not found: invalid version: module contains a go.mod file, so module path must
> match major version ("github.com/iyashjayesh/monigo/v2")
> ```
>
> The release line was renumbered rather than renaming the module: `/v2` would be
> permanent for an API that is not v2-shaped, and per the proxy there were no v2
> consumers to preserve. The changes below shipped in `1.3.0`.

### Breaking Changes
- Renamed `GetRuningPort()` to `GetRunningPort()`
- Renamed `ViewFunctionMaetrtics()` to `ViewFunctionMetrics()` (api package)
- `Build()` now panics on invalid config (missing ServiceName, bad port, bad StorageType)
- All API endpoints now enforce HTTP methods (GET/POST) -- wrong method returns 405
- `isStaticFile()` no longer bypasses auth for `.html` files
- `context.Context` added as first parameter to `TraceFunction`, `TraceFunctionWithArgs`, `TraceFunctionWithReturn`, `TraceFunctionWithReturns`, `GetServiceStats`
- Structured logging via `log/slog` replaces `log.Printf` -- use `WithLogger()` / `WithLogLevel()` to customize

### Fixed
- **Data loss**: removed `PurgeStorage()` from startup -- historical data now survives restarts
- **Data race** on `samplingRate` -- now uses `sync/atomic`
- `FunctionTraceDetails()` returns deep copy instead of raw map pointer
- Replaced `http.DefaultServeMux` with dedicated mux (prevents route collisions)
- All API handlers check marshal errors and return proper 500s
- Hardcoded `host=server1` label replaced with `os.Hostname()`
- Duplicate `"sys"` key in raw memory stats
- `GCCPUFraction` and counter metrics no longer incorrectly converted as bytes
- `ConvertBytesToUnit` now uses base-1024 (was base-1000)
- Prometheus exporter uses raw float64 values instead of parsing formatted strings
- Replaced deprecated `ioutil.ReadAll` with `io.ReadAll`

### Added
- Graceful shutdown with SIGINT/SIGTERM handling
- Builder validation at `Build()` time
- Comprehensive test suite across all packages (core, api, common, timeseries, config)
- Benchmarks for hot paths (core, timeseries, common)
- OpenTelemetry exporter option via `WithOTelEndpoint()`
- Structured logging via `log/slog` with `WithLogger()` and `WithLogLevel()` builder options
- `context.Context` propagation through public API
- Decoupled `Storage` interface from tstorage types (monigo-owned `Label`, `DataPoint`, `Row`)

### Changed
- CI updated to Go 1.24, with race detector and `go vet`
- Storage interface uses monigo-owned types instead of tstorage types
