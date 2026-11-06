package provider

import (
	"log/slog"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	v1 "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/replication"
)

// PeerConfig carries replication reconnect/polling intervals and retained legacy
// topology inputs. BackoffMin/BackoffMax and DiscoveryInterval feed the current
// managed scheduling owner. Current peer identities and eligible HTTPS origins
// come from the separately certified signed workload membership.
//
// LANTERN_PEERS, legacy static/DNS discovery selectors and public-token peer
// CA/client-certificate settings do not enable HA. loadPeerPlaneConfig rejects
// their explicit presence, including empty values; they remain represented here
// only for that startup refusal and the documented registry contract.
type PeerConfig struct {
	Peers             []string
	CAFile            string
	ClientCertFile    string
	ClientKeyFile     string
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
func loadPeerConfig(reader *envconfig.Reader) PeerConfig {
	raw := strings.TrimSpace(reader.String("LANTERN_PEERS", ""))
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
		CAFile:            reader.String("LANTERN_PEER_CA_FILE", ""),
		ClientCertFile:    reader.String("LANTERN_PEER_CLIENT_CERT_FILE", ""),
		ClientKeyFile:     reader.String("LANTERN_PEER_CLIENT_KEY_FILE", ""),
		BackoffMin:        time.Duration(reader.Int("LANTERN_PUMP_BACKOFF_MIN_MS", 250)) * time.Millisecond,
		BackoffMax:        time.Duration(reader.Int("LANTERN_PUMP_BACKOFF_MAX_MS", 30_000)) * time.Millisecond,
		Discovery:         strings.ToLower(strings.TrimSpace(reader.String("LANTERN_PEER_DISCOVERY", "static"))),
		DNSName:           strings.TrimSpace(reader.String("LANTERN_PEER_DNS_NAME", "")),
		DefaultPort:       strings.TrimSpace(reader.String("LANTERN_PEER_DEFAULT_PORT", "50051")),
		DiscoveryInterval: time.Duration(reader.Int("LANTERN_PEER_DISCOVERY_INTERVAL_MS", 10_000)) * time.Millisecond,
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
	resolver *PeerResolver,
	rc ReplicationConfig,
	peerTransport *replication.PeerTransport,
	svc *dataruntime.Executor,
	cache *graphcache.GraphCache[string, *v1.Vertex],
	m replication.Metrics,
	logger *slog.Logger,
	installer *SnapshotInstallerSelection,
	_ runtimeCertified,
) *replication.Pump {
	cfg := newReplicationPumpConfig(pc, resolver, rc, peerTransport, svc, m, logger, installer)
	return replication.NewPump(cfg, svc, cache)
}

// searchPumpCertified binds a constructed Pump to the sealed Search engine.
// It conveys composition identity, never a native replication-policy grant.
type searchPumpCertified struct {
	manager     *ManagedOperationalRuntime
	primary     *dataruntime.Executor
	cache       *graphcache.GraphCache[string, *v1.Vertex]
	certified   runtimeCertified
	search      managedSearchCertified
	pump        *replication.Pump
	fingerprint string
}

func validSearchPeerInstallation(certified runtimeCertified, manager *ManagedOperationalRuntime, primary *dataruntime.Executor, cache *graphcache.GraphCache[string, *v1.Vertex], search managedSearchCertified) bool {
	return certified.valid && certified.runtime != nil && certified.primary == primary && certified.replication != nil &&
		manager != nil && manager.runtime != nil && manager.runtime.data == certified.runtime &&
		cache != nil && cache == certified.runtime.GraphCache() && search.validFor(manager, primary)
}

func (c searchPumpCertified) valid() bool {
	return validSearchPeerInstallation(c.certified, c.manager, c.primary, c.cache, c.search) &&
		c.pump != nil && c.fingerprint != "" && c.primary.SearchConfigFingerprint() == c.fingerprint &&
		c.pump.SearchConfigFingerprint() == c.fingerprint
}

func (c searchPumpCertified) validFor(certified runtimeCertified, manager *ManagedOperationalRuntime, primary *dataruntime.Executor, cache *graphcache.GraphCache[string, *v1.Vertex], search managedSearchCertified, pump *replication.Pump) bool {
	return c.valid() && validSearchPeerInstallation(certified, manager, primary, cache, search) &&
		c.manager == manager && c.primary == primary && c.cache == cache && c.pump == pump &&
		c.certified.runtime == certified.runtime && c.certified.replication == certified.replication &&
		c.search.runtime == search.runtime && c.search.installation == search.installation
}

// NewSearchCertifiedReplicationPump constructs peer workers only after the
// actual primary's managed Search installation has been sealed. The ordinary
// constructor remains available for isolated legacy compositions.
func NewSearchCertifiedReplicationPump(
	pc PeerConfig,
	resolver *PeerResolver,
	rc ReplicationConfig,
	peerTransport *replication.PeerTransport,
	svc *dataruntime.Executor,
	cache *graphcache.GraphCache[string, *v1.Vertex],
	m replication.Metrics,
	logger *slog.Logger,
	installer *SnapshotInstallerSelection,
	certified runtimeCertified,
	manager *ManagedOperationalRuntime,
	search managedSearchCertified,
) (searchPumpCertified, error) {
	if !validSearchPeerInstallation(certified, manager, svc, cache, search) {
		return searchPumpCertified{}, errManagedOperational
	}
	pump := NewReplicationPump(pc, resolver, rc, peerTransport, svc, cache, m, logger, installer, certified)
	result := searchPumpCertified{
		manager: manager, primary: svc, cache: cache, certified: certified, search: search,
		pump: pump, fingerprint: svc.SearchConfigFingerprint(),
	}
	if !result.valid() {
		return searchPumpCertified{}, errManagedOperational
	}
	return result, nil
}

// NewSearchReplicationPump selects the exact worker attested by the witness.
// No worker is started, reconfigured or adopted by this selector.
func NewSearchReplicationPump(certified searchPumpCertified) (*replication.Pump, error) {
	if !certified.valid() {
		return nil, errManagedOperational
	}
	return certified.pump, nil
}

func newReplicationPumpConfig(
	pc PeerConfig,
	resolver *PeerResolver,
	rc ReplicationConfig,
	peerTransport *replication.PeerTransport,
	svc *dataruntime.Executor,
	m replication.Metrics,
	logger *slog.Logger,
	installer *SnapshotInstallerSelection,
) replication.Config {
	cfg := replication.Config{
		NodeID:                  rc.NodeID,
		Peers:                   pc.Peers,
		BackoffMin:              pc.BackoffMin,
		BackoffMax:              pc.BackoffMax,
		Logger:                  logger,
		Metrics:                 m,
		DiscoveryInterval:       pc.DiscoveryInterval,
		PeerTransport:           peerTransport,
		SearchConfigFingerprint: svc.SearchConfigFingerprint(),
		NamespaceFormat:         svc.DataNamespaceFormat(),
		SnapshotInstaller:       installer.selected(),
	}
	if resolver != nil {
		cfg.Source = resolver.Source
	}
	return cfg
}
