package security

import (
	"sort"
	"unicode/utf8"
)

// Range is a binary half-open logical-key interval. Empty Upper denotes
// positive infinity; bounds need not themselves be valid UTF-8 keys.
type Range struct {
	Lower string
	Upper string
}

// Scope is the immutable union of Allow prefixes minus every applicable Deny.
// It contains no graph data and is compiled before entering a query lock.
type Scope struct{ ranges []Range }

func (s *Scope) Empty() bool { return s == nil || len(s.ranges) == 0 }
func (s *Scope) Ranges() []Range {
	if s == nil {
		return nil
	}
	return append([]Range(nil), s.ranges...)
}
func (s *Scope) Contains(key string) bool {
	if s == nil || key == "" || !utf8.ValidString(key) {
		return false
	}
	i := sort.Search(len(s.ranges), func(i int) bool { return s.ranges[i].Lower > key }) - 1
	return i >= 0 && (s.ranges[i].Upper == "" || key < s.ranges[i].Upper)
}

// Within narrows a permission scope to a literal requested prefix.
func (s *Scope) Within(prefix string) *Scope {
	if s == nil || !utf8.ValidString(prefix) {
		return &Scope{}
	}
	return intersectScopes(s, &Scope{ranges: []Range{{prefix, prefixEnd(prefix)}}})
}

// Scope intersects the required actions. No action hierarchy or implicit
// permission is introduced by the query optimizer.
func (a *Access) Scope(actions ...Action) *Scope {
	if a == nil || len(actions) == 0 {
		return &Scope{}
	}
	var result *Scope
	for _, action := range actions {
		i := scopeActionIndex(action)
		if i < 0 {
			return &Scope{}
		}
		compiled := a.cache.get(string(rune(i+1))+a.setKey, func() *Scope { return a.compileScope(action) })
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

func scopeActionIndex(action Action) int {
	switch action {
	case VertexRead:
		return 0
	case VertexWrite:
		return 1
	case VertexDelete:
		return 2
	case EdgeRead:
		return 3
	case EdgeAdd:
		return 4
	case EdgeWrite:
		return 5
	case EdgeDelete:
		return 6
	case Query:
		return 7
	case CDCIdentity:
		return 8
	case CDCValue:
		return 9
	case Export:
		return 10
	case ReceiptRead:
		return 11
	case EdgeCreate:
		return 12
	default:
		return -1
	}
}

func prefixEnd(prefix string) string {
	end := []byte(prefix)
	for i := len(end) - 1; i >= 0; i-- {
		if end[i] != 255 {
			end[i]++
			return string(end[:i+1])
		}
	}
	return ""
}

func (a *Access) compileScope(action Action) *Scope {
	var allow, deny []string
	for _, role := range a.roles {
		allow = append(allow, role.data[action].allow...)
		deny = append(deny, role.data[action].deny...)
	}
	return prefixScope(allow, deny)
}

func prefixScope(allow, deny []string) *Scope {
	allow, deny = disjointPrefixes(allow), disjointPrefixes(deny)
	result := &Scope{}
	d := 0
	for _, prefix := range allow {
		lower, upper := prefix, prefixEnd(prefix)
		for d < len(deny) && endBefore(prefixEnd(deny[d]), lower) {
			d++
		}
		for j := d; j < len(deny) && (upper == "" || deny[j] < upper); j++ {
			if deny[j] > lower {
				result.ranges = append(result.ranges, Range{lower, deny[j]})
			}
			end := prefixEnd(deny[j])
			if end == "" || upper != "" && end >= upper {
				lower = upper
				// An infinite Deny removes the remainder even when upper is
				// also infinity; the separate flag avoids an empty-bound mixup.
				goto exhausted
			}
			if end > lower {
				lower = end
			}
		}
		if upper == "" || lower < upper {
			result.ranges = append(result.ranges, Range{lower, upper})
		}
	exhausted:
	}
	return result
}

func endBefore(end, lower string) bool { return end != "" && end <= lower }
func minEnd(a, b string) string {
	if a == "" || b != "" && b < a {
		return b
	}
	return a
}
func intersectScopes(a, b *Scope) *Scope {
	result := &Scope{}
	for i, j := 0, 0; i < len(a.ranges) && j < len(b.ranges); {
		x, y := a.ranges[i], b.ranges[j]
		lower, upper := max(x.Lower, y.Lower), minEnd(x.Upper, y.Upper)
		if upper == "" || lower < upper {
			result.ranges = append(result.ranges, Range{lower, upper})
		}
		if x.Upper == y.Upper {
			i++
			j++
		} else if y.Upper == "" || x.Upper != "" && x.Upper < y.Upper {
			i++
		} else {
			j++
		}
	}
	return result
}
