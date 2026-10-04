package service

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/security"
)

type dataAccessCommitter struct{}

func (dataAccessCommitter) CommitRevision(context.Context, *security.Revision) error { return nil }

func dataAccessRule(id string, effect security.Effect, action security.Action, prefix string) security.PermissionRule {
	return security.PermissionRule{ID: id, Effect: effect, Action: action, Resource: security.DataResource, Prefix: &prefix}
}

// Data-boundary tests exercise the policy engine directly, independently of
// management DTO/HTTP fixtures. They share one immutable admitted revision.
func dataAccessFixture(t *testing.T, rules []security.PermissionRule) (func() time.Time, func(string, time.Time) context.Context) {
	t.Helper()
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize))
	store, err := security.NewStore(security.StoreOptions{Generation: [16]byte{1}, PublicKey: key.Public().(ed25519.PublicKey), PrivateKey: key, Committer: dataAccessCommitter{}, Limits: security.DefaultPolicyLimits()})
	if err != nil {
		t.Fatal(err)
	}
	identity := func(subject string) security.Identity {
		return security.Identity{Kind: security.OIDCPrincipal, Issuer: "https://idp.example", Subject: subject}
	}
	image := security.Image{Version: security.ImageVersion, BootstrapRevision: 1,
		Issuers:    []security.Issuer{{URL: "https://idp.example", Enabled: true, ConfigRevision: 1, ClientID: "admin", APIAudience: "api", RedirectURI: "https://admin.example/auth/callback", Algorithms: []string{"EdDSA"}}},
		Roles:      []security.Role{{ID: "security_admin", Rules: []security.PermissionRule{{ID: "manage", Effect: security.Allow, Action: security.SecurityManage, Resource: security.GlobalResource}}}, {ID: "data", Rules: rules}},
		Principals: []security.Principal{{Identity: identity("admin"), State: security.Active, Assignments: []security.RoleAssignment{{RoleID: "security_admin", EnvOwned: true}}}, {Identity: identity("reader"), State: security.Active, Assignments: []security.RoleAssignment{{RoleID: "data"}}}}}
	if _, err := store.ReconcileBootstrap(t.Context(), 0, [16]byte{1}, image); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	clock := func() time.Time { return now }
	contexts := func(subject string, authTime time.Time) context.Context {
		revision, _ := store.Current()
		admission, err := security.NewAdmission(identity(subject), authTime, now.Add(time.Hour), revision, func(ctx context.Context, cut *security.Revision) error {
			if err := ctx.Err(); err != nil {
				return err
			}
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
	return clock, contexts
}
