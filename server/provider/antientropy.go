package provider

import (
	"log/slog"
	"time"

	cachegraph "github.com/anaregdesign/lantern/core/cache/graph"
	v1 "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/replication"
	"github.com/anaregdesign/lantern/server/service"
)

// AntiEntropyConfig groups the periodic anti-entropy driver knobs (#186).
//
//   - LANTERN_ANTI_ENTROPY_INTERVAL_MS   tick cadence in ms. Default
//     30000 (30s). Set to 0 to disable the driver entirely while
//     keeping the rest of the replication stack enabled.
//   - LANTERN_ANTI_ENTROPY_SUBSCRIBE_TIMEOUT_MS  caps the duration of
//     a single catch-up Subscribe stream. Default 30000 (30s).
type AntiEntropyConfig struct {
	Interval         time.Duration
	SubscribeTimeout time.Duration
}

// NewAntiEntropyConfig selects the AntiEntropy slice of Config.
func NewAntiEntropyConfig(c *Config) AntiEntropyConfig { return c.AntiEntropy }

// loadAntiEntropyConfig reads AntiEntropyConfig from the environment.
// Called from NewConfig so all env reads remain colocated.
func loadAntiEntropyConfig() AntiEntropyConfig {
	return AntiEntropyConfig{
		Interval:         time.Duration(envconfig.Int("LANTERN_ANTI_ENTROPY_INTERVAL_MS", 30_000)) * time.Millisecond,
		SubscribeTimeout: time.Duration(envconfig.Int("LANTERN_ANTI_ENTROPY_SUBSCRIBE_TIMEOUT_MS", 30_000)) * time.Millisecond,
	}
}

// NewAntiEntropyDriver constructs the periodic anti-entropy driver
// (#186). The driver is the convergence safety net for the pump:
// once per Interval it polls each peer's PeerStatus and triggers a
// bounded Subscribe-or-Snapshot catch-up when the peer's own
// origin watermark exceeds the local one.
//
// Empty PeerConfig.Peers or a zero Interval yields a driver whose
// Run is a no-op (returns immediately), so the same wire graph
// supports single-instance, pump-only, and full-HA topologies.
func NewAntiEntropyDriver(
	pc PeerConfig,
	rc ReplicationConfig,
	ac AntiEntropyConfig,
	svc *service.LanternService,
	cache *cachegraph.GraphCache[string, *v1.Vertex],
	pump *replication.Pump,
	m replication.AntiEntropyMetrics,
	logger *slog.Logger,
) *replication.AntiEntropy {
	_ = pump // forces wire to construct the pump before the driver
	return replication.NewAntiEntropy(replication.AntiEntropyConfig{
		NodeID:           rc.NodeID,
		Peers:            pc.Peers,
		Interval:         ac.Interval,
		SubscribeTimeout: ac.SubscribeTimeout,
		Logger:           logger,
		Metrics:          m,
	}, svc, svc, cache)
}
