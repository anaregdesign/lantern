package authz

import (
	"strings"
	"unicode/utf8"
)

// compiledRule retains stable source identities for explanations without reading
// durable state or retaining mutable policy input.
type compiledRule struct {
	id      string
	effect  Effect
	action  Action
	literal string
	uid     *PrincipalUIDPrefix
}

func compileRule(rule PermissionRule) compiledRule {
	compiled := compiledRule{id: rule.ID, effect: rule.Effect, action: rule.Action}
	if rule.Prefix != nil {
		compiled.literal = *rule.Prefix
	}
	if rule.PrincipalUIDPrefix != nil {
		prefix := *rule.PrincipalUIDPrefix
		compiled.uid = &prefix
	}
	return compiled
}

func (r compiledRule) matches(key string, bindings SubjectBindings) bool {
	if r.uid != nil {
		return r.uid.matches(key, bindings)
	}
	return strings.HasPrefix(key, r.literal)
}

type RuleMatch struct {
	RoleID   string
	RuleID   string
	Effect   Effect
	Action   Action
	Endpoint string
}

// ExplainEdge records each required endpoint capability with the same
// Deny-wins result used for admission. No Role-specific pair rule exists.
func (a *Access) ExplainEdge(action Action, tail, head string) (bool, []RuleMatch, error) {
	if !EdgeSelectorAction(action) || tail == "" || head == "" || !utf8.ValidString(tail) || !utf8.ValidString(head) || len(tail) > DefaultPolicyLimits().MaxPrefixBytes || len(head) > DefaultPolicyLimits().MaxPrefixBytes {
		return false, nil, ErrInvalidPolicy
	}
	tailAction, headAction := action, action
	switch action {
	case EdgeRead:
		tailAction, headAction = VertexRead, VertexRead
	case EdgeCreate, EdgeAdd, EdgeWrite, EdgeDelete:
		tailAction, headAction = VertexRead, VertexWrite
	}
	tailAllowed, tailMatches, err := a.Explain(tailAction, &tail)
	if err != nil {
		return false, nil, err
	}
	headAllowed, headMatches, err := a.Explain(headAction, &head)
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
// source Role/rule identities. The caller supplies only a validated immutable binding.
func (a *Access) Explain(action Action, key *string) (bool, []RuleMatch, error) {
	kind, known := ActionResource(action)
	if !known || !GrantableAction(action) || (kind == DataResource) != (key != nil) || key != nil && (*key == "" || !utf8.ValidString(*key)) {
		return false, nil, ErrInvalidPolicy
	}
	var matches []RuleMatch
	if a == nil {
		return false, nil, nil
	}
	for _, role := range a.roles {
		for _, rule := range role.rules {
			if rule.action == action && (key == nil || rule.matches(*key, a.bindings)) {
				matches = append(matches, RuleMatch{RoleID: role.id, RuleID: rule.id, Effect: rule.effect, Action: action})
			}
		}
	}
	if key == nil {
		return a.AllowsGlobal(action), matches, nil
	}
	return a.Allows(action, *key), matches, nil
}
