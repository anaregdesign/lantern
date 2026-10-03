package security

import (
	"fmt"
	"unicode/utf8"
)

// Access captures one Principal's explicit Role membership. It is immutable
// and safe to share for a complete batch; it performs no I/O or graph lookup.
type Access struct {
	roles []*compiledRole
}

func (p *CompiledPolicy) ForRoles(ids []string) (*Access, error) {
	if p == nil || len(ids) > p.maxAssignments {
		return nil, ErrInvalidPolicy
	}
	access := &Access{roles: make([]*compiledRole, 0, len(ids))}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		role, known := p.roles[id]
		if !known {
			return nil, ErrUnknownRole
		}
		if seen[id] {
			return nil, fmt.Errorf("%w: duplicate assignment", ErrInvalidPolicy)
		}
		seen[id] = true
		access.roles = append(access.roles, role)
	}
	return access, nil
}

func (a *Access) Allows(action Action, logicalKey string) bool {
	kind, known := actionResource(action)
	if a == nil || !known || kind != DataResource || logicalKey == "" || !utf8.ValidString(logicalKey) {
		return false
	}
	allowed := false
	for _, role := range a.roles {
		prefixes := role.data[action]
		if matchesPrefix(prefixes.deny, logicalKey) {
			return false
		}
		allowed = allowed || matchesPrefix(prefixes.allow, logicalKey)
	}
	return allowed
}

func (a *Access) AllowsGlobal(action Action) bool {
	kind, known := actionResource(action)
	if a == nil || !known || kind != GlobalResource {
		return false
	}
	allowed := false
	for _, role := range a.roles {
		flags := role.global[action]
		if flags.deny {
			return false
		}
		allowed = allowed || flags.allow
	}
	return allowed
}

// AllowsEdge includes both endpoint reads and, for Add/Put, creation rights.
// Lifecycle reduction additionally needs EdgeDelete at the mutation preflight.
func (a *Access) AllowsEdge(action Action, tail, head string) bool {
	if action != EdgeRead && action != EdgeAdd && action != EdgeWrite && action != EdgeDelete {
		return false
	}
	if !a.Allows(action, tail) || !a.Allows(action, head) ||
		!a.Allows(VertexRead, tail) || !a.Allows(VertexRead, head) {
		return false
	}
	if action == EdgeAdd || action == EdgeWrite {
		return a.Allows(EdgeRead, tail) && a.Allows(EdgeRead, head) &&
			a.Allows(VertexWrite, tail) && a.Allows(VertexWrite, head)
	}
	if action == EdgeDelete {
		return a.Allows(EdgeRead, tail) && a.Allows(EdgeRead, head)
	}
	return true
}
