package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/iyashjayesh/monigo/common"
	"github.com/iyashjayesh/monigo/internal/logger"
	"github.com/iyashjayesh/monigo/models"
)

const maxTrackedFunctions = 10000

var (
	functionMetrics = make(map[string]*models.FunctionMetrics)
	basePath        = common.GetBasePath()

	samplingRate atomic.Int64
	callCounters = make(map[string]uint64)
	countersMu   sync.Mutex
)

func init() {
	samplingRate.Store(100)
}

// SetSamplingRate sets the sampling rate for function tracing: one call in
// every `rate` is profiled. The default is 100.
//
// # The sampled call pays about 200ms
//
// Profiling a call is not cheap and the cost does not scale with the work.
// pprof.StopCPUProfile blocks while the runtime flushes its profile buffer,
// which takes a roughly constant ~200ms regardless of how long the function
// ran. Measured here, a 1ms function:
//
//	sampled call:     202.9ms
//	unsampled call:     1.1ms
//	overhead:         201.8ms
//
// At the default rate a traced handler serving 100 rps stalls for ~200ms once
// per second. ExecutionTime does not show this -- it is captured before the
// stop -- so the recorded metric reads 1ms while the caller's goroutine was
// blocked for 203ms. Anyone comparing MoniGo's numbers against their own
// latency graphs will find them disagreeing on exactly one call in `rate`.
//
// # And the profile is usually empty
//
// Go's CPU profiler samples at 100Hz, so a call shorter than ~10ms typically
// finishes between two samples and captures nothing. At the default settings
// the common case is to pay the 200ms and get an empty profile.
//
// This closes off the obvious workaround: lowering `rate` to collect more
// profiles makes the stall more frequent, and raising it makes profiles rarer
// without making them any less empty. Trace functions that do enough work to
// be worth sampling, and leave hot, short ones untraced.
func SetSamplingRate(rate int) {
	if rate < 1 {
		rate = 1
	}
	samplingRate.Store(int64(rate))
}

// TraceFunction traces the function and captures the metrics
func TraceFunction(_ context.Context, f func()) {
	name := strings.ReplaceAll(runtime.FuncForPC(reflect.ValueOf(f).Pointer()).Name(), "/", "-")
	executeFunctionWithProfiling(name, f)
}

// FunctionTraceDetails returns a snapshot copy of the function trace details (thread-safe)
func FunctionTraceDetails() map[string]*models.FunctionMetrics {
	mu.Lock()
	defer mu.Unlock()

	result := make(map[string]*models.FunctionMetrics, len(functionMetrics))
	for k, v := range functionMetrics {
		copied := *v
		result[k] = &copied
	}
	return result
}

func buildArgValues(fnType reflect.Type, args []interface{}) ([]reflect.Value, bool) {
	if len(args) != fnType.NumIn() {
		logger.Log.Error("function argument count mismatch", "expected", fnType.NumIn(), "got", len(args))
		return nil, false
	}

	argValues := make([]reflect.Value, len(args))
	for i, arg := range args {
		expectedType := fnType.In(i)
		if arg == nil {
			switch expectedType.Kind() {
			case reflect.Interface, reflect.Ptr, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
				argValues[i] = reflect.Zero(expectedType)
			default:
				logger.Log.Error("argument type mismatch: cannot assign nil to non-nullable type", "index", i, "expected", expectedType)
				return nil, false
			}
			continue
		}

		argValue := reflect.ValueOf(arg)
		if !argValue.Type().AssignableTo(expectedType) {
			logger.Log.Error("argument type mismatch", "index", i, "expected", expectedType, "got", argValue.Type())
			return nil, false
		}
		argValues[i] = argValue
	}
	return argValues, true
}

// TraceFunctionWithArgs traces a function with parameters and captures the metrics
func TraceFunctionWithArgs(_ context.Context, f interface{}, args ...interface{}) {
	fnValue := reflect.ValueOf(f)
	if fnValue.Kind() != reflect.Func {
		logger.Log.Error("first argument must be a function", "type", fmt.Sprintf("%T", f))
		return
	}

	fnType := fnValue.Type()
	argValues, ok := buildArgValues(fnType, args)
	if !ok {
		return
	}

	name := generateFunctionName(fnValue, fnType)

	executeFunctionWithProfiling(name, func() {
		fnValue.Call(argValues)
	})
}

// TraceFunctionWithReturn traces a function and returns the first result.
func TraceFunctionWithReturn(ctx context.Context, f interface{}, args ...interface{}) interface{} {
	results := TraceFunctionWithReturns(ctx, f, args...)
	if len(results) > 0 {
		return results[0]
	}
	return nil
}

// TraceFunctionWithReturns traces a function and returns all results.
func TraceFunctionWithReturns(_ context.Context, f interface{}, args ...interface{}) []interface{} {
	fnValue := reflect.ValueOf(f)
	if fnValue.Kind() != reflect.Func {
		logger.Log.Error("first argument must be a function", "type", fmt.Sprintf("%T", f))
		return nil
	}

	fnType := fnValue.Type()
	argValues, ok := buildArgValues(fnType, args)
	if !ok {
		return nil
	}

	name := generateFunctionName(fnValue, fnType)

	var results []interface{}
	executeFunctionWithProfiling(name, func() {
		reflectResults := fnValue.Call(argValues)
		results = make([]interface{}, len(reflectResults))
		for i, result := range reflectResults {
			results[i] = result.Interface()
		}
	})

	return results
}

func generateFunctionName(fnValue reflect.Value, fnType reflect.Type) string {
	baseName := strings.ReplaceAll(runtime.FuncForPC(fnValue.Pointer()).Name(), "/", "-")

	if fnType.NumIn() > 0 {
		paramTypes := make([]string, fnType.NumIn())
		for i := 0; i < fnType.NumIn(); i++ {
			paramTypes[i] = fnType.In(i).String()
		}
		baseName = fmt.Sprintf("%s(%s)", baseName, strings.Join(paramTypes, ","))
	}

	if fnType.NumOut() > 0 {
		returnTypes := make([]string, fnType.NumOut())
		for i := 0; i < fnType.NumOut(); i++ {
			returnTypes[i] = fnType.Out(i).String()
		}
		baseName = fmt.Sprintf("%s->(%s)", baseName, strings.Join(returnTypes, ","))
	}

	return baseName
}

// sanitizeFileName replaces characters that are invalid in file paths.
func sanitizeFileName(name string) string {
	replacer := strings.NewReplacer(
		"(", "_", ")", "_",
		"<", "_", ">", "_",
		":", "_", "*", "_",
		"?", "_", "\"", "_",
		"|", "_", " ", "_",
	)
	return replacer.Replace(name)
}

func executeFunctionWithProfiling(name string, fn func()) {
	countersMu.Lock()
	if len(callCounters) > maxTrackedFunctions {
		// Evict oldest entries to prevent unbounded growth.
		for k := range callCounters {
			delete(callCounters, k)
			break
		}
	}
	callCounters[name]++
	count := callCounters[name]
	countersMu.Unlock()

	shouldProfile := count%uint64(samplingRate.Load()) == 0

	initialGoroutines := runtime.NumGoroutine()
	var memStatsBefore runtime.MemStats
	if shouldProfile {
		runtime.ReadMemStats(&memStatsBefore)
	}

	var cpuProfFilePath, memProfFilePath string
	var cpuProfileFile *os.File

	if shouldProfile {
		folderPath := fmt.Sprintf("%s/profiles", basePath)
		if err := os.MkdirAll(folderPath, os.ModePerm); err != nil {
			logger.Log.Warn("failed to create profiles directory", "error", err)
		}

		safeName := sanitizeFileName(name)
		cpuProfFilePath = filepath.Join(folderPath, fmt.Sprintf("%s_cpu.prof", safeName))
		memProfFilePath = filepath.Join(folderPath, fmt.Sprintf("%s_mem.prof", safeName))

		var err error
		cpuProfileFile, err = StartCPUProfile(cpuProfFilePath)
		if err != nil {
			// Usually "already in use": another traced call is being sampled,
			// and profiling is process-wide. Clearing the path keeps this call
			// honest about having no profile rather than pointing at a file
			// that is empty or belongs to the other call.
			logger.Log.Warn("failed to start CPU profile", "error", err)
			cpuProfFilePath = ""
		}
	}

	start := time.Now()
	fn()
	elapsed := time.Since(start)

	// Every call, not just profiled ones: timing is cheap and the distribution
	// is only meaningful if it covers everything.
	observeLatency(name, elapsed)

	if shouldProfile {
		StopCPUProfile(cpuProfileFile)
		if err := WriteHeapProfile(memProfFilePath); err != nil {
			logger.Log.Warn("failed to write heap profile", "error", err)
		}
	}

	// Process-wide, not per-function: any other goroutine starting or finishing
	// during the call moves this. The sign is kept -- clamping negatives to
	// zero meant a call that let goroutines finish looked identical to one that
	// did nothing, which is the opposite of the truth.
	goroutineDelta := runtime.NumGoroutine() - initialGoroutines

	var memoryUsage uint64
	if shouldProfile {
		var memStatsAfter runtime.MemStats
		runtime.ReadMemStats(&memStatsAfter)
		/*
		 * TotalAlloc, not Alloc.
		 *
		 * Alloc is bytes currently live, so a GC during the call drops it and
		 * the difference goes negative. The old code guarded that with
		 * `if after.Alloc >= before.Alloc` and otherwise reported zero -- which
		 * meant a heavy allocator that happened to trigger a collection was
		 * recorded as allocating nothing. main.highMemoryUsage measured 0 for
		 * exactly this reason.
		 *
		 * TotalAlloc is cumulative and never decreases, so the difference is
		 * the bytes this call actually allocated whether or not the collector
		 * ran. It cannot go negative, so no guard is needed.
		 */
		memoryUsage = memStatsAfter.TotalAlloc - memStatsBefore.TotalAlloc
	}

	mu.Lock()
	defer mu.Unlock()

	if len(functionMetrics) > maxTrackedFunctions {
		// Evict one arbitrary entry to cap memory.
		for k := range functionMetrics {
			delete(functionMetrics, k)
			break
		}
	}

	p50, p95, _, reliable := latencySummary(name)

	if m, exists := functionMetrics[name]; exists {
		m.FunctionLastRanAt = start
		m.ExecutionTime = elapsed
		m.CallCount = count
		m.ApproximateP50 = p50
		m.ApproximateP95 = p95
		m.PercentilesReliable = reliable
		m.GoroutineCount = goroutineDelta
		if shouldProfile {
			m.MemoryUsage = memoryUsage
			m.MemoryUsageSampled = true
			m.CPUProfileFilePath = cpuProfFilePath
			m.MemProfileFilePath = memProfFilePath
		}
	} else {
		functionMetrics[name] = &models.FunctionMetrics{
			FunctionLastRanAt:   start,
			ExecutionTime:       elapsed,
			CallCount:           count,
			ApproximateP50:      p50,
			ApproximateP95:      p95,
			PercentilesReliable: reliable,
			GoroutineCount:      goroutineDelta,
			MemoryUsage:         memoryUsage,
			MemoryUsageSampled:  shouldProfile,
			CPUProfileFilePath:  cpuProfFilePath,
			MemProfileFilePath:  memProfFilePath,
		}
	}
}

// ViewFunctionMetrics renders the stored profiles for one traced function.
//
// This used to shell out to `go tool pprof`, which meant the Go SDK had to be
// installed on the machine running the service. In a distroless, scratch or
// alpine image -- nearly every production deployment of a Go service -- it is
// not, and this returned the string "Error: 'go' command not found" as the
// report body. It also spawned a subprocess for every HTTP request.
//
// runtime/pprof writes fully symbolized profiles: the function names and file
// paths are inside the file. Parsing needs no toolchain, no binary and no
// source tree, so the reports are rendered in-process.
func ViewFunctionMetrics(name, reportType string, metrics *models.FunctionMetrics) models.FunctionTraceDetails {
	// A name arriving from a query string is no longer interpolated into a
	// command line, so this is no longer a shell-injection guard. It stays
	// because a name with control characters cannot match a real Go symbol and
	// signals a malformed request.
	if strings.HasPrefix(name, "-") || strings.ContainsAny(name, ";&|$` \t\n") {
		return models.FunctionTraceDetails{
			FunctionName: name,
			CoreProfile: models.Profiles{
				CPU: "Error: Invalid function name",
				Mem: "Error: Invalid function name",
			},
			FunctionCodeTrace: "Error: Invalid function name",
		}
	}

	return models.FunctionTraceDetails{
		FunctionName: name,
		CoreProfile: models.Profiles{
			CPU: renderProfileReport(metrics.CPUProfileFilePath, reportType),
			Mem: renderProfileReport(metrics.MemProfileFilePath, reportType),
		},
		FunctionCodeTrace: renderAnnotatedSource(metrics.CPUProfileFilePath, name),
	}
}
