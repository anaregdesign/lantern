package security

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func nativeTestOptions(t *testing.T, path string) NativeStoreOptions {
	t.Helper()
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize))
	return NativeStoreOptions{Path: path, Generation: [16]byte{1}, PublicKey: privateKey.Public().(ed25519.PublicKey),
		PrivateKey: privateKey, Limits: DefaultPolicyLimits(), Graph: graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)}
}

func nativeTestSuspendedImage() Image {
	image := testImage()
	image.Principals = append(image.Principals, Principal{Identity: Identity{Kind: MachinePrincipal, MachineName: "former_reader"},
		State: Suspended, Assignments: []RoleAssignment{{RoleID: "reader"}}})
	return image
}

func cleanupNativeStore(t *testing.T, native *NativeStore) {
	t.Helper()
	t.Cleanup(func() {
		if err := native.Close(); err != nil {
			t.Errorf("close native Store: %v", err)
		}
	})
}

func dataRule(effect Effect, action Action, prefix string) PermissionRule {
	return PermissionRule{Effect: effect, Action: action, Resource: DataResource, Prefix: &prefix}
}

func machineBootstrapFixture(t *testing.T) (Bootstrap, string, time.Time) {
	t.Helper()
	raw, err := NewMachineToken()
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := MachineTokenDigest(raw)
	now := time.Date(2026, 10, 3, 1, 0, 0, 0, time.UTC)
	identity := Identity{Kind: MachinePrincipal, MachineName: "worker"}
	read, denied := dataRule(Allow, VertexRead, "orders:"), dataRule(Deny, VertexRead, "orders:private:")
	read.ID, denied.ID = "read", "private"
	return Bootstrap{Revision: 1, Issuer: testImage().Issuers[0], AdminSubjects: []string{"admin"},
		Roles:    []Role{{ID: "reader", Rules: []PermissionRule{read, denied}}},
		Machines: []BootstrapMachine{{Name: identity.MachineName, RoleIDs: []string{"reader"}, Credentials: []MachineCredential{{Identity: identity, Digest: digest, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}}}}}, raw, now
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

type fakeCommitter struct {
	calls int
	err   error
}

func (c *fakeCommitter) CommitRevision(context.Context, *Revision) error { c.calls++; return c.err }

type fakeAuthorityClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeAuthorityClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeAuthorityClock) advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}
func newAuthorityTestStore(t *testing.T) (*Store, *fakeAuthorityClock) {
	t.Helper()
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize))
	store, err := NewStore(StoreOptions{Generation: [16]byte{1}, PublicKey: key.Public().(ed25519.PublicKey), PrivateKey: key, Committer: &fakeCommitter{}, Limits: DefaultPolicyLimits()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReconcileBootstrap(t.Context(), 0, [16]byte{1}, testImage()); err != nil {
		t.Fatal(err)
	}
	return store, &fakeAuthorityClock{now: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)}
}
