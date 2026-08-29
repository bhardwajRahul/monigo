package exporters

import (
	"context"
	"sync"
	"time"

	"github.com/iyashjayesh/monigo/core"
	"github.com/iyashjayesh/monigo/internal/exporter"
	"github.com/prometheus/client_golang/prometheus"
)

// MonigoCollector implements the prometheus.Collector interface.
//
// Prometheus pulls: MoniGo never exports to it, so the only honest status is
// when it was last scraped and how often. scrapeMu guards those counters,
// which Collect writes from whichever goroutine the registry scrapes on.
type MonigoCollector struct {
	scrapeMu    sync.Mutex
	scrapeCount uint64
	lastScrape  time.Time

	cpuUsage    *prometheus.Desc
	memoryUsage *prometheus.Desc
	goroutines  *prometheus.Desc

	diskReadBytes  *prometheus.Desc
	diskWriteBytes *prometheus.Desc
}

var (
	once      sync.Once
	collector *MonigoCollector
)

// NewMonigoCollector returns a singleton instance of MonigoCollector.
func NewMonigoCollector() *MonigoCollector {
	once.Do(func() {
		collector = &MonigoCollector{
			cpuUsage: prometheus.NewDesc(
				"monigo_cpu_usage_percent",
				"Current system CPU usage percentage.",
				nil, nil,
			),
			memoryUsage: prometheus.NewDesc(
				"monigo_memory_usage_bytes",
				"Current system memory usage in bytes.",
				nil, nil,
			),
			goroutines: prometheus.NewDesc(
				"monigo_goroutines_count",
				"Number of goroutines running.",
				nil, nil,
			),
			diskReadBytes: prometheus.NewDesc(
				"monigo_disk_read_bytes_total",
				"Total bytes read from disk.",
				nil, nil,
			),
			diskWriteBytes: prometheus.NewDesc(
				"monigo_disk_write_bytes_total",
				"Total bytes written to disk.",
				nil, nil,
			),
		}
	})
	return collector
}

// Describe sends the super-set of all possible descriptors of metrics
// collected by this Collector to the provided channel.
func (c *MonigoCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.cpuUsage
	ch <- c.memoryUsage
	ch <- c.goroutines
	ch <- c.diskReadBytes
	ch <- c.diskWriteBytes
}

// Collect is called by the Prometheus registry when collecting metrics.
func (c *MonigoCollector) Collect(ch chan<- prometheus.Metric) {
	c.scrapeMu.Lock()
	c.scrapeCount++
	c.lastScrape = time.Now()
	c.scrapeMu.Unlock()

	stats := core.GetServiceStats(context.Background())

	// CPU Load - use raw float64 values directly, no string parsing
	ch <- prometheus.MustNewConstMetric(
		c.cpuUsage,
		prometheus.GaugeValue,
		stats.LoadStatistics.SystemCPULoadRaw,
	)

	// Memory - use raw bytes value directly
	ch <- prometheus.MustNewConstMetric(
		c.memoryUsage,
		prometheus.GaugeValue,
		stats.MemoryStatistics.MemoryUsedBySystemRaw,
	)

	// Goroutines
	ch <- prometheus.MustNewConstMetric(
		c.goroutines,
		prometheus.GaugeValue,
		float64(stats.CoreStatistics.Goroutines),
	)

	// Disk I/O
	ch <- prometheus.MustNewConstMetric(
		c.diskReadBytes,
		prometheus.CounterValue,
		float64(stats.DiskIO.ReadBytes),
	)
	ch <- prometheus.MustNewConstMetric(
		c.diskWriteBytes,
		prometheus.CounterValue,
		float64(stats.DiskIO.WriteBytes),
	)
}

// ScrapeHealth reports how the Prometheus endpoint is being used.
//
// There is no failure counter: a scrape that fails, fails at the client, and
// MoniGo never learns of it. Reporting a fabricated zero would imply a health
// signal that does not exist, so the pull kind carries no failure fields and
// the dashboard renders it as "scraped Ns ago" rather than a pass/fail verdict.
func (c *MonigoCollector) ScrapeHealth() exporter.Health {
	c.scrapeMu.Lock()
	defer c.scrapeMu.Unlock()
	return exporter.Health{
		Name:        "prometheus",
		Kind:        exporter.KindPull,
		LastSuccess: c.lastScrape,
		Total:       c.scrapeCount,
	}
}
