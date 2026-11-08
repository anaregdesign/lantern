package securitytransport

import "github.com/anaregdesign/lantern/server/internal/security"

func securityImageFixture() security.Image {
	issuer := "https://idp.example"
	return security.Image{Version: security.ImageVersion, BootstrapRevision: 1, Issuers: []security.Issuer{{URL: issuer, Enabled: true, ConfigRevision: 1, ClientID: "admin", APIAudience: "api", RedirectURI: "https://admin.example/auth/callback", Algorithms: []string{"EdDSA"}}}, Roles: []security.Role{{ID: "security_admin", Rules: []security.PermissionRule{{ID: "security-read", Permission: security.Read, Resource: security.SecurityResource}, {ID: "security-write", Permission: security.Write, Resource: security.SecurityResource}, {ID: "settings-read", Permission: security.Read, Resource: security.SettingsResource}, {ID: "settings-write", Permission: security.Write, Resource: security.SettingsResource}}}}, Principals: []security.Principal{{Identity: security.Identity{Kind: security.OIDCPrincipal, Issuer: issuer, Subject: "admin"}, State: security.Active, HumanIssuerConfigRevision: 1, Assignments: []security.RoleAssignment{{RoleID: "security_admin", EnvOwned: true}}}, {Identity: security.Identity{Kind: security.OIDCPrincipal, Issuer: issuer, Subject: "reader"}, State: security.Active}}}
}
