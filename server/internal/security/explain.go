package security

import "strings"

type RuleMatch struct {
	RoleID string
	RuleID string
	Effect Effect
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
