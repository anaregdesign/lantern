package authz

import (
	"strings"
	"unicode/utf8"
)

const testUIDA = "11111111111111111111111111111111"
const testUIDB = "22222222222222222222222222222222"

func uidRule(effect Effect, action Action, before, after string) PermissionRule {
	return PermissionRule{Effect: effect, Action: action, Resource: DataResource,
		PrincipalUIDPrefix: &PrincipalUIDPrefix{LiteralBefore: before, LiteralAfter: after}}
}

func dataRule(effect Effect, action Action, prefix string) PermissionRule {
	return PermissionRule{Effect: effect, Action: action, Resource: DataResource, Prefix: &prefix}
}
func globalRule(effect Effect, action Action) PermissionRule {
	return PermissionRule{Effect: effect, Action: action, Resource: GlobalResource}
}

func referenceAllows(roles []Role, action Action, key string) bool {
	if key == "" || !utf8.ValidString(key) {
		return false
	}
	allow := false
	for _, role := range roles {
		for _, rule := range role.Rules {
			if rule.Action == action && rule.Resource == DataResource && strings.HasPrefix(key, *rule.Prefix) {
				if rule.Effect == Deny {
					return false
				}
				allow = true
			}
		}
	}
	return allow
}
