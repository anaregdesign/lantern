package query

import (
	"errors"
	"time"

	"github.com/anaregdesign/lantern/core/search"
)

// RequestPolicy contains exactly the eleven managed search request settings.
// Index construction, admission and session resource settings remain local.
type RequestPolicy struct {
	DefaultLimit     uint32
	MaxLimit         uint32
	DefaultMode      search.MatchMode
	DefaultMinShould uint32
	Timeout          time.Duration
	MaxQueryBytes    int
	WorkBudget       search.Budget
}

func RequestPolicyFromLimits(l Limits) RequestPolicy {
	return RequestPolicy{l.DefaultLimit, l.MaxLimit, l.DefaultMode, l.DefaultMinShould, l.Timeout, l.MaxQueryBytes, l.WorkBudget}
}

// Resolve validates a candidate against the actual local engine baseline.
// It neither reads environment inputs nor normalizes/clamps authoritative data.
// Callers bind the resulting full fingerprint in their certified preflight.
func (p RequestPolicy) Resolve(baseline Limits) (Limits, string, error) {
	if p.DefaultLimit == 0 || p.MaxLimit == 0 || p.DefaultLimit > p.MaxLimit {
		return Limits{}, "", errors.New("search limits require 0 < default <= maximum")
	}
	if p.DefaultMode != search.MatchAny && p.DefaultMode != search.MatchAll && p.DefaultMode != search.MatchMinShould {
		return Limits{}, "", errors.New("search default mode must be any, all or min-should")
	}
	if p.DefaultMinShould == 0 || p.Timeout <= 0 || p.Timeout%time.Millisecond != 0 || p.MaxQueryBytes <= 0 {
		return Limits{}, "", errors.New("search min-should, whole-millisecond timeout and query bytes must be positive")
	}
	// These existing public capabilities are uint32. Managed policy must
	// preserve their exact representation rather than wrapping the status.
	const maxCapability = int64(1<<32 - 1)
	if p.Timeout.Milliseconds() > maxCapability || int64(p.MaxQueryBytes) > maxCapability || p.WorkBudget.MaxQueryTerms > maxCapability {
		return Limits{}, "", errors.New("managed search timeout, query bytes and query terms must fit uint32 capabilities")
	}
	b := p.WorkBudget
	if b.MaxQueryTerms <= 0 || b.MaxQueryTerms > int64(p.MaxQueryBytes) || b.MaxDictionaryVisits <= 0 || b.MaxPostingVisits <= 0 || b.MaxPositionVisits <= 0 || b.MaxExpirationVisits <= 0 {
		return Limits{}, "", errors.New("search work budgets must be positive and query terms must not exceed query bytes")
	}
	if baseline.MaxSessionHits <= 0 || uint64(baseline.MaxSessionHits) <= uint64(p.MaxLimit) {
		return Limits{}, "", errors.New("local search session hit cap must exceed managed search maximum")
	}
	if baseline.MaxInFlight <= 0 || baseline.CursorTTL <= 0 || baseline.MaxSessions <= 0 || baseline.MaxSessionBytes <= 0 {
		return Limits{}, "", errors.New("local search admission and session baseline must be positive")
	}
	resolved := baseline
	resolved.DefaultLimit, resolved.MaxLimit = p.DefaultLimit, p.MaxLimit
	resolved.DefaultMode, resolved.DefaultMinShould = p.DefaultMode, p.DefaultMinShould
	resolved.Timeout, resolved.MaxQueryBytes, resolved.WorkBudget = p.Timeout, p.MaxQueryBytes, p.WorkBudget
	return resolved, ConfigFingerprint(resolved), nil
}
