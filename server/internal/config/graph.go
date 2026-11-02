package config

import (
	"fmt"
	"time"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
)

// CacheConfig sizes the GraphCache TTL and its GC tick, plus the optional
// aggregate capacity caps (#848).
//
//   - LANTERN_DEFAULT_TTL_SECONDS        (default 60)
//   - LANTERN_GC_INTERVAL_SECONDS        (default 60) — GraphCache.Watch tick
//   - LANTERN_GC_EDGE_BUDGET             (default 0) — tail buckets scanned per tick; 0 = full sweep
//   - LANTERN_MAX_VERTICES               (default 0 = unlimited)
//   - LANTERN_MAX_EDGES                  (default 0 = unlimited)
//   - LANTERN_MAX_VERTEX_CAUSAL_ENTRIES  (default 0 = unlimited)
//   - LANTERN_MAX_EDGE_CAUSAL_ENTRIES    (default 0 = unlimited)
//
// The legacy caps are SOFT, enforced at the local write-RPC boundary over the
// conservative live-plus-Put-barrier footprint. A live additive edge that also
// has a barrier can count twice; charging born-expired Put barriers is
// intentional so they cannot bypass the legacy caps. The causal caps are
// separate atomic budgets over the exact causal-identity union (one slot for a
// live HLC floor, Put barrier, or Delete tombstone). Replication apply and
// backup restore bypass both cap families — rejecting writes peers already
// committed would break convergence — so in HA a node may report causal
// over-limit after convergence. TTL decay frees legacy capacity only where no
// Put barrier remains; causal capacity is freed only when the identity has no
// retained floor. There is deliberately no eviction policy. Pair with
// GOMEMLIMIT as the second line of defense.
type CacheConfig struct {
	TTL                    time.Duration
	GCInterval             time.Duration
	GCEdgeBudget           int
	MaxVertices            int
	MaxEdges               int
	MaxVertexCausalEntries int
	MaxEdgeCausalEntries   int
}

// TraversalConfig bounds the wall-clock budget of the traversal-heavy
// Illuminate RPC (#842). A deadline-less client running an expensive PPR or
// deep BFS holds GraphCache.mu.RLock for the whole walk, stalling every
// writer and the GC tick; this knob lets the SERVER cap that hold time
// regardless of client behaviour. The non-zero default protects a server even
// when clients omit deadlines; 0 is an explicit opt-out. The budget only ever
// tightens: a client deadline shorter than the budget still wins.
//
//   - LANTERN_TRAVERSAL_TIMEOUT_MS                (default 5000)
//   - LANTERN_TRAVERSAL_MAX_PUSHES                 (default 1000000)
//   - LANTERN_TRAVERSAL_MAX_TOUCHED_EDGES          (default 10000000)
//   - LANTERN_TRAVERSAL_MAX_RESULTS                (default 1024)
type TraversalConfig struct {
	Timeout         time.Duration
	MaxPushes       int
	MaxTouchedEdges int
	MaxResults      int
}

// ScanConfig caps the per-call pagination knobs for the prefix RPCs.
// Defaults aim at "safe to leave unconfigured": small enough that a buggy
// client cannot trivially exhaust the server, large enough to make a
// reasonable single page useful. Operators can lift the ceilings via env
// when the workload warrants it.
//
//   - LANTERN_SCAN_DEFAULT_LIMIT                    (default 1000)
//   - LANTERN_SCAN_MAX_LIMIT                        (default 10000)
//   - LANTERN_DELETE_BY_PREFIX_DEFAULT_LIMIT        (default 10000)
//   - LANTERN_DELETE_BY_PREFIX_MAX_LIMIT            (default 100000)
type ScanConfig struct {
	ScanDefaultLimit           uint32
	ScanMaxLimit               uint32
	DeleteByPrefixDefaultLimit uint32
	DeleteByPrefixMaxLimit     uint32
}

// ValidationLimits caps user-controllable request dimensions so a single
// caller can't exhaust server resources before the rate limiter or message
// size cap kicks in.
type ValidationLimits struct {
	MaxKeyLen         int
	MaxBatchSize      int
	IlluminateMaxStep int
	IlluminateMaxK    int
}

func loadCache(reader *envconfig.Reader) CacheConfig {
	return CacheConfig{
		TTL:                    time.Duration(reader.Int("LANTERN_DEFAULT_TTL_SECONDS", 60)) * time.Second,
		GCInterval:             time.Duration(reader.Int("LANTERN_GC_INTERVAL_SECONDS", 60)) * time.Second,
		GCEdgeBudget:           reader.Int("LANTERN_GC_EDGE_BUDGET", 0),
		MaxVertices:            reader.Int("LANTERN_MAX_VERTICES", 0),
		MaxEdges:               reader.Int("LANTERN_MAX_EDGES", 0),
		MaxVertexCausalEntries: reader.Int("LANTERN_MAX_VERTEX_CAUSAL_ENTRIES", 0),
		MaxEdgeCausalEntries:   reader.Int("LANTERN_MAX_EDGE_CAUSAL_ENTRIES", 0),
	}
}

func loadValidation(reader *envconfig.Reader) ValidationLimits {
	return ValidationLimits{
		MaxKeyLen:         reader.Int("LANTERN_MAX_KEY_LEN", 1024),
		MaxBatchSize:      reader.Int("LANTERN_MAX_BATCH_SIZE", 10000),
		IlluminateMaxStep: reader.Int("LANTERN_ILLUMINATE_MAX_STEP", 16),
		IlluminateMaxK:    reader.Int("LANTERN_ILLUMINATE_MAX_K", 1024),
	}
}

func loadScan(reader *envconfig.Reader) ScanConfig {
	return ScanConfig{
		ScanDefaultLimit:           reader.Uint32("LANTERN_SCAN_DEFAULT_LIMIT", 1000),
		ScanMaxLimit:               reader.Uint32("LANTERN_SCAN_MAX_LIMIT", 10000),
		DeleteByPrefixDefaultLimit: reader.Uint32("LANTERN_DELETE_BY_PREFIX_DEFAULT_LIMIT", 10000),
		DeleteByPrefixMaxLimit:     reader.Uint32("LANTERN_DELETE_BY_PREFIX_MAX_LIMIT", 100000),
	}
}

func loadTraversal(reader *envconfig.Reader) TraversalConfig {
	const (
		defaultTimeoutMS      = 5_000
		defaultMaxPushes      = 1_000_000
		defaultMaxTouchedEdge = 10_000_000
		defaultMaxResults     = 1_024
	)
	timeoutMS := reader.Int("LANTERN_TRAVERSAL_TIMEOUT_MS", defaultTimeoutMS)
	if timeoutMS < 0 {
		reader.Malformed("LANTERN_TRAVERSAL_TIMEOUT_MS", fmt.Sprint(timeoutMS), "must be zero or a positive millisecond duration")
		timeoutMS = defaultTimeoutMS
	}
	positive := func(key string, def int) int {
		v := reader.Int(key, def)
		if v <= 0 {
			reader.Malformed(key, fmt.Sprint(v), "must be positive")
			return def
		}
		return v
	}
	return TraversalConfig{
		Timeout:         time.Duration(timeoutMS) * time.Millisecond,
		MaxPushes:       positive("LANTERN_TRAVERSAL_MAX_PUSHES", defaultMaxPushes),
		MaxTouchedEdges: positive("LANTERN_TRAVERSAL_MAX_TOUCHED_EDGES", defaultMaxTouchedEdge),
		MaxResults:      positive("LANTERN_TRAVERSAL_MAX_RESULTS", defaultMaxResults),
	}
}
