package security

import "github.com/anaregdesign/lantern/server/internal/authz"

type RuleMatch = authz.RuleMatch

// Explain delegates pure matching to the same bound Access used for admission.
// Disabled and unknown Principals never acquire a view through this read API.
func (s *Snapshot) Explain(identity Identity, action Action, key *string) (bool, []RuleMatch, error) {
	access, _ := s.AccessFor(identity)
	return access.Explain(action, key)
}

func (s *Snapshot) ExplainEdge(identity Identity, action Action, tail, head string) (bool, []RuleMatch, error) {
	access, _ := s.AccessFor(identity)
	return access.ExplainEdge(action, tail, head)
}
