package security

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"testing"
	"time"
)

// Independent original input for the retained public-wire cases. This is not a
// runtime profile or an authority/clock factory; normal binaries do not link it.
func currentOriginalFixtureImage(t *testing.T, path, issuer string) Image {
	t.Helper()
	var input struct {
		Issuer                         *Issuer  `json:"issuer,omitempty"`
		AdminSubjects                  []string `json:"admin_subjects"`
		HumanSubjectNamespaceQualified bool     `json:"human_subject_namespace_qualified"`
		Roles                          []Role   `json:"roles"`
		Machines                       []struct {
			Name        string   `json:"name"`
			RoleIDs     []string `json:"role_ids"`
			Credentials []struct {
				Token     string    `json:"token"`
				CreatedAt time.Time `json:"created_at"`
				ExpiresAt time.Time `json:"expires_at"`
			} `json:"credentials"`
		} `json:"machines"`
	}
	raw, err := s3aReadProvisioned(path, MaxImageBytes)
	if err == nil {
		err = s2JSONPreflight(raw, DefaultPolicyLimits())
	}
	if err == nil {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&input)
		if err == nil {
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				err = errS3AConfig
			}
		}
	}
	if err != nil || !filepath.IsAbs(path) || len(input.AdminSubjects) == 0 || len(input.AdminSubjects) > 64 || len(input.Machines) > 64 {
		t.Fatal("bounded private original bootstrap input required", err)
	}
	defer clear(raw)
	callback := sha256.Sum256([]byte(issuer))
	image := Image{Version: ImageVersion, BootstrapRevision: 1,
		Issuers: []Issuer{{URL: issuer, ConfigRevision: 1, Enabled: true, EnvOwned: true, ClientID: "admin", APIAudience: "api", RedirectURI: fmt.Sprintf("https://admin.example/auth/callback/%x", callback), Algorithms: []string{"EdDSA"}, HumanSubjectNamespaceQualified: input.HumanSubjectNamespaceQualified}},
		Roles:   []Role{{ID: "security_admin", Name: "Security administrator", Rules: []PermissionRule{{ID: "manage", Effect: Allow, Action: SecurityManage, Resource: GlobalResource}}}},
	}
	if input.Issuer != nil {
		if input.Issuer.URL != issuer || input.Issuer.ConfigRevision != 1 || !input.Issuer.Enabled || !input.Issuer.EnvOwned {
			t.Fatal("exact independently provisioned issuer required")
		}
		image.Issuers = []Issuer{*input.Issuer}
	}
	image.Roles = append(image.Roles, input.Roles...)
	for _, subject := range input.AdminSubjects {
		image.Principals = append(image.Principals, Principal{Identity: Identity{Kind: OIDCPrincipal, Issuer: issuer, Subject: subject}, State: Active, HumanIssuerConfigRevision: 1, Assignments: []RoleAssignment{{RoleID: "security_admin", EnvOwned: true}}})
	}
	for _, machine := range input.Machines {
		identity := Identity{Kind: MachinePrincipal, MachineName: machine.Name}
		p := Principal{Identity: identity, State: Active}
		for _, role := range machine.RoleIDs {
			p.Assignments = append(p.Assignments, RoleAssignment{RoleID: role, EnvOwned: true})
		}
		image.Principals = append(image.Principals, p)
		for _, credential := range machine.Credentials {
			digest, valid := MachineTokenDigest(credential.Token)
			if !valid {
				t.Fatal("noncanonical original machine credential")
			}
			image.MachineCredentials = append(image.MachineCredentials, MachineCredential{Identity: identity, Digest: digest, CreatedAt: credential.CreatedAt, ExpiresAt: credential.ExpiresAt})
		}
	}
	if _, err := CompileImage(image, DefaultPolicyLimits()); err != nil {
		t.Fatal("invalid complete original fixture image", err)
	}
	return image
}
