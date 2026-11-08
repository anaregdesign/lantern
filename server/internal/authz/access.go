package authz

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// Access captures one Principal's explicit Role membership. It is immutable
// and safe to share for a complete batch; it performs no I/O or graph lookup.
type Access struct {
	edgeMode EdgeEndpointMode
	roles    []*compiledRole
	cache    *scopeCache
	setKey   string
	bindings SubjectBindings
}

// ForRoles retains the literal/global-only API. UID selectors require BindRoles.
func (p *CompiledPolicy) ForRoles(ids []string) (*Access, error) {
	return p.BindRoles(ids, SubjectBindings{})
}

// AccessKey validates membership and subject bindings before allocating the
// bounded reuse key. Installed security snapshots may reuse a view only for this
// exact key and the same compiled policy. It carries no serving authority.
func (p *CompiledPolicy) AccessKey(ids []string, bindings SubjectBindings) (string, error) {
	if p == nil || len(ids) > p.maxAssignments || !bindings.valid() {
		return "", ErrInvalidPolicy
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !ValidRoleID(id) {
			return "", ErrInvalidPolicy
		}
		role, known := p.roles[id]
		if !known {
			return "", ErrUnknownRole
		}
		if seen[id] || role.requiresUID && bindings.PersonalUID == "" {
			return "", fmt.Errorf("%w: duplicate assignment or missing UID", ErrInvalidPolicy)
		}
		seen[id] = true
	}
	canonical := append([]string(nil), ids...)
	sort.Strings(canonical)
	key := string(p.edgeMode) + "\x00" + strings.Join(canonical, "\x00")
	if bindings.PersonalUID != "" {
		digest := bindings.Digest()
		key += "\x00\x00" + hex.EncodeToString(digest[:])
	}
	return key, nil
}

// BindRoles retains shared compiled Role structure and only a lightweight
// immutable subject binding. It creates no per-user copy of the policy rules.
func (p *CompiledPolicy) BindRoles(ids []string, bindings SubjectBindings) (*Access, error) {
	key, err := p.AccessKey(ids, bindings)
	if err != nil {
		return nil, err
	}
	access := &Access{edgeMode: p.edgeMode, roles: make([]*compiledRole, 0, len(ids)), cache: p.scopeCache, setKey: key, bindings: bindings}
	for _, id := range ids {
		access.roles = append(access.roles, p.roles[id])
	}
	// Explain retains canonical policy source order, independent of membership
	// input order or access-set reuse across snapshots.
	sort.Slice(access.roles, func(i, j int) bool { return access.roles[i].order < access.roles[j].order })
	return access, nil
}

func (a *Access) BindingDigest() [32]byte {
	if a == nil {
		return [32]byte{}
	}
	return a.bindings.Digest()
}

func matchesUIDPrefix(prefixes []PrincipalUIDPrefix, key string, bindings SubjectBindings) bool {
	for _, prefix := range prefixes {
		if prefix.matches(key, bindings) {
			return true
		}
	}
	return false
}

func (p prefixRules) allows(key string, bindings SubjectBindings) bool {
	return matchesPrefix(p.allow, key) || matchesUIDPrefix(p.uidAllow, key, bindings)
}

func (a *Access) Allows(action Action, logicalKey string) bool {
	resource, known := ActionResource(action)
	permission, mapped := OperationPermission(action)
	return known && mapped && resource == DataResource && a.AllowsData(permission, logicalKey)
}

func (a *Access) AllowsData(permission Permission, logicalKey string) bool {
	if a == nil || !ValidPermission(DataResource, permission) || logicalKey == "" || !utf8.ValidString(logicalKey) {
		return false
	}
	for _, role := range a.roles {
		if role.data[permission].allows(logicalKey, a.bindings) {
			return true
		}
	}
	return false
}

// AllowsResource checks only a selector-free typed management resource.
func (a *Access) AllowsResource(resource ResourceKind, permission Permission) bool {
	if a == nil || resource == DataResource || !ValidPermission(resource, permission) {
		return false
	}
	for _, role := range a.roles {
		if role.global[resource][permission] {
			return true
		}
	}
	return false
}

func (a *Access) AllowsGlobal(action Action) bool {
	resource, known := ActionResource(action)
	permission, mapped := OperationPermission(action)
	return known && mapped && a.AllowsResource(resource, permission)
}

// AllowsAll proves the explicit all-data positive scope.
func (a *Access) AllowsAll(action Action) bool {
	switch action {
	case EdgeRead:
		return a.AllowsAll(VertexRead)
	case EdgeCreate, EdgeAdd, EdgeWrite, EdgeDelete:
		tail, head := a.EdgeEndpointActions()
		return a.AllowsAll(tail) && a.AllowsAll(head)
	}
	kind, known := ActionResource(action)
	if a == nil || !known || kind != DataResource {
		return false
	}
	allowed := false
	for _, role := range a.roles {
		permission, ok := OperationPermission(action)
		if !ok {
			return false
		}
		rules := role.data[permission]
		allowed = allowed || len(rules.allow) > 0 && rules.allow[0] == ""
	}
	return allowed
}

// AllowsEdge derives modifications from the one captured endpoint policy.
// Reading always needs both endpoint reads. Response disclosure and
// actual endpoint-creation effects are separate Server checks.
func (a *Access) AllowsEdge(action Action, tail, head string) bool {
	switch action {
	case EdgeRead:
		return a.Allows(VertexRead, tail) && a.Allows(VertexRead, head)
	case EdgeCreate, EdgeAdd, EdgeWrite, EdgeDelete:
		tailAction, headAction := a.EdgeEndpointActions()
		return a.Allows(tailAction, tail) && a.Allows(headAction, head)
	default:
		return false
	}
}
