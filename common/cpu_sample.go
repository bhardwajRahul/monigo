package common

import (
	"sync"
	"time"

	"github.com/iyashjayesh/monigo/internal/logger"
	"github.com/shirou/gopsutil/cpu"
)

/*
System-wide CPU utilisation, sampled without blocking.

gopsutil's cpu.Percent(d, ...) sleeps for d to take two readings and diff them.
Both callers here asked for a full second, and GET /metrics reaches both, so
every dashboard poll spent two seconds inside the handler before it could
answer -- on the one endpoint the whole page waits for. The first paint showed
em dashes for that entire window, which reads as "no data" rather than "not
yet".

Passing 0 instead makes it a delta against gopsutil's previous reading and
returns immediately. That is also the better measurement for this use: the
dashboard polls on an interval, so the average across the interval says more
than a one-second spot sample taken at an arbitrary moment.

The reading is taken once and cached because gopsutil keeps the "previous
reading" in a package global. Two callers each passing 0 would shorten the
other's window to whatever gap happened to separate them, making both readings
noisy for no reason.
*/

const minCPUSampleInterval = 2 * time.Second

var cpuSample struct {
	mu    sync.Mutex
	taken time.Time
	value float64
}

// SystemCPUPercent returns total system CPU utilisation as a percentage.
// It never blocks. A reading younger than minCPUSampleInterval is reused.
func SystemCPUPercent() float64 {
	cpuSample.mu.Lock()
	defer cpuSample.mu.Unlock()

	if !cpuSample.taken.IsZero() && time.Since(cpuSample.taken) < minCPUSampleInterval {
		return cpuSample.value
	}

	// interval 0: percentage since the previous call, computed from cached
	// counters rather than by sleeping.
	percents, err := cpu.Percent(0, false)
	if err != nil {
		logger.Log.Error("sampling system CPU", "error", err)
		return cpuSample.value // the last good reading, or zero if there is none
	}
	if len(percents) == 0 {
		return cpuSample.value
	}

	var total float64
	for _, p := range percents {
		total += p
	}
	cpuSample.value = total / float64(len(percents))
	cpuSample.taken = time.Now()
	return cpuSample.value
}

// PrimeCPUSampler takes the first reading so that the first request served
// after start-up has a real delta to report rather than the since-boot average
// gopsutil returns when it has no previous call to compare against.
func PrimeCPUSampler() {
	cpuSample.mu.Lock()
	defer cpuSample.mu.Unlock()
	if _, err := cpu.Percent(0, false); err != nil {
		logger.Log.Error("priming the CPU sampler", "error", err)
	}
}
