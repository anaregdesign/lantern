package authz

import (
	"strings"
	"unicode/utf8"
)

// compiledRule retains stable source identities for explanations without reading
// durable state or retaining mutable policy input.
type compiledRule struct {
	id         string
	permission Permission
	resource   ResourceKind
	literal    string
	uid        *PrincipalUIDPrefix
}

func compileRule(rule PermissionRule) compiledRule {
	compiled := compiledRule{id: rule.ID, permission: rule.Permission, resource: rule.Resource}
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
	RoleID     string
	RuleID     string
	Permission Permission
	Resource   ResourceKind
	Endpoint   string
}

// ExplainEdge records each required endpoint capability with the same
// Allow-union result used for admission. No Role-specific pair rule exists.
func (a *Access) ExplainEdge(action Action, tail, head string) (bool, []RuleMatch, error) {
	if !EdgeSelectorAction(action) || tail == "" || head == "" || !utf8.ValidString(tail) || !utf8.ValidString(head) || len(tail) > DefaultPolicyLimits().MaxPrefixBytes || len(head) > DefaultPolicyLimits().MaxPrefixBytes {
		return false, nil, ErrInvalidPolicy
	}
	tailAction, headAction := action, action
	switch action {
	case EdgeRead:
		tailAction, headAction = VertexRead, VertexRead
	case EdgeCreate, EdgeAdd, EdgeWrite, EdgeDelete:
		tailAction, headAction = a.EdgeEndpointActions()
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

// Explain uses the central operation mapping and the same bound access as admission.
func (a *Access) Explain(action Action, key *string) (bool, []RuleMatch, error) {
	resource, known := ActionResource(action)
	permission, mapped := OperationPermission(action)
	if !known || !mapped {
		return false, nil, ErrInvalidPolicy
	}
	return a.ExplainResource(resource, permission, key)
}

func (a *Access) ExplainResource(resource ResourceKind, permission Permission, key *string) (bool, []RuleMatch, error) {
	if !ValidPermission(resource, permission) || (resource == DataResource) != (key != nil) || key != nil && (*key == "" || !utf8.ValidString(*key)) {
		return false, nil, ErrInvalidPolicy
	}
	var matches []RuleMatch
	if a == nil {
		return false, nil, nil
	}
	for _, role := range a.roles {
		for _, rule := range role.rules {
			if rule.permission == permission && rule.resource == resource && (key == nil || rule.matches(*key, a.bindings)) {
				matches = append(matches, RuleMatch{RoleID: role.id, RuleID: rule.id, Permission: permission, Resource: resource})
			}
		}
	}
	if key == nil {
		return a.AllowsResource(resource, permission), matches, nil
	}
	return a.AllowsData(permission, *key), matches, nil
}
