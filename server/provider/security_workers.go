package provider

import (
	"context"
	"github.com/anaregdesign/lantern/server/readiness"
	"log/slog"
	"time"
)

// SecurityWorkers have no data-RPC network calls. App owns their lifetime.
type SecurityWorkers struct {
	runtime *SecurityRuntime
	peer    *PeerIdentityRuntime
	policy  *SecurityPeerRuntime
	gate    *readiness.Gate
	logger  *slog.Logger
}

func NewSecurityWorkers(runtime *SecurityRuntime, peer *PeerIdentityRuntime, policy *SecurityPeerRuntime, gate *readiness.Gate, logger *slog.Logger) *SecurityWorkers {
	return &SecurityWorkers{runtime, peer, policy, gate, logger}
}
func (r *SecurityRuntime) Ready(ctx context.Context) bool {
	if r == nil {
		return false
	}
	if r.peer != nil && r.peer.CheckWorkload(ctx) != nil {
		return false
	}
	if r.mode == "off" {
		return true
	}
	if r.current != nil {
		return r.current.Ready(ctx)
	}
	if r.native == nil {
		return false
	}
	current, ok := r.native.Store().Current()
	return ok && r.authorityCheck(ctx, current) == nil
}
func (w *SecurityWorkers) Run(ctx context.Context) error {
	workerCtx, stop := context.WithCancel(ctx)
	defer stop()
	renewed := make(chan struct{})
	go func() {
		defer close(renewed)
		if w.policy != nil {
			w.policy.Run(workerCtx, func(error) { w.logger.Warn("policy renewal unavailable") })
		}
	}()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	count := 0
	update := func() {
		ready := w.runtime.Ready(ctx)
		if w.peer != nil {
			ready = ready && w.peer.CheckWorkload(ctx) == nil
		}
		w.gate.SetServingPermission(ready)
	}
	update()
	for {
		select {
		case <-ctx.Done():
			stop()
			<-renewed
			return nil
		case <-ticker.C:
			count++
			if w.peer != nil && count%4 == 0 {
				if w.peer.ReloadMembership() != nil {
					w.logger.Warn("peer membership reload rejected", "reason", w.peer.store.FaultReason())
				}
			}
			update()
		}
	}
}
