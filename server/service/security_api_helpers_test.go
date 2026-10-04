package service

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"github.com/anaregdesign/lantern/server/internal/security"
	"testing"
	"time"
)

type securityAPICommitter struct{ calls int }

func (c *securityAPICommitter) CommitRevision(context.Context, *security.Revision) error {
	c.calls++
	return nil
}
func securityAPIFixture(t *testing.T) (*SecurityConnectHandler, *securityAPICommitter, func(string, time.Time) context.Context) {
	t.Helper()
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize))
	sink := &securityAPICommitter{}
	store, err := security.NewStore(security.StoreOptions{Generation: [16]byte{1}, PublicKey: key.Public().(ed25519.PublicKey), PrivateKey: key, Committer: sink, Limits: security.DefaultPolicyLimits()})
	if err != nil {
		t.Fatal(err)
	}
	issuer := "https://idp.example"
	identity := func(subject string) security.Identity {
		return security.Identity{Kind: security.OIDCPrincipal, Issuer: issuer, Subject: subject}
	}
	image := security.Image{Version: security.ImageVersion, BootstrapRevision: 1,
		Issuers:    []security.Issuer{{URL: issuer, Enabled: true, ConfigRevision: 1, ClientID: "admin", APIAudience: "api", RedirectURI: "https://admin.example/auth/callback", Algorithms: []string{"EdDSA"}, SecretRef: "hidden"}},
		Roles:      []security.Role{{ID: "security_admin", Rules: []security.PermissionRule{{ID: "manage", Effect: security.Allow, Action: security.SecurityManage, Resource: security.GlobalResource}}}},
		Principals: []security.Principal{{Identity: identity("admin"), State: security.Active, Assignments: []security.RoleAssignment{{RoleID: "security_admin", EnvOwned: true}}}, {Identity: identity("other_admin"), State: security.Active, Assignments: []security.RoleAssignment{{RoleID: "security_admin"}}}, {Identity: identity("reader"), State: security.Active}}}
	if _, err = store.ReconcileBootstrap(t.Context(), 0, [16]byte{1}, image); err != nil {
		t.Fatal(err)
	}
	handler, err := NewSecurityConnectHandler(SecurityServiceOptions{Store: store, ValidateIssuer: func(context.Context, security.Issuer) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	handler.now = func() time.Time { return now }
	contexts := func(subject string, authTime time.Time) context.Context {
		revision, ok := store.Current()
		if !ok {
			t.Fatal("store unavailable")
		}
		admission, err := security.NewAdmission(identity(subject), authTime, now.Add(time.Hour), revision, func(_ context.Context, cut *security.Revision) error {
			current, ok := store.Current()
			if !ok || current.Digest() != cut.Digest() {
				return security.ErrAuthorityUnavailable
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return security.WithAdmission(t.Context(), admission)
	}
	return handler, sink, contexts
}
