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
	uidAllow []PrincipalUIDPrefix
}

type compiledRole struct {
	data        map[Permission]prefixRules
	global      map[ResourceKind]map[Permission]bool
	id          string
	order       int
	requiresUID bool
	rules       []compiledRule
}

// CompiledPolicy owns no mutable input references. Ranges and Role tables are
// shared by all Principals observing this immutable revision.
type CompiledPolicy struct {
	edgeMode       EdgeEndpointMode
	roles          map[string]*compiledRole
	maxAssignments int
	scopeCache     *scopeCache
}

func CompileRoles(roles []Role, limits PolicyLimits) (*CompiledPolicy, error) {
	return CompileRolesWithEdgeMode(roles, limits, EdgeReadRead)
}

func CompileRolesWithEdgeMode(roles []Role, limits PolicyLimits, mode EdgeEndpointMode) (*CompiledPolicy, error) {
	if !mode.Valid() {
		return nil, ErrInvalidPolicy
	}
	if err := ValidateRoleBounds(roles, limits); err != nil {
		return nil, ErrInvalidPolicy
	}
	policy := &CompiledPolicy{edgeMode: mode, roles: make(map[string]*compiledRole, len(roles)),
		maxAssignments: limits.MaxAssignments, scopeCache: newScopeCache()}
	for order, role := range roles {
		if !ValidRoleID(role.ID) || role.ID == "cluster_replica" ||
			!utf8.ValidString(role.Name) || len(role.Name) > 256 || len(role.Rules) > limits.MaxRules {
			return nil, fmt.Errorf("%w: Role identity or limit", ErrInvalidPolicy)
		}
		if _, exists := policy.roles[role.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate Role", ErrInvalidPolicy)
		}
		compiled := &compiledRole{id: role.ID, order: order, data: make(map[Permission]prefixRules),
			global: make(map[ResourceKind]map[Permission]bool)}
		ruleIDs := make(map[string]bool, len(role.Rules))
		for _, rule := range role.Rules {
			if rule.ID != "" {
				if !ValidRoleID(rule.ID) || ruleIDs[rule.ID] {
					return nil, ErrInvalidPolicy
				}
				ruleIDs[rule.ID] = true
			}
			if !ValidPermission(rule.Resource, rule.Permission) {
				return nil, fmt.Errorf("%w: resource or permission", ErrInvalidPolicy)
			}
			if rule.Resource != DataResource {
				if rule.Prefix != nil || rule.PrincipalUIDPrefix != nil {
					return nil, fmt.Errorf("%w: nondata selector", ErrInvalidPolicy)
				}
				if compiled.global[rule.Resource] == nil {
					compiled.global[rule.Resource] = make(map[Permission]bool)
				}
				compiled.global[rule.Resource][rule.Permission] = true
			} else {
				if (rule.Prefix == nil) == (rule.PrincipalUIDPrefix == nil) {
					return nil, fmt.Errorf("%w: data prefix", ErrInvalidPolicy)
				}
				prefixes := compiled.data[rule.Permission]
				if rule.PrincipalUIDPrefix != nil {
					compiled.requiresUID = true
					prefixes.uidAllow = append(prefixes.uidAllow, *rule.PrincipalUIDPrefix)
				} else {
					prefixes.allow = append(prefixes.allow, *rule.Prefix)
				}
				compiled.data[rule.Permission] = prefixes
			}
			compiled.rules = append(compiled.rules, compileRule(rule))
		}
		for action, prefixes := range compiled.data {
			prefixes.allow = disjointPrefixes(prefixes.allow)
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
			if len(rule.ID) > 64 || len(rule.Permission) > 16 || len(rule.Resource) > 16 {
				return ErrInvalidPolicy
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

// Positive rules form a union of prefix intervals. Sorting and removing covered
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
