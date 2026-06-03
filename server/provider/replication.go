package provider

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"os"
	"strings"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
	domainmetrics "github.com/anaregdesign/lantern/server/metrics"
)

// MutationLogConfig sizes the in-memory mutation log ring buffer used by
// the replication subsystem.
//
//   - LANTERN_MUTATION_LOG_CAPACITY      (default 100000)
type MutationLogConfig struct {
	Capacity int
}

// ReplicationConfig groups identity knobs used by HLC stamping and the
// outbound origin tag on every appended mutation.
//
//   - LANTERN_NODE_ID                    32-char hex (16 bytes); when unset
//     a cryptographically random NodeID is generated at process start so
//     two unrelated nodes never collide. Operators that want a stable
//     identity across restarts must set this explicitly.
type ReplicationConfig struct {
	NodeID hlc.NodeID
}

// NewMutationLogConfig returns the MutationLogConfig slice of Config.
func NewMutationLogConfig(c *Config) MutationLogConfig { return c.MutationLog }

// NewReplicationConfig returns the ReplicationConfig slice of Config.
func NewReplicationConfig(c *Config) ReplicationConfig { return c.Replication }

// loadMutationLogConfig reads MutationLogConfig from the environment. Called
// from NewConfig so all env reads remain colocated.
func loadMutationLogConfig() MutationLogConfig {
	return MutationLogConfig{
		Capacity: envconfig.Int("LANTERN_MUTATION_LOG_CAPACITY", 100_000),
	}
}

// loadReplicationConfig reads ReplicationConfig from the environment. Called
// from NewConfig so all env reads remain colocated.
func loadReplicationConfig() ReplicationConfig {
	var id hlc.NodeID
	raw := strings.TrimSpace(os.Getenv("LANTERN_NODE_ID"))
	raw = strings.TrimPrefix(raw, "0x")
	if raw != "" {
		if b, err := hex.DecodeString(raw); err == nil && len(b) == len(id) {
			copy(id[:], b)
			return ReplicationConfig{NodeID: id}
		}
		// Fall through to random on malformed input. Logged here so the
		// operator sees the fallback without crashing startup.
		slog.Warn("ignoring malformed LANTERN_NODE_ID; using random NodeID",
			slog.String("value", raw))
	}
	if _, err := rand.Read(id[:]); err != nil {
		// rand.Read on linux/darwin cannot fail in practice; degrade
		// to all-zero rather than panicking the server.
		slog.Error("failed to read crypto/rand for NodeID; using zero NodeID",
			slog.Any("err", err))
	}
	return ReplicationConfig{NodeID: id}
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
	log := mutationlog.New(mutationlog.Options{Capacity: mlc.Capacity})
	m.SetMutationLogCapacity(mlc.Capacity)
	return log
}
