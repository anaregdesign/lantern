package security

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
	allow []string
	deny  []string
}

type compiledRole struct {
	data   map[Action]prefixRules
	global map[Action]struct{ allow, deny bool }
}

// CompiledPolicy owns no mutable input references. Ranges and Role tables are
// shared by all Principals observing this immutable revision.
type CompiledPolicy struct {
	roles          map[string]*compiledRole
	maxAssignments int
}

func CompileRoles(roles []Role, limits PolicyLimits) (*CompiledPolicy, error) {
	if err := validateRoleBounds(roles, limits); err != nil {
		return nil, ErrInvalidPolicy
	}
	policy := &CompiledPolicy{roles: make(map[string]*compiledRole, len(roles)),
		maxAssignments: limits.MaxAssignments}
	totalBytes := 0
	for _, role := range roles {
		if !validRoleID(role.ID) || role.ID == "cluster_replica" ||
			!utf8.ValidString(role.Name) || len(role.Name) > 256 || len(role.Rules) > limits.MaxRules {
			return nil, fmt.Errorf("%w: Role identity or limit", ErrInvalidPolicy)
		}
		if _, exists := policy.roles[role.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate Role", ErrInvalidPolicy)
		}
		compiled := &compiledRole{data: make(map[Action]prefixRules),
			global: make(map[Action]struct{ allow, deny bool })}
		for _, rule := range role.Rules {
			kind, known := actionResource(rule.Action)
			if !known || rule.Resource != kind || (rule.Effect != Allow && rule.Effect != Deny) {
				return nil, fmt.Errorf("%w: action, resource or effect", ErrInvalidPolicy)
			}
			if kind == GlobalResource {
				if rule.Prefix != nil {
					return nil, fmt.Errorf("%w: global prefix", ErrInvalidPolicy)
				}
				flags := compiled.global[rule.Action]
				if rule.Effect == Deny {
					flags.deny = true
				} else {
					flags.allow = true
				}
				compiled.global[rule.Action] = flags
				continue
			}
			if rule.Prefix == nil || !utf8.ValidString(*rule.Prefix) || len(*rule.Prefix) > limits.MaxPrefixBytes {
				return nil, fmt.Errorf("%w: data prefix", ErrInvalidPolicy)
			}
			// Subtract before adding, so an adversarial bound cannot overflow.
			if len(*rule.Prefix) > limits.MaxTotalBytes-totalBytes {
				return nil, fmt.Errorf("%w: total prefix limit", ErrInvalidPolicy)
			}
			totalBytes += len(*rule.Prefix)
			prefixes := compiled.data[rule.Action]
			if rule.Effect == Deny {
				prefixes.deny = append(prefixes.deny, *rule.Prefix)
			} else {
				prefixes.allow = append(prefixes.allow, *rule.Prefix)
			}
			compiled.data[rule.Action] = prefixes
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
func validateRoleBounds(roles []Role, limits PolicyLimits) error {
	if !limits.valid() || len(roles) > limits.MaxRoles {
		return ErrInvalidPolicy
	}
	total := 0
	for _, role := range roles {
		if len(role.ID) > 64 || len(role.Name) > 256 || !utf8.ValidString(role.Name) || len(role.Rules) > limits.MaxRules {
			return ErrInvalidPolicy
		}
		for _, rule := range role.Rules {
			if len(rule.Action) > 64 || len(rule.Resource) > 16 || len(rule.Effect) > 16 {
				return ErrInvalidPolicy
			}
			if rule.Prefix != nil {
				if !utf8.ValidString(*rule.Prefix) || len(*rule.Prefix) > limits.MaxPrefixBytes || len(*rule.Prefix) > limits.MaxTotalBytes-total {
					return ErrInvalidPolicy
				}
				total += len(*rule.Prefix)
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
