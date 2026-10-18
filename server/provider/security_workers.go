package provider

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/anaregdesign/lantern/server/internal/readinessdiag"
	"github.com/anaregdesign/lantern/server/readiness"
)

// SecurityWorkers have no data-RPC network calls. App owns their lifetime.
type SecurityWorkers struct {
	runtime *SecurityRuntime
	peer    *PeerIdentityRuntime
	gate    *readiness.Gate
	logger  *slog.Logger
}

func NewSecurityWorkers(runtime *SecurityRuntime, peer *PeerIdentityRuntime, gate *readiness.Gate, logger *slog.Logger) *SecurityWorkers {
	return &SecurityWorkers{runtime, peer, gate, logger}
}
func (r *SecurityRuntime) Ready(ctx context.Context) bool {
	ready, _ := r.readyObserved(ctx)
	return ready
}
func (r *SecurityRuntime) readyObserved(ctx context.Context) (bool, string) {
	if r == nil {
		return false, "unavailable"
	}
	if r.peer != nil && r.peer.CheckWorkload(ctx) != nil {
		return false, "unavailable"
	}
	if r.mode == "off" {
		return true, "ready"
	}
	if r.current != nil {
		ready := r.current.Ready(ctx)
		if ready {
			return true, "ready"
		}
		return false, "unavailable"
	}
	return false, "unavailable"
}

// ObserveReadiness is a native fixture composition hook, called before Run or
// listener serving. It changes only passive observation, never configuration.
func (w *SecurityWorkers) ObserveReadiness(out io.Writer) {
	if w != nil && w.runtime != nil {
		w.runtime.readinessObservation = readinessdiag.New(out)
	}
}

func (w *SecurityWorkers) updateReadiness(ctx context.Context) {
	var observation *readinessdiag.Observer
	if w.runtime != nil {
		observation = w.runtime.readinessObservation
	}
	observation.WorkerStart()
	ready, reason := w.runtime.readyObserved(ctx)
	if w.peer != nil {
		ready = ready && w.peer.CheckWorkload(ctx) == nil
		if !ready && reason == "ready" {
			reason = "unavailable"
		}
	}
	observation.WorkerDone(reason)
	w.gate.SetServingPermission(ready)
	observation.WorkerPublished()
}
func (w *SecurityWorkers) Run(ctx context.Context) error {
	if w.runtime != nil {
		defer func() { w.runtime.readinessObservation.WorkerStopped(ctx.Err() != nil) }()
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	count := 0
	w.updateReadiness(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			count++
			if count%4 == 0 {
				if w.runtime != nil && w.runtime.current != nil && w.runtime.current.ReloadMembership(ctx) != nil {
					w.logger.Warn("control membership reload rejected")
				}
				if w.peer != nil && w.peer.ReloadMembership() != nil {
					w.logger.Warn("peer membership reload rejected", "reason", w.peer.store.FaultReason())
				}
			}
			w.updateReadiness(ctx)
		}
	}
}
