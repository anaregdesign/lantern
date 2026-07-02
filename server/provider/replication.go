package provider

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
	domainmetrics "github.com/anaregdesign/lantern/server/metrics"
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
//     a cryptographically random NodeID is generated at process start so
//     two unrelated nodes never collide. Operators that want a stable
//     identity across restarts must set this explicitly.
//   - LANTERN_TOMBSTONE_TTL              max retention window for delete
//     tombstones and the upper bound on caller-supplied Expiration on
//     Add*/Put* RPCs. Default 1 year (8760h). Set to a value larger than
//     the longest plausible cross-cluster delivery delay to avoid late
//     writes resurrecting deleted entries.
type ReplicationConfig struct {
	NodeID       hlc.NodeID
	TombstoneTTL time.Duration
}

// NewMutationLogConfig returns the MutationLogConfig slice of Config.
func NewMutationLogConfig(c *Config) MutationLogConfig { return c.MutationLog }

// NewReplicationConfig returns the ReplicationConfig slice of Config.
func NewReplicationConfig(c *Config) ReplicationConfig { return c.Replication }

// loadMutationLogConfig reads MutationLogConfig from the environment. Called
// from NewConfig so all env reads remain colocated.
func loadMutationLogConfig() MutationLogConfig {
	return MutationLogConfig{
		Capacity:         envconfig.Int("LANTERN_MUTATION_LOG_CAPACITY", 100_000),
		SubscriberBuffer: envconfig.Int("LANTERN_MUTATION_LOG_SUBSCRIBER_BUFFER", 512),
	}
}

// loadReplicationConfig reads ReplicationConfig from the environment. Called
// from NewConfig so all env reads remain colocated.
func loadReplicationConfig() ReplicationConfig {
	ttl := envconfig.Duration("LANTERN_TOMBSTONE_TTL", 365*24*time.Hour) // 1 year
	var id hlc.NodeID
	raw := strings.TrimSpace(envconfig.String("LANTERN_NODE_ID", ""))
	raw = strings.TrimPrefix(raw, "0x")
	if raw != "" {
		if b, err := hex.DecodeString(raw); err == nil && len(b) == len(id) {
			copy(id[:], b)
			return ReplicationConfig{NodeID: id, TombstoneTTL: ttl}
		}
		// Fall through to random on malformed input. Logged here so the
		// operator sees the fallback without crashing startup, and recorded
		// in the envconfig findings so LANTERN_STRICT_CONFIG treats it like
		// every other malformed value (#847).
		envconfig.Malformed("LANTERN_NODE_ID", raw, "must be 32 hex chars (16 bytes)")
		slog.Warn("ignoring malformed LANTERN_NODE_ID; using random NodeID",
			slog.String("value", raw))
	}
	if _, err := rand.Read(id[:]); err != nil {
		// rand.Read on linux/darwin cannot fail in practice; degrade
		// to all-zero rather than panicking the server.
		slog.Error("failed to read crypto/rand for NodeID; using zero NodeID",
			slog.Any("err", err))
	}
	return ReplicationConfig{NodeID: id, TombstoneTTL: ttl}
}

// NewHLCClock constructs the process-wide hybrid logical clock. The clock's
// origin NodeID is the same one stamped onto outgoing replication frames.
//
// OnSkewExceeded is intentionally left nil here — the skew metric is owned
// by the replication subsystem and will be wired in when remote Update calls
// land (see issues #180 and #182). For now this is a one-way local clock.
func NewHLCClock(rc ReplicationConfig) *hlc.Clock {
	return hlc.New(rc.NodeID, hlc.Options{})
}

// NewMutationLog constructs the bounded in-memory mutation log. The capacity
// gauge is initialised here (rather than from inside mutationlog itself) so
// the core package keeps zero dependencies on prometheus.
func NewMutationLog(mlc MutationLogConfig, m *domainmetrics.DomainMetrics) *mutationlog.Log {
	log := mutationlog.New(mutationlog.Options{
		Capacity:         mlc.Capacity,
		SubscriberBuffer: mlc.SubscriberBuffer,
		// Forward dispatcher fan-out drops to the Prometheus counter so
		// operators see slow-subscriber pressure (#260). The callback
		// pattern keeps core/mutationlog free of prometheus deps.
		OnDrop: m.OnMutationLogSubscriberDropped,
	})
	m.SetMutationLogCapacity(mlc.Capacity)
	return log
}
