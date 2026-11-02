// Package config owns ordinary Server operational values, defaults and value
// validation. Runtime construction and trust certification belong to provider.
// Loading uses an explicit environment Reader shared by the composition root.
package config

import "github.com/anaregdesign/lantern/server/internal/envconfig"

// Config groups ordinary values for loading and validation. Constructors should
// receive the focused value group they need rather than this aggregate.
type Config struct {
	Net           NetConfig
	RateLimit     RateLimitConfig
	Observability ObservabilityConfig
	Cache         CacheConfig
	Shutdown      ShutdownConfig
	Validation    ValidationLimits
	Traversal     TraversalConfig
	Scan          ScanConfig
	Search        SearchConfig
	Tracing       TracingConfig
}

// Load reads operational environment values and records primitive parser
// findings using reader. The composition root supplies the metrics default
// after it has resolved the authentication mode. Call CheckEnvironment only
// after every other Server loader has registered its settings, then Validate.
func Load(reader *envconfig.Reader, metricsDefault string) Config {
	return Config{
		Net:           loadNetwork(reader),
		RateLimit:     loadRateLimit(reader),
		Observability: loadObservability(reader, metricsDefault),
		Cache:         loadCache(reader),
		Shutdown:      loadShutdown(reader),
		Validation:    loadValidation(reader),
		Traversal:     loadTraversal(reader),
		Scan:          loadScan(reader),
		Search:        loadSearch(reader),
		Tracing:       loadTracing(reader),
	}
}
