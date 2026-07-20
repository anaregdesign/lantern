package provider

import (
	"time"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
	domainmetrics "github.com/anaregdesign/lantern/server/metrics"
	"github.com/anaregdesign/lantern/server/readiness"
	"github.com/anaregdesign/lantern/server/replication"
)

// ReadinessConfig groups the LB-drain hook knobs (#188).
//
//   - LANTERN_MAX_REPLICATION_LAG       maximum tolerated per-(peer, origin)
//     lag in mutation-seq units before the overall ("") gRPC health entry
//     flips to NOT_SERVING. Default 10000. Single-instance deployments
//     (no static peers and no dynamic discovery) bypass gating entirely.
type ReadinessConfig struct {
	MaxLag uint64
}

// NewReadinessConfig selects the Readiness slice of Config.
func NewReadinessConfig(c *Config) ReadinessConfig { return c.Readiness }

// loadReadinessConfig reads ReadinessConfig from the environment. Called
// from NewConfig so all env reads remain colocated.
func loadReadinessConfig() ReadinessConfig {
	return ReadinessConfig{
		MaxLag: uint64(envconfig.Int("LANTERN_MAX_REPLICATION_LAG", 10_000)),
	}
}

// NewReadinessGate constructs the readiness Gate that drives the overall
// ("") gRPC health entry. Either static peers or a dynamic resolver selects
// peer mode; only a topology with neither bypasses lag gating.
func NewReadinessGate(rc ReadinessConfig, pc PeerConfig, resolver *PeerResolver, hc *HealthChecker) *readiness.Gate {
	hasDynamicPeers := resolver != nil && resolver.Source != nil
	return readiness.NewGate(rc.MaxLag, len(pc.Peers) > 0 || hasDynamicPeers, hc)
}

// pumpMetricsFanOut delegates replication.Metrics events to both
// *DomainMetrics (Prometheus counters) and the readiness Gate so a single
// pump emits to both without either subsystem importing the other.
type pumpMetricsFanOut struct {
	dm   *domainmetrics.DomainMetrics
	gate *readiness.Gate
}

// NewPumpMetrics returns a replication.Metrics that fans out to both the
// Prometheus collectors and the readiness gate. Provided as a wire seam so
// the pump constructor stays focused on its own dependencies.
func NewPumpMetrics(dm *domainmetrics.DomainMetrics, gate *readiness.Gate) replication.Metrics {
	return &pumpMetricsFanOut{dm: dm, gate: gate}
}

func (f *pumpMetricsFanOut) OnPumpConnect(peer string) {
	f.dm.OnPumpConnect(peer)
	f.gate.OnPumpConnect(peer)
}

func (f *pumpMetricsFanOut) OnPumpDisconnect(peer, reason string) {
	f.dm.OnPumpDisconnect(peer, reason)
	f.gate.OnPumpDisconnect(peer, reason)
}

func (f *pumpMetricsFanOut) OnPumpApply(peer string) {
	f.dm.OnPumpApply(peer)
	f.gate.OnPumpApply(peer)
}

func (f *pumpMetricsFanOut) OnPumpDropSelfEcho(peer string) {
	f.dm.OnPumpDropSelfEcho(peer)
	f.gate.OnPumpDropSelfEcho(peer)
}

func (f *pumpMetricsFanOut) OnPumpSnapshotReplayed(peer string, vertices, edges uint64, duration time.Duration) {
	f.dm.OnPumpSnapshotReplayed(peer, vertices, edges, duration)
	f.gate.OnPumpSnapshotReplayed(peer, vertices, edges, duration)
}

func (f *pumpMetricsFanOut) OnSearchConfig(peer string, matched bool) {
	f.dm.OnSearchConfig(peer, matched)
	f.gate.OnSearchConfig(peer, matched)
}

// antiEntropyMetricsFanOut delegates AntiEntropyMetrics events to both
// *DomainMetrics and the readiness Gate.
type antiEntropyMetricsFanOut struct {
	dm   *domainmetrics.DomainMetrics
	gate *readiness.Gate
}

// NewAntiEntropyMetrics returns a replication.AntiEntropyMetrics that fans
// out to both the Prometheus collectors and the readiness gate.
func NewAntiEntropyMetrics(dm *domainmetrics.DomainMetrics, gate *readiness.Gate) replication.AntiEntropyMetrics {
	return &antiEntropyMetricsFanOut{dm: dm, gate: gate}
}

func (f *antiEntropyMetricsFanOut) OnAntiEntropyCycle() {
	f.dm.OnAntiEntropyCycle()
	f.gate.OnAntiEntropyCycle()
}

func (f *antiEntropyMetricsFanOut) OnAntiEntropyTick(peer string) {
	f.dm.OnAntiEntropyTick(peer)
	f.gate.OnAntiEntropyTick(peer)
}

func (f *antiEntropyMetricsFanOut) OnAntiEntropyBehind(peer, origin string, gap uint64) {
	f.dm.OnAntiEntropyBehind(peer, origin, gap)
	f.gate.OnAntiEntropyBehind(peer, origin, gap)
}

func (f *antiEntropyMetricsFanOut) OnAntiEntropyCaughtUp(peer, origin string, applied uint64) {
	f.dm.OnAntiEntropyCaughtUp(peer, origin, applied)
	f.gate.OnAntiEntropyCaughtUp(peer, origin, applied)
}

func (f *antiEntropyMetricsFanOut) OnAntiEntropyError(peer, reason string) {
	f.dm.OnAntiEntropyError(peer, reason)
	f.gate.OnAntiEntropyError(peer, reason)
}

func (f *antiEntropyMetricsFanOut) OnSearchConfig(peer string, matched bool) {
	f.dm.OnSearchConfig(peer, matched)
	f.gate.OnSearchConfig(peer, matched)
}
