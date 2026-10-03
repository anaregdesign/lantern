package security

import (
	"strings"
	"unicode/utf8"
)

type RuleMatch struct {
	RoleID string
	RuleID string
	Effect Effect
}

// ExplainEdge explains one action's complete directed selector. Endpoint and
// other action dependencies belong to the public operation, not this check.
func (s *Snapshot) ExplainEdge(identity Identity, action Action, tail, head string) (bool, []RuleMatch, error) {
	if !edgeSelectorAction(action) || tail == "" || head == "" || !utf8.ValidString(tail) || !utf8.ValidString(head) || len(tail) > DefaultPolicyLimits().MaxPrefixBytes || len(head) > DefaultPolicyLimits().MaxPrefixBytes {
		return false, nil, ErrInvalidPolicy
	}
	access, active := s.AccessFor(identity)
	if !active {
		return false, nil, nil
	}
	image := s.Image()
	assigned := make(map[string]bool, len(access.roles))
	for _, principal := range image.Principals {
		if principal.Identity == identity {
			for _, assignment := range principal.Assignments {
				assigned[assignment.RoleID] = true
			}
		}
	}
	var matches []RuleMatch
	for _, role := range image.Roles {
		if !assigned[role.ID] {
			continue
		}
		for _, rule := range role.Rules {
			if rule.Action != action || rule.Resource != DataResource {
				continue
			}
			prefixMatch := rule.Prefix != nil && (strings.HasPrefix(tail, *rule.Prefix) || strings.HasPrefix(head, *rule.Prefix))
			pairMatch := rule.Pair != nil && strings.HasPrefix(tail, rule.Pair.Tail) && strings.HasPrefix(head, rule.Pair.Head)
			if prefixMatch || pairMatch {
				matches = append(matches, RuleMatch{RoleID: role.ID, RuleID: rule.ID, Effect: rule.Effect})
			}
		}
	}
	return access.AllowsEdgeAction(action, tail, head), matches, nil
}

// Explain returns the same Deny-wins result as compiled access plus the stable
// source Role/rule identities. Disabled or unregistered accounts never match.
func (s *Snapshot) Explain(identity Identity, action Action, key *string) (bool, []RuleMatch, error) {
	kind, known := actionResource(action)
	if !known || (kind == DataResource) != (key != nil) || key != nil && *key == "" {
		return false, nil, ErrInvalidPolicy
	}
	access, active := s.AccessFor(identity)
	if !active {
		return false, nil, nil
	}
	image := s.Image()
	assigned := make(map[string]bool)
	for _, principal := range image.Principals {
		if principal.Identity == identity {
			for _, assignment := range principal.Assignments {
				assigned[assignment.RoleID] = true
			}
		}
	}
	var matches []RuleMatch
	for _, role := range image.Roles {
		if !assigned[role.ID] {
			continue
		}
		for _, rule := range role.Rules {
			if rule.Action == action && rule.Resource == kind &&
				(key == nil || rule.Prefix != nil && strings.HasPrefix(*key, *rule.Prefix)) {
				matches = append(matches, RuleMatch{RoleID: role.ID, RuleID: rule.ID, Effect: rule.Effect})
			}
		}
	}
	if key == nil {
		return access.AllowsGlobal(action), matches, nil
	}
	return access.Allows(action, *key), matches, nil
}
