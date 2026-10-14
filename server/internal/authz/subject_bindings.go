package authz

import (
	"crypto/sha256"
	"strings"
	"unicode/utf8"
)

// SubjectBindings supplies immutable subject facts to the pure evaluator. This
// value is not an authorization capability: production callers obtain its UID
// only from the installed security Principal, never from a request or claim.
type SubjectBindings struct {
	PersonalUID string
}

// ValidPersonalUID accepts the canonical, nonzero 128-bit Server UID. Allocation
// and uniqueness belong to security; the evaluator only validates its syntax.
func ValidPersonalUID(uid string) bool {
	if len(uid) != 32 {
		return false
	}
	nonzero := false
	for i := range len(uid) {
		c := uid[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
		nonzero = nonzero || c != '0'
	}
	return nonzero
}

func (b SubjectBindings) valid() bool {
	return b.PersonalUID == "" || ValidPersonalUID(b.PersonalUID)
}

// Digest gives bound Access/cache/cursor consumers one canonical subject
// binding. The zero digest denotes the absent binding used by legacy Roles.
func (b SubjectBindings) Digest() [32]byte {
	if b.PersonalUID == "" {
		return [32]byte{}
	}
	return sha256.Sum256([]byte("lantern/authz/principal-uid/v1\x00" + b.PersonalUID))
}

// PrincipalUIDPrefix is a typed single slot between two byte-literal parts.
// It never interprets template syntax, wildcard text or arbitrary claims.
type PrincipalUIDPrefix struct {
	LiteralBefore string `json:"literal_before"`
	LiteralAfter  string `json:"literal_after"`
}

func (p PrincipalUIDPrefix) valid(maxBytes int) bool {
	// Check each length before addition, including the fixed UID width.
	return maxBytes >= 34 && len(p.LiteralBefore) <= maxBytes-33 &&
		len(p.LiteralAfter) <= maxBytes-32-len(p.LiteralBefore) &&
		strings.HasSuffix(p.LiteralBefore, ":") && strings.HasPrefix(p.LiteralAfter, ":") &&
		utf8.ValidString(p.LiteralBefore) && utf8.ValidString(p.LiteralAfter)
}

func (p PrincipalUIDPrefix) expandedBytes() int {
	return len(p.LiteralBefore) + 32 + len(p.LiteralAfter)
}

func (p PrincipalUIDPrefix) bind(b SubjectBindings) string {
	return p.LiteralBefore + b.PersonalUID + p.LiteralAfter
}

// matches uses the same concatenation as bind without allocating on data RPCs.
// Only compiled selectors and validated nonempty bindings reach this method.
func (p PrincipalUIDPrefix) matches(key string, b SubjectBindings) bool {
	rest, ok := strings.CutPrefix(key, p.LiteralBefore)
	if !ok {
		return false
	}
	rest, ok = strings.CutPrefix(rest, b.PersonalUID)
	return ok && strings.HasPrefix(rest, p.LiteralAfter)
}

// PersonalPrefix returns the initial registration area, not every prefix a
// future operator-assigned UID rule might authorize.
func PersonalPrefix(uid string) (string, error) {
	if !ValidPersonalUID(uid) {
		return "", ErrInvalidPolicy
	}
	return (PrincipalUIDPrefix{LiteralBefore: "users:", LiteralAfter: ":"}).bind(SubjectBindings{PersonalUID: uid}), nil
}
