package provider

import (
	"encoding/hex"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
)

// MutationLogConfig sizes the in-memory mutation log ring buffer used by
// the replication subsystem.
//
//   - LANTERN_MUTATION_LOG_CAPACITY           (default 100000)
//     Under the leaderless Subscribe contract (#415, Reading B) each
//     replica appends entries from EVERY cluster origin (local writes
//     plus remote applies that reached us through the peer pump),
//     so effective per-origin retention at constant capacity scales
//     ~1/N with cluster size. Operators running clusters with N > 1
//     replicas at sustained write rates should size capacity as
//     roughly `peak_cluster_rps × retention_seconds × safety_margin`
//     and watch `lantern_mutation_log_fill_ratio` for headroom.
//   - LANTERN_MUTATION_LOG_SUBSCRIBER_BUFFER  per-subscriber outbound
//     channel depth. Each Subscribe stream owns a buffered channel of this
//     size; the fan-out path uses a non-blocking send and tears the
//     subscriber down on a full channel. Too-small values turn transient
//     scheduler stalls under high write rates into permanent gap-closes
//     (see issue #258). Default 512.
type MutationLogConfig struct {
	Capacity         int
	SubscriberBuffer int
}

// ReplicationConfig groups identity knobs used by HLC stamping and the
// outbound origin tag on every appended mutation.
//
//   - LANTERN_NODE_ID                    32-char hex (16 bytes); when unset
//     the graph database generates and persists an identity on first open.
//     Receipt-enabled nodes require an explicit matching identity.
//   - LANTERN_TOMBSTONE_TTL              max retention window for delete
//     tombstones and the upper bound on caller-supplied Expiration on
//     Add*/Put* RPCs. Default 1 year (8760h). Set to a value larger than
//     the longest plausible cross-cluster delivery delay to avoid late
//     writes resurrecting deleted entries.
type ReplicationConfig struct {
	NodeID         hlc.NodeID
	TombstoneTTL   time.Duration
	nodeIDExplicit bool
}

// NewMutationLogConfig returns the MutationLogConfig slice of Config.
func NewMutationLogConfig(c *Config) MutationLogConfig { return c.MutationLog }

// NewReplicationConfig returns the ReplicationConfig slice of Config.
func NewReplicationConfig(c *Config) ReplicationConfig { return c.Replication }

// loadMutationLogConfig reads MutationLogConfig from the environment. Called
// from NewConfig so all env reads remain colocated.
func loadMutationLogConfig(reader *envconfig.Reader) MutationLogConfig {
	return MutationLogConfig{
		Capacity:         reader.Int("LANTERN_MUTATION_LOG_CAPACITY", 100_000),
		SubscriberBuffer: reader.Int("LANTERN_MUTATION_LOG_SUBSCRIBER_BUFFER", 512),
	}
}

// loadReplicationConfig reads ReplicationConfig from the environment. Called
// from NewConfig so all env reads remain colocated.
func loadReplicationConfig(reader *envconfig.Reader) ReplicationConfig {
	ttl := reader.Duration("LANTERN_TOMBSTONE_TTL", 365*24*time.Hour) // 1 year
	var id hlc.NodeID
	raw := strings.TrimSpace(reader.String("LANTERN_NODE_ID", ""))
	cfg := ReplicationConfig{TombstoneTTL: ttl, nodeIDExplicit: raw != ""}
	if raw != "" {
		b, err := hex.DecodeString(strings.TrimPrefix(raw, "0x"))
		if err == nil && len(b) == len(id) {
			copy(cfg.NodeID[:], b)
		}
		if cfg.NodeID == (hlc.NodeID{}) {
			reader.Malformed("LANTERN_NODE_ID", raw, "must be 32 hex characters and nonzero")
		}
	}
	return cfg
}
