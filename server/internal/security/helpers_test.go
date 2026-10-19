package security

import (
	"strings"
	"testing"
	"time"
)

func dataRule(effect Effect, action Action, prefix string) PermissionRule {
	return PermissionRule{Effect: effect, Action: action, Resource: DataResource, Prefix: &prefix}
}

func testIdentity() Identity {
	return Identity{Kind: OIDCPrincipal, Issuer: "https://idp.example/realm", Subject: "admin"}
}

func testImage() Image {
	return Image{Version: ImageVersion, BootstrapRevision: 1,
		Issuers: []Issuer{{URL: testIdentity().Issuer, ConfigRevision: 1, Enabled: true, ClientID: "admin-client", APIAudience: "lantern", RedirectURI: "https://admin.example/auth/callback", Algorithms: []string{"RS256"}}},
		Roles: []Role{{ID: "security_admin", Rules: []PermissionRule{globalRule(Allow, SecurityManage)}},
			{ID: "reader", Rules: []PermissionRule{dataRule(Allow, VertexRead, "orders:")}}},
		Principals: []Principal{{Identity: testIdentity(), State: Active, Assignments: []RoleAssignment{{RoleID: "security_admin", EnvOwned: true}}, HumanIssuerConfigRevision: 1}}}
}

func testSession() Session {
	now := time.Date(2026, 10, 3, 1, 0, 0, 0, time.UTC)
	return Session{IssuerConfigRevision: 1, Digest: strings.Repeat("a", 64), Identity: testIdentity(), CreatedAt: now, AuthTime: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}
}

func globalRule(effect Effect, action Action) PermissionRule {
	return PermissionRule{Effect: effect, Action: action, Resource: GlobalResource}
}

// registrationImage supplies only deterministic policy data. It is not provider
// authentication evidence and never qualifies a real Google/Apple registration.
func registrationImage() Image {
	image := s1Image()
	image.Issuers = append(image.Issuers, Issuer{URL: GoogleIssuer, Enabled: true, ConfigRevision: 1, Algorithms: []string{"RS256"}, NativeClients: []NativeClient{{ID: "android", Kind: GoogleIDToken, ClientID: "fixture-web-client", ServiceOrigin: "https://lantern.example"}, {ID: "ios", Kind: GoogleIDToken, ClientID: "fixture-web-client", ServiceOrigin: "https://lantern.example"}}})
	image.Roles = append(image.Roles, PersonalUserRole())
	image.RegistrationPolicy = &RegistrationPolicy{Version: RegistrationPolicyVersion, Enabled: true, RoleID: PersonalUserRoleID, Profiles: []RegistrationProfile{{Issuer: GoogleIssuer, ClientProfileID: "android"}}}
	return image
}

func machineImageFixture(t *testing.T) (Image, string, time.Time) {
	t.Helper()
	token, err := NewMachineToken()
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := MachineTokenDigest(token)
	now := time.Date(2026, 10, 3, 1, 0, 0, 0, time.UTC)
	image := testImage()
	id := Identity{Kind: MachinePrincipal, MachineName: "worker"}
	read, deny := dataRule(Allow, VertexRead, "orders:"), dataRule(Deny, VertexRead, "orders:private:")
	read.ID, deny.ID = "read", "private"
	image.Roles[1].Rules = []PermissionRule{read, deny}
	image.Principals = append(image.Principals, Principal{Identity: id, State: Active, Assignments: []RoleAssignment{{RoleID: "reader"}}})
	image.MachineCredentials = []MachineCredential{{Identity: id, Digest: digest, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}}
	return image, token, now
}
