package core

import (
	"math"
	"sync"
	"time"
)

/*
A bounded latency distribution per traced function.

The dashboard wants p50 and p95, which need a distribution rather than the
single last-call duration FunctionMetrics carries. Keeping the raw durations
would give exact percentiles, but at maxTrackedFunctions (10 000) a ring of 128
int64s per function is roughly 10 MB held permanently -- in a library whose
whole argument is that it is cheap to embed, and which just cut its dashboard
payload from 7.9 MB to 356 KB for that reason.

So: fixed exponential buckets. Each function costs one fixed-size array of
counters regardless of how often it is called, which puts the ceiling at roughly
800 KB for the same 10 000 functions. Percentiles come back at bucket
resolution, which is enough to answer "which function is slow" -- the question
the column exists for -- and is not enough to quote as an SLO. Callers are told
which, via ApproximateP50/P95 naming and the doc on the model fields.

Timing is not sampled: executeFunctionWithProfiling measures every call, and
only *profiling* runs at one call in samplingRate. So the distribution covers
every call, not one percent of them.
*/

const (
	// histogramBuckets spans 1µs to ~17s in powers of four, which brackets
	// everything from a tight loop to a call that should have timed out.
	// Powers of four rather than two keeps the array short at the cost of
	// coarser resolution; at p95 the bucket edges matter less than the
	// magnitude.
	histogramBuckets = 13

	// histogramBase is the upper bound of the first bucket.
	histogramBase = time.Microsecond

	// minSamplesForPercentile is the point below which a percentile stops
	// being a summary and becomes an anecdote. p95 of three calls is just the
	// slowest of three; reporting it as a percentile invites someone to read
	// it as a stable property of the function.
	minSamplesForPercentile = 20
)

// bucketUpperBounds[i] is the inclusive upper bound of bucket i. The final
// bucket is unbounded.
var bucketUpperBounds = func() [histogramBuckets]time.Duration {
	var b [histogramBuckets]time.Duration
	d := histogramBase
	for i := 0; i < histogramBuckets; i++ {
		b[i] = d
		d *= 4
	}
	b[histogramBuckets-1] = time.Duration(math.MaxInt64)
	return b
}()

type latencyHistogram struct {
	counts [histogramBuckets]uint64
	total  uint64
}

func (h *latencyHistogram) observe(d time.Duration) {
	if d < 0 {
		return
	}
	for i := 0; i < histogramBuckets; i++ {
		if d <= bucketUpperBounds[i] {
			h.counts[i]++
			h.total++
			return
		}
	}
	// Unreachable: the last bound is MaxInt64. Kept so a future edit to the
	// bounds cannot silently drop observations.
	h.counts[histogramBuckets-1]++
	h.total++
}

/*
quantile returns the upper bound of the bucket in which the requested quantile
falls, and whether there were enough samples for the answer to mean anything.

The value is deliberately the bucket's upper bound rather than an interpolation
across it: interpolating would produce a precise-looking number the data does
not support. A caller that sees 4ms should read "at most 4ms", which is true,
rather than "4.13ms", which is invented.
*/
func (h *latencyHistogram) quantile(q float64) (time.Duration, bool) {
	if h.total < minSamplesForPercentile {
		return 0, false
	}
	target := uint64(math.Ceil(q * float64(h.total)))
	if target == 0 {
		target = 1
	}
	var cumulative uint64
	for i := 0; i < histogramBuckets; i++ {
		cumulative += h.counts[i]
		if cumulative >= target {
			return bucketUpperBounds[i], true
		}
	}
	return bucketUpperBounds[histogramBuckets-1], true
}

var (
	latencyMu sync.Mutex
	latencies = make(map[string]*latencyHistogram)
)

// observeLatency records one call duration for a function.
func observeLatency(name string, d time.Duration) {
	latencyMu.Lock()
	defer latencyMu.Unlock()

	h, ok := latencies[name]
	if !ok {
		// Bounded by the same cap as functionMetrics, and evicted the same way,
		// so the two maps cannot drift into holding different function sets.
		if len(latencies) >= maxTrackedFunctions {
			for k := range latencies {
				delete(latencies, k)
				break
			}
		}
		h = &latencyHistogram{}
		latencies[name] = h
	}
	h.observe(d)
}

// latencySummary returns the approximate p50 and p95 for a function, and the
// number of observations behind them. ok is false when there have been too few
// calls for a percentile to be meaningful.
func latencySummary(name string) (p50, p95 time.Duration, samples uint64, ok bool) {
	latencyMu.Lock()
	defer latencyMu.Unlock()

	h, exists := latencies[name]
	if !exists {
		return 0, 0, 0, false
	}
	p50, ok50 := h.quantile(0.50)
	p95, ok95 := h.quantile(0.95)
	return p50, p95, h.total, ok50 && ok95
}

// resetLatencies clears the distributions. For tests.
func resetLatencies() {
	latencyMu.Lock()
	defer latencyMu.Unlock()
	latencies = make(map[string]*latencyHistogram)
}
