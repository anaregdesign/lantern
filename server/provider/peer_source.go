package provider

import (
	"log/slog"

	"github.com/anaregdesign/lantern/server/replication"
)

// PeerResolver owns the optional dynamic PeerSource shared by the replication
// Pump and AntiEntropy driver. Static deployments leave Source nil and both
// consumers continue to use PeerConfig.Peers directly.
type PeerResolver struct {
	Source replication.PeerSource
}

// NewPeerResolver constructs the dynamic resolver once so every replication
// subsystem observes the same discovery semantics and self-filter set.
func NewPeerResolver(pc PeerConfig, logger *slog.Logger) *PeerResolver {
	resolver := &PeerResolver{}
	if pc.Discovery != "dns" || pc.DNSName == "" {
		return resolver
	}

	selfIPs, err := replication.LocalIPSet()
	if err != nil {
		logger.Warn("replication: failed to enumerate local IPs for DNS self-filter",
			slog.Any("err", err))
		selfIPs = nil
	}
	resolver.Source = &replication.DNSSource{
		Name:    pc.DNSName,
		Port:    pc.DefaultPort,
		SelfIPs: selfIPs,
	}
	logger.Info("replication: DNS peer discovery enabled",
		slog.String("dns_name", pc.DNSName),
		slog.String("default_port", pc.DefaultPort),
		slog.Duration("interval", pc.DiscoveryInterval))
	return resolver
}
