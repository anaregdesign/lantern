// Package config owns ordinary Server operational values, defaults and value
// validation. Runtime construction and trust certification belong to provider.
// Loading uses envconfig's process-wide registry; it is not an isolated or
// concurrently independent operation.
package config

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
// findings using envconfig. The composition root supplies the metrics default
// after it has resolved the authentication mode. Call CheckEnvironment only
// after every other Server loader has registered its settings, then Validate.
func Load(metricsDefault string) Config {
	return Config{
		Net:           loadNetwork(),
		RateLimit:     loadRateLimit(),
		Observability: loadObservability(metricsDefault),
		Cache:         loadCache(),
		Shutdown:      loadShutdown(),
		Validation:    loadValidation(),
		Traversal:     loadTraversal(),
		Scan:          loadScan(),
		Search:        loadSearch(),
		Tracing:       loadTracing(),
	}
}
