package provider

import (
	"log/slog"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	v1 "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/replication"
	"github.com/anaregdesign/lantern/server/service"
)

// PeerConfig groups the outbound peer-replication pump knobs (#185, #190).
//
//   - LANTERN_PEERS                      CSV list of peer addresses
//     ("host:port,host:port"). Empty (the default) yields a no-op pump
//     unless LANTERN_PEER_DISCOVERY=dns; otherwise the server runs in
//     single-instance mode. Whitespace around each entry is trimmed;
//     empty entries are dropped.
//   - LANTERN_PUMP_BACKOFF_MIN_MS        initial reconnect delay after a
//     pump session error. Default 250ms. Doubles on each successive
//     failure, capped at LANTERN_PUMP_BACKOFF_MAX_MS.
//   - LANTERN_PUMP_BACKOFF_MAX_MS        upper cap on the reconnect
//     delay. Default 30000ms (30s).
//   - LANTERN_PEER_DISCOVERY             peer discovery mode. "static"
//     (default) uses LANTERN_PEERS as-is. "dns" periodically resolves
//     LANTERN_PEER_DNS_NAME to its A/AAAA records and treats every
//     non-self address as a peer — the standard k8s headless-Service /
//     Docker Compose service-name pattern (#190).
//   - LANTERN_PEER_DNS_NAME              hostname for DNS discovery.
//     Required when LANTERN_PEER_DISCOVERY=dns.
//   - LANTERN_PEER_DEFAULT_PORT          port appended to every DNS-
//     resolved IP. Default "50051".
//   - LANTERN_PEER_DISCOVERY_INTERVAL_MS DNS re-poll cadence. Default
//     10000ms (10s). A transient resolution failure logs and preserves
//     the previously-active peer set so established subscriptions are
//     not torn down.
type PeerConfig struct {
	Peers             []string
	BackoffMin        time.Duration
	BackoffMax        time.Duration
	Discovery         string
	DNSName           string
	DefaultPort       string
	DiscoveryInterval time.Duration
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
		Peers:             peers,
		BackoffMin:        time.Duration(envconfig.Int("LANTERN_PUMP_BACKOFF_MIN_MS", 250)) * time.Millisecond,
		BackoffMax:        time.Duration(envconfig.Int("LANTERN_PUMP_BACKOFF_MAX_MS", 30_000)) * time.Millisecond,
		Discovery:         strings.ToLower(strings.TrimSpace(envconfig.String("LANTERN_PEER_DISCOVERY", "static"))),
		DNSName:           strings.TrimSpace(envconfig.String("LANTERN_PEER_DNS_NAME", "")),
		DefaultPort:       strings.TrimSpace(envconfig.String("LANTERN_PEER_DEFAULT_PORT", "50051")),
		DiscoveryInterval: time.Duration(envconfig.Int("LANTERN_PEER_DISCOVERY_INTERVAL_MS", 10_000)) * time.Millisecond,
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
	cache *graphcache.GraphCache[string, *v1.Vertex],
	m replication.Metrics,
	logger *slog.Logger,
) *replication.Pump {
	cfg := replication.Config{
		NodeID:            rc.NodeID,
		Peers:             pc.Peers,
		BackoffMin:        pc.BackoffMin,
		BackoffMax:        pc.BackoffMax,
		Logger:            logger,
		Metrics:           m,
		DiscoveryInterval: pc.DiscoveryInterval,
	}
	if pc.Discovery == "dns" && pc.DNSName != "" {
		selfIPs, err := replication.LocalIPSet()
		if err != nil {
			logger.Warn("replication pump: failed to enumerate local IPs for DNS self-filter",
				slog.Any("err", err))
			selfIPs = nil
		}
		cfg.Source = &replication.DNSSource{
			Name:    pc.DNSName,
			Port:    pc.DefaultPort,
			SelfIPs: selfIPs,
		}
		logger.Info("replication pump: DNS peer discovery enabled",
			slog.String("dns_name", pc.DNSName),
			slog.String("default_port", pc.DefaultPort),
			slog.Duration("interval", pc.DiscoveryInterval))
	}
	return replication.NewPump(cfg, svc, cache)
}
