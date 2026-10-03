package security

import (
	"strings"
	"unicode/utf8"
)

// AllowsEdgeAction checks a complete directed selector for one action. It
// grants no implicit endpoint/action permission; public RPCs add those checks.
func (a *Access) AllowsEdgeAction(action Action, tail, head string) bool {
	if a == nil || !edgeSelectorAction(action) || tail == "" || head == "" || !utf8.ValidString(tail) || !utf8.ValidString(head) {
		return false
	}
	var tailAllowed, headAllowed, pairAllowed bool
	for _, role := range a.roles {
		prefix := role.data[action]
		if matchesPrefix(prefix.deny, tail) || matchesPrefix(prefix.deny, head) {
			return false
		}
		tailAllowed = tailAllowed || matchesPrefix(prefix.allow, tail)
		headAllowed = headAllowed || matchesPrefix(prefix.allow, head)
		pairs := role.pairs[action]
		for _, pair := range pairs.deny {
			if strings.HasPrefix(tail, pair.Tail) && strings.HasPrefix(head, pair.Head) {
				return false
			}
		}
		for _, pair := range pairs.allow {
			pairAllowed = pairAllowed || strings.HasPrefix(tail, pair.Tail) && strings.HasPrefix(head, pair.Head)
		}
	}
	return pairAllowed || tailAllowed && headAllowed
}

// EdgeRanges is one action's union of complete selectors minus all applicable
// Deny selectors. Returned slices are detached and carry no graph data.
type EdgeRanges struct {
	Endpoints         []Range
	ExcludedEndpoints []Range
	Pairs             [][2]Range
	ExcludedPairs     [][2]Range
}

func (a *Access) HasPairSelectors(actions ...Action) bool {
	if a == nil {
		return false
	}
	for _, action := range actions {
		for _, role := range a.roles {
			p := role.pairs[action]
			if len(p.allow)+len(p.deny) > 0 {
				return true
			}
		}
	}
	return false
}
func (a *Access) EdgeRanges(action Action) EdgeRanges {
	var result EdgeRanges
	if a == nil {
		return result
	}
	var allow, deny []string
	pairRange := func(pair PrefixPair) [2]Range {
		return [2]Range{{pair.Tail, prefixEnd(pair.Tail)}, {pair.Head, prefixEnd(pair.Head)}}
	}
	for _, role := range a.roles {
		p := role.data[action]
		allow = append(allow, p.allow...)
		deny = append(deny, p.deny...)
		pairs := role.pairs[action]
		for _, pair := range pairs.allow {
			result.Pairs = append(result.Pairs, pairRange(pair))
		}
		for _, pair := range pairs.deny {
			result.ExcludedPairs = append(result.ExcludedPairs, pairRange(pair))
		}
	}
	for _, p := range disjointPrefixes(allow) {
		result.Endpoints = append(result.Endpoints, Range{p, prefixEnd(p)})
	}
	for _, p := range disjointPrefixes(deny) {
		result.ExcludedEndpoints = append(result.ExcludedEndpoints, Range{p, prefixEnd(p)})
	}
	return result
}

// EdgeCandidateScope is a safe endpoint superset for range seeks. Pair Deny
// requires the final two-dimensional predicate and never broadens a result.
func (a *Access) EdgeCandidateScope(actions ...Action) *Scope {
	if a == nil || len(actions) == 0 {
		return &Scope{}
	}
	var result *Scope
	for _, action := range actions {
		index := scopeActionIndex(action)
		if index < 0 {
			return &Scope{}
		}
		compiled := a.cache.get("edge:"+string(rune(index+1))+a.setKey, func() *Scope {
			var allow, deny []string
			for _, role := range a.roles {
				p := role.data[action]
				allow = append(allow, p.allow...)
				deny = append(deny, p.deny...)
				for _, pair := range role.pairs[action].allow {
					allow = append(allow, pair.Tail, pair.Head)
				}
			}
			return prefixScope(allow, deny)
		})
		if result == nil {
			result = compiled
		} else {
			result = intersectScopes(result, compiled)
		}
		if result.Empty() {
			return result
		}
	}
	return result
}
