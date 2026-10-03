package security

type Effect string

const (
	Allow Effect = "allow"
	Deny  Effect = "deny"
)

// PermissionRule belongs exclusively to a Role. A pointer distinguishes an
// explicit all-data prefix ("") from an omitted prefix on a global rule.
type PermissionRule struct {
	ID       string       `json:"id,omitempty"`
	Effect   Effect       `json:"effect"`
	Action   Action       `json:"action"`
	Resource ResourceKind `json:"resource"`
	Prefix   *string      `json:"prefix,omitempty"`
}

type Role struct {
	ID    string           `json:"id"`
	Name  string           `json:"name"`
	Rules []PermissionRule `json:"rules"`
}

func validRoleID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, char := range id {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9', char == '_', char == '-':
		default:
			return false
		}
	}
	return true
}
