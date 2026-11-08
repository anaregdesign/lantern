package authz

// Permission is the complete grant vocabulary. Write never implies read.
type Permission string

const (
	Read  Permission = "read"
	Write Permission = "write"
)

// PermissionRule is a positive Allow on a typed resource. A data selector is
// mandatory even for the explicit all-data prefix (""); global resources have
// no selector. Historical action/effect/pair fields are not an active schema.
type PermissionRule struct {
	ID                 string              `json:"id,omitempty"`
	Permission         Permission          `json:"permission"`
	Resource           ResourceKind        `json:"resource"`
	Prefix             *string             `json:"prefix,omitempty"`
	PrincipalUIDPrefix *PrincipalUIDPrefix `json:"principal_uid_prefix,omitempty"`
}

type Role struct {
	ID    string           `json:"id"`
	Name  string           `json:"name"`
	Rules []PermissionRule `json:"rules"`
}

func ValidRoleID(id string) bool {
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
