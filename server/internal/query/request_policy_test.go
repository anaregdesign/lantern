package query

import (
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/search"
)

func policyTestLimits() Limits {
	l := DefaultLimits()
	l.Enabled, l.PositionsEnabled, l.DefaultMinShould = true, true, 1
	return l
}

func TestRequestPolicyResolveRejectsInvalidCanonicalValues(t *testing.T) {
	baseline := policyTestLimits()
	for _, tc := range []struct {
		name   string
		change func(*RequestPolicy)
	}{
		{"default zero", func(p *RequestPolicy) { p.DefaultLimit = 0 }},
		{"max zero", func(p *RequestPolicy) { p.MaxLimit = 0 }},
		{"pair", func(p *RequestPolicy) { p.DefaultLimit = p.MaxLimit + 1 }},
		{"unknown mode", func(p *RequestPolicy) { p.DefaultMode = search.MatchMode(255) }},
		{"min should", func(p *RequestPolicy) { p.DefaultMinShould = 0 }},
		{"timeout zero", func(p *RequestPolicy) { p.Timeout = 0 }},
		{"timeout negative", func(p *RequestPolicy) { p.Timeout = -time.Millisecond }},
		{"timeout fractional", func(p *RequestPolicy) { p.Timeout = time.Millisecond + 1 }},
		{"query bytes", func(p *RequestPolicy) { p.MaxQueryBytes = 0 }},
		{"query terms", func(p *RequestPolicy) { p.WorkBudget.MaxQueryTerms = 0 }},
		{"terms bytes relationship", func(p *RequestPolicy) { p.WorkBudget.MaxQueryTerms = int64(p.MaxQueryBytes) + 1 }},
		{"dictionary", func(p *RequestPolicy) { p.WorkBudget.MaxDictionaryVisits = -1 }},
		{"postings", func(p *RequestPolicy) { p.WorkBudget.MaxPostingVisits = 0 }},
		{"positions", func(p *RequestPolicy) { p.WorkBudget.MaxPositionVisits = 0 }},
		{"expirations", func(p *RequestPolicy) { p.WorkBudget.MaxExpirationVisits = 0 }},
		{"external session dependency", func(p *RequestPolicy) { p.MaxLimit = uint32(baseline.MaxSessionHits) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := RequestPolicyFromLimits(baseline)
			tc.change(&p)
			if _, fingerprint, err := p.Resolve(baseline); err == nil || fingerprint != "" {
				t.Fatalf("invalid policy resolved: fingerprint=%s err=%v", fingerprint, err)
			}
		})
	}
}

func TestRequestPolicyResolveKeepsActualExcludedBaseline(t *testing.T) {
	baseline := policyTestLimits()
	baseline.AnalysisLimits.MaxLiveTerms = 12345
	p := RequestPolicyFromLimits(baseline)
	p.DefaultLimit, p.MaxLimit, p.DefaultMode = 7, 11, search.MatchAll
	resolved, fingerprint, err := p.Resolve(baseline)
	if err != nil {
		t.Fatal(err)
	}
	if RequestPolicyFromLimits(resolved) != p || fingerprint != ConfigFingerprint(resolved) {
		t.Fatal("resolved candidate differs from exact requested values")
	}
	// Replacing all selected fields back must recover every excluded input.
	old := RequestPolicyFromLimits(baseline)
	restored, _, err := old.Resolve(resolved)
	if err != nil || restored != baseline {
		t.Fatalf("excluded inputs changed: %+v/%v", restored, err)
	}
	different := baseline
	different.AnalysisLimits.MaxLiveTerms++
	_, otherFingerprint, err := p.Resolve(different)
	if err != nil || otherFingerprint == fingerprint {
		t.Fatal("different local index baseline did not change compatibility proof")
	}
	for _, change := range []func(*Limits){
		func(l *Limits) { l.MaxSessionHits = 11 },
		func(l *Limits) { l.MaxInFlight = 0 },
		func(l *Limits) { l.CursorTTL = 0 },
		func(l *Limits) { l.MaxSessions = 0 },
		func(l *Limits) { l.MaxSessionBytes = 0 },
	} {
		invalid := baseline
		change(&invalid)
		if _, _, err := p.Resolve(invalid); err == nil {
			t.Fatalf("invalid excluded baseline normalized silently: %+v", invalid)
		}
	}
}
