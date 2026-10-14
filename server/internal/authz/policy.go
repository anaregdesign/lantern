package authz

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

var (
	ErrInvalidPolicy = errors.New("invalid Role policy")
	ErrUnknownRole   = errors.New("unknown assigned Role")
)

type prefixRules struct {
	allow    []string
	deny     []string
	uidAllow []PrincipalUIDPrefix
	uidDeny  []PrincipalUIDPrefix
}

type compiledRole struct {
	data        map[Action]prefixRules
	global      map[Action]struct{ allow, deny bool }
	id          string
	order       int
	requiresUID bool
	rules       []compiledRule
}

// CompiledPolicy owns no mutable input references. Ranges and Role tables are
// shared by all Principals observing this immutable revision.
type CompiledPolicy struct {
	roles          map[string]*compiledRole
	maxAssignments int
	scopeCache     *scopeCache
}

func CompileRoles(roles []Role, limits PolicyLimits) (*CompiledPolicy, error) {
	if err := ValidateRoleBounds(roles, limits); err != nil {
		return nil, ErrInvalidPolicy
	}
	policy := &CompiledPolicy{roles: make(map[string]*compiledRole, len(roles)),
		maxAssignments: limits.MaxAssignments, scopeCache: newScopeCache()}
	for order, role := range roles {
		if !ValidRoleID(role.ID) || role.ID == "cluster_replica" ||
			!utf8.ValidString(role.Name) || len(role.Name) > 256 || len(role.Rules) > limits.MaxRules {
			return nil, fmt.Errorf("%w: Role identity or limit", ErrInvalidPolicy)
		}
		if _, exists := policy.roles[role.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate Role", ErrInvalidPolicy)
		}
		compiled := &compiledRole{id: role.ID, order: order, data: make(map[Action]prefixRules),
			global: make(map[Action]struct{ allow, deny bool })}
		ruleIDs := make(map[string]bool, len(role.Rules))
		for _, rule := range role.Rules {
			// Do not reinterpret obsolete Edge grants or pair selectors as
			// broader Vertex authority when loading an older signed image.
			if !GrantableAction(rule.Action) || rule.Pair != nil {
				return nil, fmt.Errorf("%w: obsolete Edge grant or pair selector", ErrInvalidPolicy)
			}
			if rule.ID != "" {
				if !ValidRoleID(rule.ID) || ruleIDs[rule.ID] {
					return nil, ErrInvalidPolicy
				}
				ruleIDs[rule.ID] = true
			}
			kind, known := ActionResource(rule.Action)
			if !known || rule.Resource != kind || (rule.Effect != Allow && rule.Effect != Deny) {
				return nil, fmt.Errorf("%w: action, resource or effect", ErrInvalidPolicy)
			}
			if kind == GlobalResource {
				if rule.Prefix != nil || rule.Pair != nil || rule.PrincipalUIDPrefix != nil {
					return nil, fmt.Errorf("%w: global selector", ErrInvalidPolicy)
				}
				flags := compiled.global[rule.Action]
				if rule.Effect == Deny {
					flags.deny = true
				} else {
					flags.allow = true
				}
				compiled.global[rule.Action] = flags
				compiled.rules = append(compiled.rules, compileRule(rule))
				continue
			}
			if (rule.Prefix == nil) == (rule.PrincipalUIDPrefix == nil) {
				return nil, fmt.Errorf("%w: data prefix", ErrInvalidPolicy)
			}
			prefixes := compiled.data[rule.Action]
			if rule.PrincipalUIDPrefix != nil {
				compiled.requiresUID = true
				if rule.Effect == Deny {
					prefixes.uidDeny = append(prefixes.uidDeny, *rule.PrincipalUIDPrefix)
				} else {
					prefixes.uidAllow = append(prefixes.uidAllow, *rule.PrincipalUIDPrefix)
				}
			} else if rule.Effect == Deny {
				prefixes.deny = append(prefixes.deny, *rule.Prefix)
			} else {
				prefixes.allow = append(prefixes.allow, *rule.Prefix)
			}
			compiled.data[rule.Action] = prefixes
			compiled.rules = append(compiled.rules, compileRule(rule))
		}
		for action, prefixes := range compiled.data {
			prefixes.allow = disjointPrefixes(prefixes.allow)
			prefixes.deny = disjointPrefixes(prefixes.deny)
			compiled.data[action] = prefixes
		}
		policy.roles[role.ID] = compiled
	}
	return policy, nil
}

// Check allocation bounds before encoding any input or building a cache key.
func ValidateRoleBounds(roles []Role, limits PolicyLimits) error {
	if !limits.Valid() || len(roles) > limits.MaxRoles {
		return ErrInvalidPolicy
	}
	total := 0
	for _, role := range roles {
		if len(role.ID) > 64 || len(role.Name) > 256 || !utf8.ValidString(role.Name) || len(role.Rules) > limits.MaxRules {
			return ErrInvalidPolicy
		}
		for _, rule := range role.Rules {
			if len(rule.ID) > 64 || len(rule.Action) > 64 || len(rule.Resource) > 16 || len(rule.Effect) > 16 {
				return ErrInvalidPolicy
			}
			if rule.Pair != nil {
				for _, prefix := range []string{rule.Pair.Tail, rule.Pair.Head} {
					if !utf8.ValidString(prefix) || len(prefix) > limits.MaxPrefixBytes || len(prefix) > limits.MaxTotalBytes-total {
						return ErrInvalidPolicy
					}
					total += len(prefix)
				}
			}
			if rule.Prefix != nil {
				if !utf8.ValidString(*rule.Prefix) || len(*rule.Prefix) > limits.MaxPrefixBytes || len(*rule.Prefix) > limits.MaxTotalBytes-total {
					return ErrInvalidPolicy
				}
				total += len(*rule.Prefix)
			}
			if prefix := rule.PrincipalUIDPrefix; prefix != nil {
				if !prefix.valid(limits.MaxPrefixBytes) || prefix.expandedBytes() > limits.MaxTotalBytes-total {
					return ErrInvalidPolicy
				}
				total += prefix.expandedBytes()
			}
		}
	}
	return nil
}

// Each effect is a union of prefix intervals. Sorting and removing covered
// descendants makes a predecessor lookup sufficient, including empty prefix.
func disjointPrefixes(prefixes []string) []string {
	sort.Strings(prefixes)
	n := 0
	for _, prefix := range prefixes {
		if n != 0 && strings.HasPrefix(prefix, prefixes[n-1]) {
			continue
		}
		prefixes[n] = prefix
		n++
	}
	return prefixes[:n:n]
}

func matchesPrefix(prefixes []string, key string) bool {
	i := sort.SearchStrings(prefixes, key)
	if i < len(prefixes) && prefixes[i] == key {
		return true
	}
	return i > 0 && strings.HasPrefix(key, prefixes[i-1])
}
