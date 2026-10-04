package security

import (
	"strings"
	"unicode/utf8"
)

type RuleMatch struct {
	RoleID   string
	RuleID   string
	Effect   Effect
	Action   Action
	Endpoint string
}

// ExplainEdge records each required endpoint capability with the same
// Deny-wins result used for admission. No Role-specific pair rule exists.
func (s *Snapshot) ExplainEdge(identity Identity, action Action, tail, head string) (bool, []RuleMatch, error) {
	if !edgeSelectorAction(action) || tail == "" || head == "" || !utf8.ValidString(tail) || !utf8.ValidString(head) || len(tail) > DefaultPolicyLimits().MaxPrefixBytes || len(head) > DefaultPolicyLimits().MaxPrefixBytes {
		return false, nil, ErrInvalidPolicy
	}
	tailAction, headAction := action, action
	switch action {
	case EdgeRead:
		tailAction, headAction = VertexRead, VertexRead
	case EdgeCreate, EdgeAdd, EdgeWrite, EdgeDelete:
		tailAction, headAction = VertexRead, VertexWrite
	}
	tailAllowed, tailMatches, err := s.Explain(identity, tailAction, &tail)
	if err != nil {
		return false, nil, err
	}
	headAllowed, headMatches, err := s.Explain(identity, headAction, &head)
	if err != nil {
		return false, nil, err
	}
	for i := range tailMatches {
		tailMatches[i].Endpoint = "tail"
	}
	for i := range headMatches {
		headMatches[i].Endpoint = "head"
	}
	return tailAllowed && headAllowed, append(tailMatches, headMatches...), nil
}

// Explain returns the same Deny-wins result as compiled access plus the stable
// source Role/rule identities. Disabled or unregistered accounts never match.
func (s *Snapshot) Explain(identity Identity, action Action, key *string) (bool, []RuleMatch, error) {
	kind, known := actionResource(action)
	if !known || !grantableAction(action) || (kind == DataResource) != (key != nil) || key != nil && *key == "" {
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
				matches = append(matches, RuleMatch{RoleID: role.ID, RuleID: rule.ID, Effect: rule.Effect, Action: action})
			}
		}
	}
	if key == nil {
		return access.AllowsGlobal(action), matches, nil
	}
	return access.Allows(action, *key), matches, nil
}
