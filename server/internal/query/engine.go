// Package query owns bounded search execution and retained result paging.
// Storage publication and authorization remain with the caller's Reader.
package query

import "golang.org/x/sync/semaphore"

// Engine owns one endpoint's search configuration, admission gate and cursor
// sessions. Construct it before serving requests; its execution methods are
// safe for concurrent use.
type Engine struct {
	limits            Limits
	configFingerprint string
	gate              *semaphore.Weighted
	sessions          *searchSessionStore
}

func New(limits Limits) *Engine {
	limits = NormalizeLimits(limits)
	engine := &Engine{
		limits:            limits,
		configFingerprint: ConfigFingerprint(limits),
		sessions:          newSearchSessionStore(limits),
	}
	if limits.MaxInFlight > 0 {
		engine.gate = semaphore.NewWeighted(int64(limits.MaxInFlight))
	}
	return engine
}

// Limits returns a value copy of the effective, normalized configuration.
func (s *Engine) Limits() Limits {
	if s == nil {
		return Limits{}
	}
	return s.limits
}

// ConfigFingerprint is shared by status discovery, peer compatibility and
// signed cursor validation.
func (s *Engine) ConfigFingerprint() string {
	if s == nil {
		return ConfigFingerprint(Limits{})
	}
	return s.configFingerprint
}
