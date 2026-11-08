package authz

import (
	"strings"
	"unicode/utf8"
)

const testUIDA = "11111111111111111111111111111111"
const testUIDB = "22222222222222222222222222222222"

func uidRule(permission Permission, before, after string) PermissionRule {
	return PermissionRule{Permission: permission, Resource: DataResource, PrincipalUIDPrefix: &PrincipalUIDPrefix{LiteralBefore: before, LiteralAfter: after}}
}
func dataRule(permission Permission, prefix string) PermissionRule {
	return PermissionRule{Permission: permission, Resource: DataResource, Prefix: &prefix}
}
func globalRule(resource ResourceKind, permission Permission) PermissionRule {
	return PermissionRule{Permission: permission, Resource: resource}
}
func referenceAllows(roles []Role, action Action, key string) bool {
	if key == "" || !utf8.ValidString(key) {
		return false
	}
	permission, ok := OperationPermission(action)
	if !ok {
		return false
	}
	for _, role := range roles {
		for _, rule := range role.Rules {
			if rule.Permission == permission && rule.Resource == DataResource && rule.Prefix != nil && strings.HasPrefix(key, *rule.Prefix) {
				return true
			}
		}
	}
	return false
}
