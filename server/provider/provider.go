package provider

import (
	"os"
	"strings"

	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/internal/config"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
)

// These aliases preserve focused provider injection while operational value
// definitions are owned exclusively by internal/config.
type NetConfig = config.NetConfig
type RateLimitConfig = config.RateLimitConfig
type ObservabilityConfig = config.ObservabilityConfig
type ShutdownConfig = config.ShutdownConfig
type TraversalConfig = config.TraversalConfig
type ScanConfig = config.ScanConfig
type SearchConfig = config.SearchConfig
type TracingConfig = config.TracingConfig

// CacheSettings names the operational cache values used by CacheConfig.
type CacheSettings = config.CacheConfig

// CacheConfig adds the private namespace selection to ordinary cache values.
// Namespace ownership remains a composition concern, not an operational knob.
type CacheConfig struct {
	CacheSettings
	namespaceFormat string
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
	CertPEM      string
	KeyPEM       string
	ClientCAPEM  string
}

// Config aggregates every focused sub-config. It is constructed once at
// startup by NewConfig and then projected into sub-configs that each provider
// consumes — providers MUST NOT take *Config when they only need one slice of
// it (SRP). main / App may keep *Config because they observe several aspects
// for startup logging.
type Config struct {
	Net                NetConfig
	TLS                TLSConfig
	RateLimit          RateLimitConfig
	Observability      ObservabilityConfig
	Tracing            TracingConfig
	Cache              CacheConfig
	Shutdown           ShutdownConfig
	Validation         ValidationLimits
	Traversal          TraversalConfig
	Auth               AuthConfig
	Security           SecurityConfig
	Changes            ChangeConfig
	LLM                LLMConfig
	Scan               ScanConfig
	Search             SearchConfig
	MutationLog        MutationLogConfig
	ReceiptWAL         ReceiptWALConfig
	GraphSnapshotSpool GraphSnapshotSpoolConfig
	Replication        ReplicationConfig
	Peer               PeerConfig
	PeerPlane          PeerPlaneConfig
	AntiEntropy        AntiEntropyConfig
	Readiness          ReadinessConfig
	CORS               CORSConfig
	Backup             backup.Config
}

// NewConfig loads every sub-config from the environment, then validates the
// load (#847): set-but-malformed values and unknown LANTERN_* names are
// logged as warnings, and — when LANTERN_STRICT_CONFIG=true — turned into a
// boot failure so a typo is caught in staging instead of silently running on
// defaults. Strict mode turns those parser findings into a boot failure;
// semantic validation failures such as negative capacity budgets are rejected
// in every mode.
func NewConfig() (*Config, error) {
	if err := validateAuthMode(); err != nil {
		return nil, err
	}
	securityConfig, err := loadSecurityConfig()
	if err != nil {
		return nil, err
	}
	peer := loadPeerConfig()
	peerPlane, err := loadPeerPlaneConfig(securityConfig, peer)
	if err != nil {
		return nil, err
	}
	changeConfig, err := loadChangeConfig(securityConfig, peerPlane)
	if err != nil {
		return nil, err
	}
	receiptWAL := loadReceiptWALConfig()
	graphSnapshotSpool, err := selectGraphSnapshotSpoolForSecurity(securityConfig, receiptWAL.Mode)
	if err != nil {
		return nil, err
	}
	metricsDefault := ":9090"
	if securityConfig.Mode == "oidc" {
		metricsDefault = "127.0.0.1:9090"
	}
	operational := config.Load(metricsDefault)
	cfg := &Config{
		Security:           securityConfig,
		PeerPlane:          peerPlane,
		Changes:            changeConfig,
		GraphSnapshotSpool: graphSnapshotSpool,
		Net:                operational.Net,
		TLS: TLSConfig{
			CertFile:     envconfig.String("LANTERN_TLS_CERT_FILE", ""),
			KeyFile:      envconfig.String("LANTERN_TLS_KEY_FILE", ""),
			ClientCAFile: envconfig.String("LANTERN_TLS_CLIENT_CA_FILE", ""),
		},
		RateLimit:     operational.RateLimit,
		Observability: operational.Observability,
		Cache:         CacheConfig{CacheSettings: operational.Cache, namespaceFormat: keyspace.Version},
		Shutdown:      operational.Shutdown,
		Validation:    operational.Validation,
		Traversal:     operational.Traversal,
		Tracing:       operational.Tracing,
		Auth: AuthConfig{
			Tokens:           splitTokens(envconfig.String("LANTERN_AUTH_TOKENS", "")),
			ExemptReflection: envconfig.Bool("LANTERN_AUTH_EXEMPT_REFLECTION", true),
		},
		LLM:         loadLLMConfig(),
		Scan:        operational.Scan,
		Search:      operational.Search,
		MutationLog: loadMutationLogConfig(),
		ReceiptWAL:  receiptWAL,
		Replication: loadReplicationConfig(),
		Readiness:   loadReadinessConfig(),
		Peer:        peer,
		AntiEntropy: loadAntiEntropyConfig(),
		CORS:        loadCORSConfig(),
		Backup:      loadBackupConfig(),
	}
	if len(securityConfig.AuthorityJSON) != 0 {
		cfg.TLS.CertPEM = securityConfig.TLS.CertPEM
		cfg.TLS.KeyPEM = securityConfig.TLS.KeyPEM
		cfg.TLS.ClientCAPEM = securityConfig.TLS.ClientCAPEM
	}
	// Config validation runs before Wire can construct and install the
	// process-wide logger. Build the same configured logger here so bootstrap
	// records are structured (and keep their severity in collectors such as
	// GKE Cloud Logging) instead of falling back to slog's plain-text stderr
	// handler.
	if err := config.CheckEnvironment(os.Environ(), newLogger(cfg.Observability, os.Stderr)); err != nil {
		return nil, err
	}
	if err := operational.Validate(); err != nil {
		return nil, err
	}
	if err := validateReceiptWALConfig(cfg.ReceiptWAL, cfg.Backup, cfg.Replication); err != nil {
		return nil, err
	}
	if err := validateGraphSnapshotSpoolConfig(cfg.GraphSnapshotSpool, cfg.ReceiptWAL.Mode, cfg.PeerPlane); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Sub-config selectors. Wire uses these to inject each focused struct into
// the providers that need it, so no provider has to depend on the full
// *Config aggregate.

func NewTracingConfig(c *Config) TracingConfig             { return c.Tracing }
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
