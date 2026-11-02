package lifecyclepolicy

import (
	"errors"
	"sync"
	"sync/atomic"
)

// ShutdownPlan is shared by App's drain coordinator and the HTTP server. The
// first shutdown action freezes one generation for both consumers, even when
// HTTP draining begins only after a long load-balancer drain window.
type ShutdownPlan struct {
	source Source
	once   sync.Once
	fixed  atomic.Pointer[Snapshot]
}

func NewShutdownPlan(source Source) (*ShutdownPlan, error) {
	if source == nil {
		return nil, errors.New("shutdown plan requires a lifecycle source")
	}
	return &ShutdownPlan{source: source}, nil
}

// Capture is called only when shutdown begins. Status reads and construction
// must not freeze future shutdown policy early.
func (p *ShutdownPlan) Capture() Snapshot {
	p.once.Do(func() {
		snapshot := p.source.LifecycleSnapshot()
		p.fixed.Store(&snapshot)
	})
	return *p.fixed.Load()
}

// Current observes the current effective policy until shutdown begins, then
// returns the frozen plan. It does not start shutdown or capture a generation.
func (p *ShutdownPlan) Current() Snapshot {
	if fixed := p.fixed.Load(); fixed != nil {
		return *fixed
	}
	snapshot := p.source.LifecycleSnapshot()
	// Capture may finish while the source read is in progress. Prefer its
	// immutable plan to a newer source generation observed after shutdown.
	if fixed := p.fixed.Load(); fixed != nil {
		return *fixed
	}
	return snapshot
}
