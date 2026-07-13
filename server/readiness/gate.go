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
// This preserves the platform startup contract — empty LANTERN_PEERS means the
// readiness probe goes green as soon as the server is up.
package readiness

import (
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/grpchealth"
)

// HealthSetter is the narrow surface of *grpchealth.StaticChecker (wrapped
// by *provider.HealthChecker) consumed by Gate. Defined here so the
// readiness package stays free of any concrete health-server dependency.
type HealthSetter interface {
	SetServingStatus(service string, status grpchealth.Status)
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
//   - BeginDrain() forces NOT_SERVING permanently regardless of mode, lag,
//     or bootstrap state (the graceful-shutdown drain signal, #768). It is
//     one-way: once draining, no later SetLag / MarkBootstrapped can flip
//     the Gate back to SERVING.
type Gate struct {
	maxLag   uint64
	hasPeers bool
	health   HealthSetter

	mu           sync.Mutex
	bootstrapped bool
	draining     bool
	lags         map[string]uint64 // key = peer + "\x00" + origin
	searchConfig map[string]bool   // peer -> fingerprint match
	current      grpchealth.Status

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
		maxLag:       maxLag,
		hasPeers:     hasPeers,
		health:       hs,
		lags:         make(map[string]uint64),
		searchConfig: make(map[string]bool),
		current:      grpchealth.StatusNotServing,
	}
	if !hasPeers {
		// Single-instance: ready immediately. Surface SERVING via the
		// health setter so probes see green from t=0.
		g.bootstrapped = true
		g.current = grpchealth.StatusServing
		g.ready.Store(true)
		if hs != nil {
			hs.SetServingStatus("", grpchealth.StatusServing)
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

// BeginDrain flips the Gate to NOT_SERVING and latches it there for the
// rest of the process lifetime (#768). It is the graceful-shutdown drain
// signal: on SIGTERM the server calls BeginDrain so the overall ("")
// gRPC health entry and the /readyz HTTP probe report NOT_SERVING
// immediately — load balancers deregister the instance and stop routing
// new requests — while the listener keeps serving for the drain window.
// It applies in single-instance mode too (that node still has a /readyz)
// and is idempotent.
func (g *Gate) BeginDrain() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.draining {
		return
	}
	g.draining = true
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

// SetSearchConfig records whether a peer reports the exact local search
// capability fingerprint. Any observed mismatch keeps a replicated node out
// of readiness; single-instance deployments bypass the check.
func (g *Gate) SetSearchConfig(peer string, matched bool) {
	if !g.hasPeers {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if previous, ok := g.searchConfig[peer]; ok && previous == matched {
		return
	}
	g.searchConfig[peer] = matched
	g.evaluateLocked()
}

// Ready reports the current readiness verdict without taking the lock.
// Safe to call from HTTP probe handlers on every request.
func (g *Gate) Ready() bool { return g.ready.Load() }

func (g *Gate) readyLocked() bool {
	// Draining wins over every other consideration, including
	// single-instance mode's always-ready shortcut.
	if g.draining {
		return false
	}
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
	for _, matched := range g.searchConfig {
		if !matched {
			return false
		}
	}
	return true
}

func (g *Gate) evaluateLocked() {
	want := grpchealth.StatusNotServing
	if g.readyLocked() {
		want = grpchealth.StatusServing
	}
	if want == g.current {
		return
	}
	g.current = want
	g.ready.Store(want == grpchealth.StatusServing)
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

// OnSearchConfig is shared by the pump and anti-entropy metric adapters.
func (g *Gate) OnSearchConfig(peer string, matched bool) {
	g.SetSearchConfig(peer, matched)
}

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
