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
	key := strings.Join(canonical, "\x00")
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
	access := &Access{roles: make([]*compiledRole, 0, len(ids)), cache: p.scopeCache, setKey: key, bindings: bindings}
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

func (p prefixRules) denies(key string, bindings SubjectBindings) bool {
	return matchesPrefix(p.deny, key) || matchesUIDPrefix(p.uidDeny, key, bindings)
}

func (p prefixRules) allows(key string, bindings SubjectBindings) bool {
	return matchesPrefix(p.allow, key) || matchesUIDPrefix(p.uidAllow, key, bindings)
}

func (a *Access) Allows(action Action, logicalKey string) bool {
	kind, known := ActionResource(action)
	if a == nil || !known || kind != DataResource || logicalKey == "" || !utf8.ValidString(logicalKey) {
		return false
	}
	allowed := false
	for _, role := range a.roles {
		prefixes := role.data[action]
		if prefixes.denies(logicalKey, a.bindings) {
			return false
		}
		allowed = allowed || prefixes.allows(logicalKey, a.bindings)
	}
	return allowed
}

func (a *Access) AllowsGlobal(action Action) bool {
	kind, known := ActionResource(action)
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

// AllowsAll proves a whole-data-domain fast path. Any applicable Deny defeats
// it, including a descendant Deny alongside an explicit empty-prefix Allow.
func (a *Access) AllowsAll(action Action) bool {
	switch action {
	case EdgeRead:
		return a.AllowsAll(VertexRead)
	case EdgeCreate, EdgeAdd, EdgeWrite, EdgeDelete:
		return a.AllowsAll(VertexRead) && a.AllowsAll(VertexWrite)
	}
	kind, known := ActionResource(action)
	if a == nil || !known || kind != DataResource {
		return false
	}
	allowed := false
	for _, role := range a.roles {
		rules := role.data[action]
		if len(rules.deny) > 0 || len(rules.uidDeny) > 0 {
			return false
		}
		allowed = allowed || len(rules.allow) > 0 && rules.allow[0] == ""
	}
	return allowed
}

// AllowsEdge derives authority from Vertex Roles. An Edge belongs to its head:
// every modification needs tail read and head write. Response disclosure and
// actual endpoint-creation effects are separate Server checks.
func (a *Access) AllowsEdge(action Action, tail, head string) bool {
	switch action {
	case EdgeRead:
		return a.Allows(VertexRead, tail) && a.Allows(VertexRead, head)
	case EdgeCreate, EdgeAdd, EdgeWrite, EdgeDelete:
		return a.Allows(VertexRead, tail) && a.Allows(VertexWrite, head)
	default:
		return false
	}
}
