package core

import (
	"testing"
	"time"
)

// A percentile over a handful of calls is not a percentile, it is the slowest
// of a handful. Reporting one invites it to be read as a stable property of the
// function, so below the floor the summary refuses to answer.
func TestPercentilesRefuseToAnswerBelowTheSampleFloor(t *testing.T) {
	t.Cleanup(resetLatencies)
	resetLatencies()

	for i := 0; i < minSamplesForPercentile-1; i++ {
		observeLatency("few", 5*time.Millisecond)
	}
	if _, _, _, ok := latencySummary("few"); ok {
		t.Errorf("summary reported as reliable with %d samples, floor is %d",
			minSamplesForPercentile-1, minSamplesForPercentile)
	}

	observeLatency("few", 5*time.Millisecond) // now at the floor
	p50, p95, n, ok := latencySummary("few")
	if !ok {
		t.Fatalf("summary still unreliable at the floor (%d samples)", n)
	}
	if p50 <= 0 || p95 <= 0 {
		t.Errorf("p50=%v p95=%v, both should be positive once reliable", p50, p95)
	}
}

// An unseen function has no distribution, which is different from one whose
// calls were all instant.
func TestUnknownFunctionHasNoSummary(t *testing.T) {
	t.Cleanup(resetLatencies)
	resetLatencies()
	if _, _, n, ok := latencySummary("never-called"); ok || n != 0 {
		t.Errorf("unknown function reported samples=%d ok=%v, want 0/false", n, ok)
	}
}

// The quantile must land in the right region of the distribution: a run that is
// overwhelmingly fast with a slow tail should put p50 near the fast bucket and
// p95 out in the slow one.
func TestQuantilesTrackTheDistribution(t *testing.T) {
	t.Cleanup(resetLatencies)
	resetLatencies()

	// 90/10, not 95/5. With exactly 5% slow the 950th ordered value of 1000 is
	// still the last fast one, so p95 lands on the fast bucket -- correct
	// arithmetic, and a poor test of whether the tail is found at all.
	for i := 0; i < 900; i++ {
		observeLatency("skewed", 10*time.Microsecond)
	}
	for i := 0; i < 100; i++ {
		observeLatency("skewed", 2*time.Second)
	}

	p50, p95, n, ok := latencySummary("skewed")
	if !ok {
		t.Fatalf("expected a reliable summary from %d samples", n)
	}
	if p50 > time.Millisecond {
		t.Errorf("p50 = %v, want it down with the 90%% of 10µs calls", p50)
	}
	if p95 < 100*time.Millisecond {
		t.Errorf("p95 = %v, want it out in the 2s tail", p95)
	}
	if p95 < p50 {
		t.Errorf("p95 %v is below p50 %v", p95, p50)
	}
}

// The returned value is a bucket upper bound, so the true duration is at or
// below it. A quantile that under-reports would be the dangerous direction:
// it would say a function is faster than it is.
func TestQuantileIsAnUpperBoundNotAnUnderestimate(t *testing.T) {
	t.Cleanup(resetLatencies)
	resetLatencies()

	const actual = 3 * time.Millisecond
	for i := 0; i < 200; i++ {
		observeLatency("steady", actual)
	}
	p50, p95, _, ok := latencySummary("steady")
	if !ok {
		t.Fatal("expected a reliable summary")
	}
	if p50 < actual || p95 < actual {
		t.Errorf("p50=%v p95=%v below the observed %v; a percentile must never "+
			"report a function as faster than it ran", p50, p95, actual)
	}
}

// Memory is the whole reason for choosing buckets over raw samples, so the cost
// per function must stay fixed however many calls it takes.
func TestHistogramCostDoesNotGrowWithCallCount(t *testing.T) {
	t.Cleanup(resetLatencies)
	resetLatencies()

	for i := 0; i < 100000; i++ {
		observeLatency("hot", time.Duration(i%1000)*time.Microsecond)
	}
	latencyMu.Lock()
	h := latencies["hot"]
	latencyMu.Unlock()

	if got := len(h.counts); got != histogramBuckets {
		t.Errorf("bucket count = %d after 100k calls, want a fixed %d", got, histogramBuckets)
	}
	if h.total != 100000 {
		t.Errorf("total = %d, want every call counted", h.total)
	}
}

// The distribution map is capped like functionMetrics, so a process that traces
// an unbounded number of distinct functions cannot grow without limit.
func TestDistributionMapIsCapped(t *testing.T) {
	t.Cleanup(resetLatencies)
	resetLatencies()

	for i := 0; i < maxTrackedFunctions+50; i++ {
		observeLatency(string(rune(i%256))+"-"+time.Duration(i).String(), time.Millisecond)
	}
	latencyMu.Lock()
	n := len(latencies)
	latencyMu.Unlock()

	if n > maxTrackedFunctions {
		t.Errorf("tracking %d distributions, cap is %d", n, maxTrackedFunctions)
	}
}

// The quantile convention matters and is easy to get backwards. With exactly 5%
// of calls slow, the 95th percentile of 1000 samples is the 950th ordered
// value, which is still a fast one -- the tail begins at 951. This pins that,
// because the obvious-looking expectation is the wrong one and a future change
// to the rounding would slip past without it.
func TestP95SitsBelowATailOfExactlyFivePercent(t *testing.T) {
	t.Cleanup(resetLatencies)
	resetLatencies()

	for i := 0; i < 950; i++ {
		observeLatency("edge", 10*time.Microsecond)
	}
	for i := 0; i < 50; i++ {
		observeLatency("edge", 2*time.Second)
	}

	_, p95, _, ok := latencySummary("edge")
	if !ok {
		t.Fatal("expected a reliable summary")
	}
	if p95 > time.Millisecond {
		t.Errorf("p95 = %v; with exactly 5%% slow the 950th value is still fast, "+
			"so p95 should not reach the tail", p95)
	}
}
