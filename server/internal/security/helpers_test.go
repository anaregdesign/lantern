package security

import (
	"strings"
	"time"
	"unicode/utf8"
)

func dataRule(effect Effect, action Action, prefix string) PermissionRule {
	return PermissionRule{Effect: effect, Action: action, Resource: DataResource, Prefix: &prefix}
}

func testIdentity() Identity {
	return Identity{Kind: OIDCPrincipal, Issuer: "https://idp.example/realm", Subject: "admin"}
}

func testImage() Image {
	return Image{Version: ImageVersion, BootstrapRevision: 1,
		Issuers: []Issuer{{URL: testIdentity().Issuer, Enabled: true, ClientID: "admin-client", APIAudience: "lantern", RedirectURI: "https://admin.example/auth/callback", Algorithms: []string{"RS256"}}},
		Roles: []Role{{ID: "security_admin", Rules: []PermissionRule{globalRule(Allow, SecurityManage)}},
			{ID: "reader", Rules: []PermissionRule{dataRule(Allow, VertexRead, "orders:")}}},
		Principals: []Principal{{Identity: testIdentity(), State: Active, Assignments: []RoleAssignment{{RoleID: "security_admin", EnvOwned: true}}}}}
}

func testSession() Session {
	now := time.Date(2026, 10, 3, 1, 0, 0, 0, time.UTC)
	return Session{Digest: strings.Repeat("a", 64), Identity: testIdentity(), CreatedAt: now, AuthTime: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}
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
