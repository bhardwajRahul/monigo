package pipeline

import (
	"context"
	"sync"
	"time"

	"github.com/iyashjayesh/monigo/internal/exporter"
	"github.com/iyashjayesh/monigo/internal/logger"
	"github.com/iyashjayesh/monigo/internal/registry"
)

type Pipeline struct {
	registry *registry.Registry
	exporter exporter.Exporter
	interval time.Duration
	collect  func()
	stopChan chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

func NewPipeline(r *registry.Registry, e exporter.Exporter, interval time.Duration) *Pipeline {
	return &Pipeline{
		registry: r,
		exporter: e,
		interval: interval,
		stopChan: make(chan struct{}),
	}
}

// WithCollector sets a function run immediately before each export, to refresh
// the registry from live stats.
//
// It is a hook rather than a second goroutine on its own ticker so that the
// snapshot an export sends is the one taken moments earlier. Two independent
// tickers at the same interval drift, and an export would then carry a sample
// of arbitrary age.
func (p *Pipeline) WithCollector(collect func()) *Pipeline {
	p.collect = collect
	return p
}

func (p *Pipeline) Start(ctx context.Context) {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if p.collect != nil {
					p.collect()
				}
				metrics := p.registry.GetAll()
				if len(metrics) > 0 {
					if err := p.exporter.Export(ctx, metrics); err != nil {
						logger.Log.Error("pipeline export failed", "exporter", p.exporter.Name(), "error", err)
					}
				}
			case <-p.stopChan:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Stop gracefully stops the pipeline. Safe to call multiple times.
func (p *Pipeline) Stop() {
	p.stopOnce.Do(func() {
		close(p.stopChan)
	})
	p.wg.Wait()
}
