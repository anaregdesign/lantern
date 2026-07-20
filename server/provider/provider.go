package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/search"
	v1 "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
	domainmetrics "github.com/anaregdesign/lantern/server/metrics"
	"github.com/anaregdesign/lantern/server/readiness"
	"github.com/anaregdesign/lantern/server/service"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NetConfig groups the primary listener and message-size / concurrency caps.
//
//   - LANTERN_PORT                       (default 6380)
//   - LANTERN_MAX_RECV_MSG_BYTES         (default 16 MiB)
//   - LANTERN_MAX_SEND_MSG_BYTES         (default 16 MiB)
//   - LANTERN_MAX_CONCURRENT_STREAMS     (default 1024; 0 = unlimited)
type NetConfig struct {
	Port                 int
	MaxRecvMsgBytes      int
	MaxSendMsgBytes      int
	MaxConcurrentStreams uint32
}

// TLSConfig groups the all-or-nothing TLS / mTLS material.
//
//   - LANTERN_TLS_CERT_FILE              PEM cert path (enables TLS)
//   - LANTERN_TLS_KEY_FILE               PEM key  path
//   - LANTERN_TLS_CLIENT_CA_FILE         PEM client CA path (enables mTLS)
type TLSConfig struct {
	CertFile     string
	KeyFile      string
	ClientCAFile string
}

// RateLimitConfig is the process-wide token bucket policy.
//
//   - LANTERN_RATE_LIMIT_RPS             0 disables rate limiting (default 0)
//   - LANTERN_RATE_LIMIT_BURST           token bucket burst (default 2x RPS)
type RateLimitConfig struct {
	RPS   float64
	Burst int
}

// ObservabilityConfig groups logging, metrics endpoint, gRPC reflection
// (the on-the-wire surface served by connectrpc.com/grpcreflect), and
// build-info knobs.
//
//   - LANTERN_LOG_LEVEL                  debug|info|warn|error (default info)
//   - LANTERN_LOG_FORMAT                 json|text             (default json)
//   - LANTERN_METRICS_ADDR               host:port for /metrics + /healthz
//     (default :9090; set empty to disable)
//   - LANTERN_REFLECTION                 true|false            (default true)
//   - LANTERN_VERSION                    overrides lantern_build_info{version}
//   - LANTERN_COMMIT                     overrides lantern_build_info{commit}
//   - LANTERN_SLOW_RPC_THRESHOLD_MS      milliseconds; RPCs that take longer
//     emit a warn-level "slow rpc" log. Default 500. Set to 0 to disable.
//   - LANTERN_PPROF_ENABLED              true|false            (default false)
//     when true, mounts /debug/pprof/* on the metrics listener for heap,
//     goroutine, mutex, block, allocs, threadcreate, profile (CPU), trace,
//     cmdline, and symbol. The metrics listener is intended for internal
//     scrape traffic only — leave PPROF disabled unless that boundary holds.
//   - LANTERN_MUTEX_PROFILE_FRACTION     int   (default 0 = disabled)
//     passed to runtime.SetMutexProfileFraction. Sampling rate for mutex
//     contention events; 1-in-N stacks are recorded. Has runtime cost on
//     contended workloads — leave 0 in production unless actively profiling.
//   - LANTERN_BLOCK_PROFILE_RATE         int   (default 0 = disabled)
//     passed to runtime.SetBlockProfileRate. Nanoseconds between block-event
//     samples; lower = more samples. Same runtime-cost caveat as above.
type ObservabilityConfig struct {
	LogLevel             slog.Level
	LogFormat            string
	MetricsAddr          string
	EnableReflection     bool
	Version              string
	Commit               string
	SlowRPCThreshold     time.Duration
	EnablePprof          bool
	MutexProfileFraction int
	BlockProfileRate     int
}

// CacheConfig sizes the GraphCache TTL and its GC tick, plus the optional
// aggregate capacity caps (#848).
//
//   - LANTERN_DEFAULT_TTL_SECONDS        (default 60)
//   - LANTERN_GC_INTERVAL_SECONDS        (default 60) — GraphCache.Watch tick
//   - LANTERN_MAX_VERTICES               (default 0 = unlimited)
//   - LANTERN_MAX_EDGES                  (default 0 = unlimited)
//
// The caps are SOFT, enforced at the local write-RPC boundary only: when a
// batch would push the live count past the cap the RPC fails fast with
// RESOURCE_EXHAUSTED instead of growing until the kernel OOM-kills the
// process (which loses ALL data, not just the misbehaving writer's).
// Replication apply and backup restore bypass the caps — rejecting writes
// peers already committed would break convergence — so the caps bound
// locally-originated growth; in HA every node applies its own cap at its
// own RPC boundary. Deletes and TTL decay free capacity naturally; there
// is deliberately no eviction policy. Pair with GOMEMLIMIT as the second
// line of defense.
type CacheConfig struct {
	TTL         time.Duration
	GCInterval  time.Duration
	MaxVertices int
	MaxEdges    int
}

// ShutdownConfig is the graceful-shutdown timing.
//
//   - LANTERN_SHUTDOWN_TIMEOUT_SECONDS   (default 30) — how long
//     http.Server.Shutdown may take to drain in-flight requests before a
//     hard Close.
//   - LANTERN_DRAIN_DELAY_SECONDS         (default 0 = disabled) — the
//     zero-drop rolling-update drain window (#768). On SIGTERM the server
//     flips readiness to NOT_SERVING (overall "" gRPC health + /readyz)
//     immediately, then keeps the listeners serving for this long so load
//     balancers and kube-proxy observe the drain and stop routing new
//     requests before the listener actually stops accepting. Opt-in
//     because it lengthens shutdown; set it slightly above the platform's
//     endpoint-propagation lag (typically 1-5s).
type ShutdownConfig struct {
	Timeout    time.Duration
	DrainDelay time.Duration
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

// SearchConfig governs the full-text SearchVertices RPC (#624). Enabled is
// opt-out: the index is built by default because content search is the
// headline reason most callers reach for Lantern's memory surface, and the
// per-put cost is only paid while the feature is on. Operators who want the
// leaner put hot path set LANTERN_SEARCH_ENABLED=false, which both skips
// EnableSearchIndex at cache construction AND makes the RPC return
// FAILED_PRECONDITION. DefaultLimit / MaxLimit cap the per-call ranked-hit
// count the same way ScanConfig caps the prefix RPCs.
//
//   - LANTERN_SEARCH_ENABLED           (default true)
//   - LANTERN_SEARCH_POSITIONS         (default true)
//   - LANTERN_SEARCH_DEFAULT_LIMIT     (default 100)
//   - LANTERN_SEARCH_MAX_LIMIT         (default 1000)
//   - LANTERN_SEARCH_DEFAULT_MODE      (default "any": any|all|min-should)
//   - LANTERN_SEARCH_DEFAULT_MIN_SHOULD (default 1)
//   - LANTERN_SEARCH_TIMEOUT_MS         (default 5000)
//   - LANTERN_SEARCH_MAX_QUERY_BYTES    (default 16384)
//   - LANTERN_SEARCH_MAX_QUERY_TERMS    (default 1024)
//   - LANTERN_SEARCH_MAX_DICTIONARY_VISITS (default 1000000)
//   - LANTERN_SEARCH_MAX_POSTING_VISITS (default 10000000)
//   - LANTERN_SEARCH_MAX_POSITION_VISITS (default 10000000)
//   - LANTERN_SEARCH_MAX_EXPIRATION_VISITS (default 100000)
//   - LANTERN_SEARCH_MAX_IN_FLIGHT      (default 32)
//   - LANTERN_SEARCH_CURSOR_TTL_SECONDS (default 60)
//   - LANTERN_SEARCH_MAX_SESSIONS       (default 128)
//   - LANTERN_SEARCH_MAX_SESSION_HITS   (default 10000)
//   - LANTERN_SEARCH_MAX_SESSION_BYTES  (default 67108864)
//   - LANTERN_SEARCH_MAX_DOCUMENT_BYTES (default 1048576)
//   - LANTERN_SEARCH_MAX_DOCUMENT_TOKENS (default 250000)
//   - LANTERN_SEARCH_MAX_DOCUMENT_TERMS (default 100000)
//   - LANTERN_SEARCH_MAX_LIVE_TERMS     (default 5000000)
//   - LANTERN_SEARCH_MAX_LIVE_POSTINGS  (default 50000000)
//   - LANTERN_SEARCH_MAX_POSITION_ENTRIES (default 50000000)
//   - LANTERN_SEARCH_COMPACTION_RATIO   (default 2)
//   - LANTERN_SEARCH_COMPACTION_MIN_RETIRED (default 10000)
type SearchConfig struct {
	Enabled      bool
	DefaultLimit uint32
	MaxLimit     uint32
	// Positions is opt-out (LANTERN_SEARCH_POSITIONS, default true): when true
	// the search index records positional postings so phrase queries verify
	// adjacency and the proximity boost ranks tight matches higher. Setting it
	// false drops the per-(word term, vertex) position store. Phrase requests
	// then fail closed with SEARCH_POSITIONS_DISABLED because adjacency cannot
	// be verified; the proximity boost is inert. This trades the phrase feature
	// for a smaller index on large corpora (#908/#1054). Ignored when Enabled is
	// false.
	Positions bool
	// DefaultMode is the match mode applied when a SearchVertices request omits
	// options or leaves match_mode unspecified: "any" (OR), "all" (AND), or
	// "min-should". The value is validated at startup (service.ValidateMatchMode);
	// an unrecognised spelling fails boot rather than silently defaulting (#911).
	DefaultMode string
	// DefaultMinShould is the minimum-should-match count applied when the mode
	// resolves to min-should but the request leaves min_should_match at 0.
	DefaultMinShould    uint32
	Timeout             time.Duration
	MaxQueryBytes       int
	MaxQueryTerms       int
	MaxDictionaryVisits int
	MaxPostingVisits    int
	MaxPositionVisits   int
	MaxExpirationVisits int
	MaxInFlight         int
	CursorTTL           time.Duration
	MaxSessions         int
	MaxSessionHits      int
	MaxSessionBytes     int64
	AnalysisLimits      search.SearchAnalysisLimits
}

// Config aggregates every focused sub-config. It is constructed once at
// startup by NewConfig and then projected into sub-configs that each provider
// consumes — providers MUST NOT take *Config when they only need one slice of
// it (SRP). main / App may keep *Config because they observe several aspects
// for startup logging.
type Config struct {
	Net           NetConfig
	TLS           TLSConfig
	RateLimit     RateLimitConfig
	Observability ObservabilityConfig
	Cache         CacheConfig
	Shutdown      ShutdownConfig
	Validation    ValidationLimits
	Traversal     TraversalConfig
	Auth          AuthConfig
	LLM           LLMConfig
	Scan          ScanConfig
	Search        SearchConfig
	MutationLog   MutationLogConfig
	Replication   ReplicationConfig
	Peer          PeerConfig
	AntiEntropy   AntiEntropyConfig
	Readiness     ReadinessConfig
	CORS          CORSConfig
	Backup        backup.Config
}

// NewConfig loads every sub-config from the environment, then validates the
// load (#847): set-but-malformed values and unknown LANTERN_* names are
// logged as warnings, and — when LANTERN_STRICT_CONFIG=true — turned into a
// boot failure so a typo is caught in staging instead of silently running on
// defaults. The returned error is non-nil only in strict mode.
func NewConfig() (*Config, error) {
	rps := envconfig.Float("LANTERN_RATE_LIMIT_RPS", 0)
	burst := envconfig.Int("LANTERN_RATE_LIMIT_BURST", int(2*rps))
	if burst <= 0 && rps > 0 {
		burst = int(2 * rps)
	}
	peer := loadPeerConfig()
	cfg := &Config{
		Net: NetConfig{
			Port:                 envconfig.Int("LANTERN_PORT", 6380),
			MaxRecvMsgBytes:      envconfig.Int("LANTERN_MAX_RECV_MSG_BYTES", 16*1024*1024),
			MaxSendMsgBytes:      envconfig.Int("LANTERN_MAX_SEND_MSG_BYTES", 16*1024*1024),
			MaxConcurrentStreams: envconfig.Uint32("LANTERN_MAX_CONCURRENT_STREAMS", 1024),
		},
		TLS: TLSConfig{
			CertFile:     envconfig.String("LANTERN_TLS_CERT_FILE", ""),
			KeyFile:      envconfig.String("LANTERN_TLS_KEY_FILE", ""),
			ClientCAFile: envconfig.String("LANTERN_TLS_CLIENT_CA_FILE", ""),
		},
		RateLimit: RateLimitConfig{
			RPS:   rps,
			Burst: burst,
		},
		Observability: ObservabilityConfig{
			LogLevel:             envconfig.Level("LANTERN_LOG_LEVEL", slog.LevelInfo),
			LogFormat:            envconfig.String("LANTERN_LOG_FORMAT", "json"),
			MetricsAddr:          envconfig.String("LANTERN_METRICS_ADDR", ":9090"),
			EnableReflection:     envconfig.Bool("LANTERN_REFLECTION", true),
			Version:              envconfig.String("LANTERN_VERSION", ""),
			Commit:               envconfig.String("LANTERN_COMMIT", ""),
			SlowRPCThreshold:     time.Duration(envconfig.Int("LANTERN_SLOW_RPC_THRESHOLD_MS", 500)) * time.Millisecond,
			EnablePprof:          envconfig.Bool("LANTERN_PPROF_ENABLED", false),
			MutexProfileFraction: envconfig.Int("LANTERN_MUTEX_PROFILE_FRACTION", 0),
			BlockProfileRate:     envconfig.Int("LANTERN_BLOCK_PROFILE_RATE", 0),
		},
		Cache: CacheConfig{
			TTL:         time.Duration(envconfig.Int("LANTERN_DEFAULT_TTL_SECONDS", 60)) * time.Second,
			GCInterval:  time.Duration(envconfig.Int("LANTERN_GC_INTERVAL_SECONDS", 60)) * time.Second,
			MaxVertices: envconfig.Int("LANTERN_MAX_VERTICES", 0),
			MaxEdges:    envconfig.Int("LANTERN_MAX_EDGES", 0),
		},
		Shutdown: ShutdownConfig{
			Timeout:    time.Duration(envconfig.Int("LANTERN_SHUTDOWN_TIMEOUT_SECONDS", 30)) * time.Second,
			DrainDelay: time.Duration(envconfig.Int("LANTERN_DRAIN_DELAY_SECONDS", 0)) * time.Second,
		},
		Validation: ValidationLimits{
			MaxKeyLen:         envconfig.Int("LANTERN_MAX_KEY_LEN", 1024),
			MaxBatchSize:      envconfig.Int("LANTERN_MAX_BATCH_SIZE", 10000),
			IlluminateMaxStep: envconfig.Int("LANTERN_ILLUMINATE_MAX_STEP", 16),
			IlluminateMaxK:    envconfig.Int("LANTERN_ILLUMINATE_MAX_K", 1024),
		},
		Traversal: loadTraversalConfig(),
		Auth: AuthConfig{
			Tokens:           splitTokens(envconfig.String("LANTERN_AUTH_TOKENS", "")),
			ExemptReflection: envconfig.Bool("LANTERN_AUTH_EXEMPT_REFLECTION", true),
		},
		LLM: loadLLMConfig(),
		Scan: ScanConfig{
			ScanDefaultLimit:           envconfig.Uint32("LANTERN_SCAN_DEFAULT_LIMIT", 1000),
			ScanMaxLimit:               envconfig.Uint32("LANTERN_SCAN_MAX_LIMIT", 10000),
			DeleteByPrefixDefaultLimit: envconfig.Uint32("LANTERN_DELETE_BY_PREFIX_DEFAULT_LIMIT", 10000),
			DeleteByPrefixMaxLimit:     envconfig.Uint32("LANTERN_DELETE_BY_PREFIX_MAX_LIMIT", 100000),
		},
		Search: SearchConfig{
			Enabled:             envconfig.Bool("LANTERN_SEARCH_ENABLED", true),
			Positions:           envconfig.Bool("LANTERN_SEARCH_POSITIONS", true),
			DefaultLimit:        envconfig.Uint32("LANTERN_SEARCH_DEFAULT_LIMIT", 100),
			MaxLimit:            envconfig.Uint32("LANTERN_SEARCH_MAX_LIMIT", 1000),
			DefaultMode:         envconfig.String("LANTERN_SEARCH_DEFAULT_MODE", "any"),
			DefaultMinShould:    envconfig.Uint32("LANTERN_SEARCH_DEFAULT_MIN_SHOULD", 1),
			Timeout:             time.Duration(envconfig.Int("LANTERN_SEARCH_TIMEOUT_MS", 5000)) * time.Millisecond,
			MaxQueryBytes:       envconfig.Int("LANTERN_SEARCH_MAX_QUERY_BYTES", 16*1024),
			MaxQueryTerms:       envconfig.Int("LANTERN_SEARCH_MAX_QUERY_TERMS", 1024),
			MaxDictionaryVisits: envconfig.Int("LANTERN_SEARCH_MAX_DICTIONARY_VISITS", 1_000_000),
			MaxPostingVisits:    envconfig.Int("LANTERN_SEARCH_MAX_POSTING_VISITS", 10_000_000),
			MaxPositionVisits:   envconfig.Int("LANTERN_SEARCH_MAX_POSITION_VISITS", 10_000_000),
			MaxExpirationVisits: envconfig.Int("LANTERN_SEARCH_MAX_EXPIRATION_VISITS", 100_000),
			MaxInFlight:         envconfig.Int("LANTERN_SEARCH_MAX_IN_FLIGHT", 32),
			CursorTTL:           time.Duration(envconfig.Int("LANTERN_SEARCH_CURSOR_TTL_SECONDS", 60)) * time.Second,
			MaxSessions:         envconfig.Int("LANTERN_SEARCH_MAX_SESSIONS", 128),
			MaxSessionHits:      envconfig.Int("LANTERN_SEARCH_MAX_SESSION_HITS", 10_000),
			MaxSessionBytes:     int64(envconfig.Int("LANTERN_SEARCH_MAX_SESSION_BYTES", 64<<20)),
			AnalysisLimits: search.SearchAnalysisLimits{
				MaxDocumentBytes:     envconfig.Int("LANTERN_SEARCH_MAX_DOCUMENT_BYTES", 1<<20),
				MaxDocumentTokens:    envconfig.Int("LANTERN_SEARCH_MAX_DOCUMENT_TOKENS", 250_000),
				MaxDocumentTerms:     envconfig.Int("LANTERN_SEARCH_MAX_DOCUMENT_TERMS", 100_000),
				MaxLiveTerms:         envconfig.Int("LANTERN_SEARCH_MAX_LIVE_TERMS", 5_000_000),
				MaxLivePostings:      int64(envconfig.Int("LANTERN_SEARCH_MAX_LIVE_POSTINGS", 50_000_000)),
				MaxPositionEntries:   int64(envconfig.Int("LANTERN_SEARCH_MAX_POSITION_ENTRIES", 50_000_000)),
				CompactionRatio:      envconfig.Float("LANTERN_SEARCH_COMPACTION_RATIO", 2),
				CompactionMinRetired: envconfig.Int("LANTERN_SEARCH_COMPACTION_MIN_RETIRED", 10_000),
			},
		},
		MutationLog: loadMutationLogConfig(),
		Replication: loadReplicationConfig(),
		Readiness:   loadReadinessConfig(),
		Peer:        peer,
		AntiEntropy: loadAntiEntropyConfig(),
		CORS:        loadCORSConfig(),
		Backup:      loadBackupConfig(),
	}
	// Config validation runs before Wire can construct and install the
	// process-wide logger. Build the same configured logger here so bootstrap
	// records are structured (and keep their severity in collectors such as
	// GKE Cloud Logging) instead of falling back to slog's plain-text stderr
	// handler.
	if err := validateEnv(os.Environ(), newLogger(cfg.Observability, os.Stderr)); err != nil {
		return nil, err
	}
	// Unlike the malformed/unknown-variable findings (warn, and only fail under
	// LANTERN_STRICT_CONFIG), an unrecognised LANTERN_SEARCH_DEFAULT_MODE always
	// fails boot: the value is a valid string, so it slips past parsing, but a
	// typo silently rewrites server-wide default ranking semantics — an operator
	// must learn about it at startup, not from confusing search results (#911).
	if err := service.ValidateMatchMode(cfg.Search.DefaultMode); err != nil {
		return nil, err
	}
	if err := validateSearchConfig(cfg.Search); err != nil {
		return nil, err
	}
	return cfg, nil
}

func validateSearchConfig(c SearchConfig) error {
	if c.DefaultLimit == 0 || c.MaxLimit == 0 || c.DefaultLimit > c.MaxLimit {
		return fmt.Errorf("search limits require 0 < default_limit <= max_limit")
	}
	if c.DefaultMinShould == 0 {
		return fmt.Errorf("LANTERN_SEARCH_DEFAULT_MIN_SHOULD must be positive")
	}
	if c.MaxSessionHits <= 0 || uint64(c.MaxSessionHits) <= uint64(c.MaxLimit) {
		return fmt.Errorf("LANTERN_SEARCH_MAX_SESSION_HITS must exceed LANTERN_SEARCH_MAX_LIMIT")
	}
	values := []struct {
		name  string
		value int64
	}{
		{"LANTERN_SEARCH_TIMEOUT_MS", c.Timeout.Milliseconds()},
		{"LANTERN_SEARCH_MAX_QUERY_BYTES", int64(c.MaxQueryBytes)},
		{"LANTERN_SEARCH_MAX_QUERY_TERMS", int64(c.MaxQueryTerms)},
		{"LANTERN_SEARCH_MAX_DICTIONARY_VISITS", int64(c.MaxDictionaryVisits)},
		{"LANTERN_SEARCH_MAX_POSTING_VISITS", int64(c.MaxPostingVisits)},
		{"LANTERN_SEARCH_MAX_POSITION_VISITS", int64(c.MaxPositionVisits)},
		{"LANTERN_SEARCH_MAX_EXPIRATION_VISITS", int64(c.MaxExpirationVisits)},
		{"LANTERN_SEARCH_MAX_IN_FLIGHT", int64(c.MaxInFlight)},
		{"LANTERN_SEARCH_CURSOR_TTL_SECONDS", int64(c.CursorTTL / time.Second)},
		{"LANTERN_SEARCH_MAX_SESSIONS", int64(c.MaxSessions)},
		{"LANTERN_SEARCH_MAX_SESSION_HITS", int64(c.MaxSessionHits)},
		{"LANTERN_SEARCH_MAX_SESSION_BYTES", c.MaxSessionBytes},
		{"LANTERN_SEARCH_MAX_DOCUMENT_BYTES", int64(c.AnalysisLimits.MaxDocumentBytes)},
		{"LANTERN_SEARCH_MAX_DOCUMENT_TOKENS", int64(c.AnalysisLimits.MaxDocumentTokens)},
		{"LANTERN_SEARCH_MAX_DOCUMENT_TERMS", int64(c.AnalysisLimits.MaxDocumentTerms)},
		{"LANTERN_SEARCH_MAX_LIVE_TERMS", int64(c.AnalysisLimits.MaxLiveTerms)},
		{"LANTERN_SEARCH_MAX_LIVE_POSTINGS", c.AnalysisLimits.MaxLivePostings},
		{"LANTERN_SEARCH_MAX_POSITION_ENTRIES", c.AnalysisLimits.MaxPositionEntries},
		{"LANTERN_SEARCH_COMPACTION_MIN_RETIRED", int64(c.AnalysisLimits.CompactionMinRetired)},
	}
	if c.AnalysisLimits.CompactionRatio <= 1 || math.IsNaN(c.AnalysisLimits.CompactionRatio) || math.IsInf(c.AnalysisLimits.CompactionRatio, 0) {
		return fmt.Errorf("LANTERN_SEARCH_COMPACTION_RATIO must be greater than 1")
	}
	for _, item := range values {
		if item.value <= 0 {
			return fmt.Errorf("%s must be positive", item.name)
		}
	}
	if c.MaxQueryTerms > c.MaxQueryBytes {
		return fmt.Errorf("LANTERN_SEARCH_MAX_QUERY_TERMS must not exceed LANTERN_SEARCH_MAX_QUERY_BYTES")
	}
	return nil
}

func loadTraversalConfig() TraversalConfig {
	const (
		defaultTimeoutMS      = 5_000
		defaultMaxPushes      = 1_000_000
		defaultMaxTouchedEdge = 10_000_000
		defaultMaxResults     = 1_024
	)
	timeoutMS := envconfig.Int("LANTERN_TRAVERSAL_TIMEOUT_MS", defaultTimeoutMS)
	if timeoutMS < 0 {
		envconfig.Malformed("LANTERN_TRAVERSAL_TIMEOUT_MS", fmt.Sprint(timeoutMS), "must be zero or a positive millisecond duration")
		timeoutMS = defaultTimeoutMS
	}
	positive := func(key string, def int) int {
		v := envconfig.Int(key, def)
		if v <= 0 {
			envconfig.Malformed(key, fmt.Sprint(v), "must be positive")
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

// foreignEnvPrefixes names LANTERN_*-prefixed namespaces owned by sibling
// processes that legitimately share an env file with the server, so the
// unknown-variable sweep does not flag them (#847). The MCP server reads
// LANTERN_MCP_* in its own process; LANTERN_TOKEN is the CLIENT-side auth
// token consumed by lantern-cli and the MCP server (#850) — a compose file
// commonly sets it alongside the server's LANTERN_AUTH_TOKENS.
var foreignEnvPrefixes = []string{"LANTERN_MCP_", "LANTERN_TOKEN"}

// validateEnv is the boot-time config validation pass (#847). It runs after
// every loader has registered its variables, so the envconfig registry is
// the complete env contract at this point. It reports through the bootstrap
// logger supplied by NewConfig and fails only under LANTERN_STRICT_CONFIG.
func validateEnv(environ []string, log *slog.Logger) error {
	strict := envconfig.Bool("LANTERN_STRICT_CONFIG", false)

	findings := envconfig.Findings()
	for _, f := range findings {
		log.Warn("config: malformed value, using default",
			slog.String("key", f.Key),
			slog.String("value", f.Raw),
			slog.String("reason", f.Reason))
	}

	unknown := envconfig.UnknownLanternVars(environ, foreignEnvPrefixes...)
	for _, u := range unknown {
		if u.Suggestion != "" {
			log.Warn("config: unknown LANTERN_* variable (typo?)",
				slog.String("key", u.Key),
				slog.String("did_you_mean", u.Suggestion))
		} else {
			log.Warn("config: unknown LANTERN_* variable",
				slog.String("key", u.Key))
		}
	}

	// One summary line naming which knobs are set (keys only — values are
	// deliberately withheld so a future secret-bearing variable can never
	// leak into the boot log).
	if set := envconfig.SetKeys(); len(set) > 0 {
		log.Info("config: environment overrides active", slog.Any("keys", set))
	}

	if strict && (len(findings) > 0 || len(unknown) > 0) {
		var parts []string
		for _, f := range findings {
			parts = append(parts, fmt.Sprintf("malformed %s=%q (%s)", f.Key, f.Raw, f.Reason))
		}
		for _, u := range unknown {
			if u.Suggestion != "" {
				parts = append(parts, fmt.Sprintf("unknown %s (did you mean %s?)", u.Key, u.Suggestion))
			} else {
				parts = append(parts, fmt.Sprintf("unknown %s", u.Key))
			}
		}
		return fmt.Errorf("LANTERN_STRICT_CONFIG: refusing to boot: %s", strings.Join(parts, "; "))
	}
	return nil
}

// Sub-config selectors. Wire uses these to inject each focused struct into
// the providers that need it, so no provider has to depend on the full
// *Config aggregate.

func NewNetConfig(c *Config) NetConfig                     { return c.Net }
func NewTLSConfig(c *Config) TLSConfig                     { return c.TLS }
func NewRateLimitConfig(c *Config) RateLimitConfig         { return c.RateLimit }
func NewObservabilityConfig(c *Config) ObservabilityConfig { return c.Observability }
func NewCacheConfig(c *Config) CacheConfig                 { return c.Cache }
func NewAuthConfig(c *Config) AuthConfig                   { return c.Auth }
func NewShutdownConfig(c *Config) ShutdownConfig           { return c.Shutdown }
func NewValidationLimits(c *Config) ValidationLimits       { return c.Validation }
func NewTraversalConfig(c *Config) TraversalConfig         { return c.Traversal }
func NewScanConfig(c *Config) ScanConfig                   { return c.Scan }
func NewSearchConfig(c *Config) SearchConfig               { return c.Search }

// NewLogger builds the process-wide structured logger and installs it as the
// slog default so any package-level slog.* call inherits the same handler.
func NewLogger(o ObservabilityConfig) *slog.Logger {
	l := newLogger(o, os.Stderr)
	slog.SetDefault(l)
	return l
}

func newLogger(o ObservabilityConfig, w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{Level: o.LogLevel}
	var h slog.Handler
	if strings.EqualFold(o.LogFormat, "text") {
		h = slog.NewTextHandler(w, opts)
	} else {
		h = slog.NewJSONHandler(w, opts)
	}
	return slog.New(h).With(slog.String("service", "lantern"))
}

func NewGraphCache(c CacheConfig, sc SearchConfig) *graphcache.GraphCache[string, *v1.Vertex] {
	gc := graphcache.NewGraphCache[string, *v1.Vertex](c.TTL)
	// Identity projection: vertex keys are themselves the indexed string.
	// Enabling the prefix index up-front (before any insert) is required
	// by the cache contract — EnablePrefixIndex panics on a non-empty
	// cache. Doing it here in the constructor guarantees that invariant.
	gc.EnablePrefixIndex(func(s string) string { return s })
	// Content search (#624) is opt-out. EnableSearchIndex carries the same
	// "before any insert" contract as EnablePrefixIndex, so it must also be
	// called here; gating on sc.Enabled keeps the put hot path free of the
	// inverted-index cost when an operator turns the feature off. Positional
	// postings are a second opt-out (LANTERN_SEARCH_POSITIONS, #908): dropping
	// them shrinks the index at the cost of phrase/proximity fidelity.
	if sc.Enabled {
		var opts []graphcache.SearchIndexOption
		if !sc.Positions {
			opts = append(opts, graphcache.WithoutSearchPositions())
		}
		opts = append(opts, graphcache.WithSearchAnalysisLimits(sc.AnalysisLimits))
		gc.EnableSearchIndex(vertexSearchDocument, strings.Compare, opts...)
	}
	return gc
}

// NewDomainMetrics registers the Lantern-specific `lantern_*` collectors on
// the shared Prometheus registry and binds the gauge sampler that reads
// cache.VertexCount / EdgeCount on each DomainMetrics.Run tick. The GC
// hooks themselves are installed separately by WireCacheGCHooks so the
// metrics adapter can be multiplexed with the structured per-tick log
// (#223).
func NewDomainMetrics(
	reg *prometheus.Registry,
	o ObservabilityConfig,
	cache *graphcache.GraphCache[string, *v1.Vertex],
) *domainmetrics.DomainMetrics {
	m := domainmetrics.New(reg, domainmetrics.Options{
		Version: o.Version,
		Commit:  o.Commit,
	})
	m.BindSampler(func() (int, int) {
		return cache.VertexCount(), cache.EdgeCount()
	})
	m.BindSearchIndexSampler(func() search.IndexMemoryStats {
		return cache.SearchIndexMemoryStats()
	})
	m.BindVertexHLCSampler(func() int {
		return cache.VertexHLCCount()
	})
	m.BindVertexHLCHighWaterSampler(func() int {
		return cache.VertexHLCHighWater()
	})
	return m
}

// CacheGCHooksWired is a marker returned by WireCacheGCHooks so wire can
// force ordering: the wiring must happen after both DomainMetrics and the
// logger are constructed, but the result is not consumed by any downstream
// provider directly. newApp accepts it as an unused parameter.
type CacheGCHooksWired struct{}

// WireCacheGCHooks installs a multiplexed pair of GC hooks on the cache:
// one branch updates DomainMetrics (lantern_cache_evicted_total +
// lantern_cache_gc_duration_seconds), the other emits one info-level
// "graph cache: gc tick" slog line per tick summarising the work done
// (#223). The hooks are fired sequentially by GraphCache.Watch on a
// single goroutine, so the per-tick accumulator inside the closure is
// safe without locking.
func WireCacheGCHooks(
	cache *graphcache.GraphCache[string, *v1.Vertex],
	m *domainmetrics.DomainMetrics,
	logger *slog.Logger,
) CacheGCHooksWired {
	// Per-tick accumulator. GraphCache.Watch calls onExpire 0–3 times
	// (one per non-empty kind) then onGC once — atomically per tick,
	// on a single goroutine — so a plain struct is sufficient.
	var tick struct {
		vertices int
		edges    int
		dangling int
	}
	onExpire := func(kind string, n int) {
		m.OnExpire(kind, n)
		switch kind {
		case "vertex":
			tick.vertices += n
		case "edge":
			tick.edges += n
		case "dangling_edge":
			tick.dangling += n
		}
	}
	onGC := func(d time.Duration) {
		m.OnGCDuration(d)
		logger.LogAttrs(context.Background(), slog.LevelInfo, "graph cache: gc tick",
			slog.Int("vertices_expired", tick.vertices),
			slog.Int("edges_expired", tick.edges),
			slog.Int("dangling_edges_removed", tick.dangling),
			slog.Int("vertices_remaining", cache.VertexCount()),
			slog.Int("edges_remaining", cache.EdgeCount()),
			slog.Int64("duration_ms", d.Milliseconds()),
		)
		tick.vertices, tick.edges, tick.dangling = 0, 0, 0
	}
	cache.SetGCHooks(onExpire, onGC)
	return CacheGCHooksWired{}
}

func NewListener(n NetConfig) (net.Listener, error) {
	return net.Listen("tcp", ":"+strconv.Itoa(n.Port))
}

// NewHealthChecker is provided by health.go and serves the
// `grpc.health.v1.Health` wire surface via connectrpc.com/grpchealth.
// Wire binds *HealthChecker into service.HealthSetter +
// readiness.HealthSetter.

// NewPrometheusRegistry isolates server metrics in a dedicated registry so the
// global default stays clean.
func NewPrometheusRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return reg
}

// NewGrpcServerMetrics has been removed; PrometheusInterceptor in
// connect_middleware.go registers the canonical `grpc_server_*` metric
// names so dashboards keep working.

// NewGrpcServerOptions and NewGrpcServer have been removed.
// The primary :6380 listener runs *http.Server with Connect-Go
// handlers and Connect interceptors (PrometheusInterceptor +
// LoggingInterceptor + SlowRPCInterceptor + ValidationInterceptor +
// RateLimitInterceptor + connect.WithRecover).
// See lantern_listener.go and connect_middleware.go.

// MetricsServer is the long-running goroutine that exposes /metrics
// (Prometheus) and /healthz + /readyz on a dedicated HTTP port. The
// NewMetricsServer constructor returns a real HTTP server when
// LANTERN_METRICS_ADDR is set and a NoopMetricsServer otherwise, so callers
// never have to nil-check before calling Run (Null Object pattern).
type MetricsServer interface {
	// Run blocks until ctx is canceled or the underlying server exits with
	// a non-shutdown error.
	Run(ctx context.Context) error
}

// NoopMetricsServer is the disabled-metrics implementation. Its Run simply
// waits for ctx to be canceled and returns nil, so the App errgroup behaves
// identically whether or not metrics are enabled.
type NoopMetricsServer struct{}

func (NoopMetricsServer) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// httpMetricsServer is the real /metrics + /healthz + /readyz HTTP server.
type httpMetricsServer struct {
	srv    *http.Server
	logger *slog.Logger
}

func NewMetricsServer(o ObservabilityConfig, reg *prometheus.Registry, gate *readiness.Gate, logger *slog.Logger) MetricsServer {
	if o.MetricsAddr == "" {
		return NoopMetricsServer{}
	}
	return &httpMetricsServer{
		srv: &http.Server{
			Addr:              o.MetricsAddr,
			Handler:           newMetricsMux(reg, gate, o.EnablePprof),
			ReadHeaderTimeout: 5 * time.Second,
		},
		logger: logger,
	}
}

// newMetricsMux builds the /metrics + /healthz + /readyz + /healthz/ready
// handler tree. Extracted so tests can exercise the readiness-aware HTTP
// shim with httptest without binding a real port. When enablePprof is true,
// /debug/pprof/* is additionally mounted; otherwise those paths 404.
func newMetricsMux(reg *prometheus.Registry, gate *readiness.Gate, enablePprof bool) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
	if enablePprof {
		registerPprofHandlers(mux)
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	// /readyz and /healthz/ready both consult the readiness Gate so HTTP
	// probes (k8s httpGet, container-platform startup probes, plain LB
	// health probes) see the same drain signal as the `grpc.health.v1`
	// overall ("") entry served on the primary listener. Single-instance
	// mode returns 200 immediately — platform startup behaviour is unchanged.
	readyHandler := func(w http.ResponseWriter, _ *http.Request) {
		if gate == nil || gate.Ready() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ready"))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not ready"))
	}
	mux.HandleFunc("/readyz", readyHandler)
	mux.HandleFunc("/healthz/ready", readyHandler)
	return mux
}

// Run blocks until ctx is canceled or ListenAndServe returns an error other
// than http.ErrServerClosed.
func (m *httpMetricsServer) Run(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = m.srv.Shutdown(shutdownCtx)
	}()
	m.logger.Info("metrics server starting", slog.String("addr", m.srv.Addr))
	if err := m.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// RegisterHealth + RegisterReflection have been removed by #347. Both
// surfaces are now mounted directly on the http.Server mux by
// NewLanternListener via connectrpc.com/grpchealth and
// connectrpc.com/grpcreflect; the env-var contract (LANTERN_REFLECTION)
// is unchanged.

// splitTokens parses the comma-separated LANTERN_AUTH_TOKENS value (#850).
// Entries are trimmed and empties dropped so "old,new," cannot admit the
// empty token.
func splitTokens(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
