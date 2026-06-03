package provider

import (
	"log/slog"
	"strings"
	"time"

	cachegraph "github.com/anaregdesign/lantern/core/cache/graph"
	v1 "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/replication"
	"github.com/anaregdesign/lantern/server/service"
)

// PeerConfig groups the outbound peer-replication pump knobs (#185).
//
//   - LANTERN_PEERS                      CSV list of peer addresses
//     ("host:port,host:port"). Empty (the default) yields a no-op pump:
//     the server runs in single-instance mode. Whitespace around each
//     entry is trimmed; empty entries are dropped.
//   - LANTERN_PUMP_BACKOFF_MIN_MS        initial reconnect delay after a
//     pump session error. Default 250ms. Doubles on each successive
//     failure, capped at LANTERN_PUMP_BACKOFF_MAX_MS.
//   - LANTERN_PUMP_BACKOFF_MAX_MS        upper cap on the reconnect
//     delay. Default 30000ms (30s).
type PeerConfig struct {
	Peers      []string
	BackoffMin time.Duration
	BackoffMax time.Duration
}

// NewPeerConfig returns the PeerConfig slice of Config.
func NewPeerConfig(c *Config) PeerConfig { return c.Peer }

// loadPeerConfig reads PeerConfig from the environment. Called from
// NewConfig so all env reads remain colocated.
func loadPeerConfig() PeerConfig {
	raw := strings.TrimSpace(envconfig.String("LANTERN_PEERS", ""))
	var peers []string
	if raw != "" {
		for _, s := range strings.Split(raw, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				peers = append(peers, s)
			}
		}
	}
	return PeerConfig{
		Peers:      peers,
		BackoffMin: time.Duration(envconfig.Int("LANTERN_PUMP_BACKOFF_MIN_MS", 250)) * time.Millisecond,
		BackoffMax: time.Duration(envconfig.Int("LANTERN_PUMP_BACKOFF_MAX_MS", 30_000)) * time.Millisecond,
	}
}

// NewReplicationPump constructs the peer-replication pump. Empty
// PeerConfig.Peers yields a Pump whose Run is a no-op (returns
// immediately) so the same wire graph supports both single-instance
// and multi-peer topologies.
//
// The cache is bound here (rather than via wire.Bind) so the
// replication package has no transitive dependency on the GraphCache
// generic specialisation — it sees only the SnapshotApplier surface.
func NewReplicationPump(
	pc PeerConfig,
	rc ReplicationConfig,
	svc *service.LanternService,
	cache *cachegraph.GraphCache[string, *v1.Vertex],
	m replication.Metrics,
	logger *slog.Logger,
) *replication.Pump {
	return replication.NewPump(replication.Config{
		NodeID:     rc.NodeID,
		Peers:      pc.Peers,
		BackoffMin: pc.BackoffMin,
		BackoffMax: pc.BackoffMax,
		Logger:     logger,
		Metrics:    m,
	}, svc, cache)
}
