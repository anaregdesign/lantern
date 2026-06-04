// Package readiness implements the load-balancer drain hook (#188): a Gate
// observes per-peer replication lag plus a one-shot "bootstrap done" event,
// and drives the overall ("") gRPC health status from SERVING to
// NOT_SERVING when the local node is too far behind the cluster.
//
// The Gate satisfies both replication.AntiEntropyMetrics (lag observations)
// and replication.Metrics (pump connect/snapshot events) so it can be
// wired alongside the existing *DomainMetrics fan-in without changing the
// replication package surface.
//
// Single-instance mode (no peers configured) bypasses gating entirely: the
// Gate is born already-ready and any subsequent SetLag calls are no-ops.
// This preserves the PaaS startup contract — empty LANTERN_PEERS means the
// readiness probe goes green as soon as the server is up.
package readiness

import (
	"sync"
	"sync/atomic"
	"time"

	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// HealthSetter is the narrow surface of *health.Server consumed by Gate.
// Matches the same shape as service.HealthSetter so wire bindings reuse
// the existing *health.Server registration.
type HealthSetter interface {
	SetServingStatus(service string, status healthpb.HealthCheckResponse_ServingStatus)
}

// Gate tracks readiness state for the overall ("") gRPC health entry.
//
// Lifecycle:
//   - Constructed via NewGate with the lag threshold, the peer-mode flag,
//     and the *health.Server.
//   - In single-instance mode (hasPeers=false) the Gate is permanently
//     ready and Apply is a no-op — callers can still install it on the
//     anti-entropy / pump fan-out without special-casing.
//   - In multi-peer mode the Gate starts NOT_SERVING and flips to SERVING
//     after MarkBootstrapped() has fired AND no observed (peer, origin)
//     lag exceeds MaxLag. It flips back to NOT_SERVING whenever the lag
//     constraint is violated again.
type Gate struct {
	maxLag   uint64
	hasPeers bool
	health   HealthSetter

	mu           sync.Mutex
	bootstrapped bool
	lags         map[string]uint64 // key = peer + "\x00" + origin
	current      healthpb.HealthCheckResponse_ServingStatus

	// ready is a lock-free mirror of the current status so HTTP probes
	// can answer without contending with metric updates.
	ready atomic.Bool
}

// NewGate constructs a Gate. maxLag is the per-(peer, origin) seq gap
// above which the node reports NOT_SERVING. hasPeers selects single-
// instance vs multi-peer mode. hs receives the SetServingStatus("", ...)
// transitions and may be nil in tests.
func NewGate(maxLag uint64, hasPeers bool, hs HealthSetter) *Gate {
	g := &Gate{
		maxLag:   maxLag,
		hasPeers: hasPeers,
		health:   hs,
		lags:     make(map[string]uint64),
		current:  healthpb.HealthCheckResponse_NOT_SERVING,
	}
	if !hasPeers {
		// Single-instance: ready immediately. Surface SERVING via the
		// health setter so probes see green from t=0.
		g.bootstrapped = true
		g.current = healthpb.HealthCheckResponse_SERVING
		g.ready.Store(true)
		if hs != nil {
			hs.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
		}
	}
	return g
}

// MarkBootstrapped is the one-shot "we have seen the cluster at least once"
// signal. In multi-peer mode the Gate refuses to report SERVING until this
// has fired (and the lag constraint is satisfied). Repeated calls are
// no-ops. In single-instance mode this is a no-op.
func (g *Gate) MarkBootstrapped() {
	if !g.hasPeers {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.bootstrapped {
		return
	}
	g.bootstrapped = true
	g.evaluateLocked()
}

// SetLag records the latest observed lag for a (peer, origin) row in
// mutation-seq units. A value of 0 clears the row (caught up). In
// single-instance mode this is a no-op.
func (g *Gate) SetLag(peer, origin string, gap uint64) {
	if !g.hasPeers {
		return
	}
	k := peer + "\x00" + origin
	g.mu.Lock()
	defer g.mu.Unlock()
	if gap == 0 {
		if _, ok := g.lags[k]; !ok {
			return
		}
		delete(g.lags, k)
	} else {
		if prev, ok := g.lags[k]; ok && prev == gap {
			return
		}
		g.lags[k] = gap
	}
	g.evaluateLocked()
}

// Ready reports the current readiness verdict without taking the lock.
// Safe to call from HTTP probe handlers on every request.
func (g *Gate) Ready() bool { return g.ready.Load() }

func (g *Gate) readyLocked() bool {
	if !g.hasPeers {
		return true
	}
	if !g.bootstrapped {
		return false
	}
	for _, v := range g.lags {
		if v > g.maxLag {
			return false
		}
	}
	return true
}

func (g *Gate) evaluateLocked() {
	want := healthpb.HealthCheckResponse_NOT_SERVING
	if g.readyLocked() {
		want = healthpb.HealthCheckResponse_SERVING
	}
	if want == g.current {
		return
	}
	g.current = want
	g.ready.Store(want == healthpb.HealthCheckResponse_SERVING)
	if g.health != nil {
		g.health.SetServingStatus("", want)
	}
}

// --- replication.AntiEntropyMetrics adapter ---

// OnAntiEntropyCycle is a no-op: cycles themselves do not change readiness.
func (g *Gate) OnAntiEntropyCycle() {}

// OnAntiEntropyTick is a no-op: per-peer ticks without a verdict do not
// change readiness.
func (g *Gate) OnAntiEntropyTick(string) {}

// OnAntiEntropyBehind records a non-zero lag observation. Anti-entropy is
// the only source of authoritative lag data so this is the gate's primary
// SetLag entry point.
func (g *Gate) OnAntiEntropyBehind(peer, origin string, gap uint64) {
	g.SetLag(peer, origin, gap)
}

// OnAntiEntropyCaughtUp clears the lag for (peer, origin).
func (g *Gate) OnAntiEntropyCaughtUp(peer, origin string, _ uint64) {
	g.SetLag(peer, origin, 0)
}

// OnAntiEntropyError is a no-op: transport errors do not by themselves
// imply staleness — the next successful probe will update lag.
func (g *Gate) OnAntiEntropyError(string, string) {}

// --- replication.Metrics (pump) adapter ---

// OnPumpConnect marks bootstrap complete the first time any peer
// connection succeeds. The pump establishes Subscribe before any
// mutations need to flow, so the first connect is a sound "we are wired
// into the cluster" signal even when no peer has data to replay.
func (g *Gate) OnPumpConnect(string) { g.MarkBootstrapped() }

// OnPumpDisconnect is a no-op: transient disconnects do not flip
// readiness directly — anti-entropy will resurface lag if the peer
// remains unreachable long enough to matter.
func (g *Gate) OnPumpDisconnect(string, string) {}

// OnPumpApply is a no-op: apply counts are tracked by *DomainMetrics.
func (g *Gate) OnPumpApply(string) {}

// OnPumpDropSelfEcho is a no-op: self-echo suppression is metrics-only.
func (g *Gate) OnPumpDropSelfEcho(string) {}

// OnPumpSnapshotReplayed marks bootstrap complete. A finished bootstrap
// snapshot is the strongest "we have caught up" signal the pump emits.
func (g *Gate) OnPumpSnapshotReplayed(string, uint64, uint64, time.Duration) { g.MarkBootstrapped() }
