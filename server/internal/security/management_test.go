package security

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestManagementLegacySignedCutRetainedIntentAndHumanEnrollment(t *testing.T) {
	// Fixed e60e16ec-compatible bytes: the bootstrap human is suspended and a
	// non-env OIDC administrator remains. No new qualification field exists.
	const oldImage = `{"version":2,"bootstrap_revision":1,"issuers":[{"config_revision":1,"url":"https://idp.example/realm","enabled":true,"client_id":"admin-client","api_audience":"lantern","redirect_uri":"https://admin.example/auth/callback","algorithms":["RS256"]}],"roles":[{"id":"security_admin","name":"","rules":[{"effect":"allow","action":"security.manage","resource":"global"}]}],"principals":[{"identity":{"kind":"oidc","issuer":"https://idp.example/realm","subject":"admin"},"state":"suspended","assignments":[{"role_id":"security_admin","env_owned":true}]},{"identity":{"kind":"oidc","issuer":"https://idp.example/realm","subject":"legacy"},"state":"active","assignments":[{"role_id":"security_admin"}]}],"sessions":null}`
	const oldIntent = `{"Version":1,"Expected":1,"Actor":{"kind":"oidc","issuer":"https://idp.example/realm","subject":"legacy"},"Changes":[{"kind":"role.put","role":{"id":"original","name":"","rules":null}}]}`
	legacy, err := DecodeImage([]byte(oldImage), DefaultPolicyLimits())
	if err != nil || !bytes.Equal(legacy.image, []byte(oldImage)) {
		t.Fatal("original signed image changed", err)
	}
	actor := testIdentity()
	actor.Subject = "legacy"
	if legacy.HumanIdentity(actor) || legacy.BearerActor(actor) != UnresolvedActor {
		t.Fatal("historical read enrolled an OIDC principal")
	}
	if _, err := CompileImage(legacy.Image(), DefaultPolicyLimits()); !errors.Is(err, ErrLastAdministrator) {
		t.Fatal("new write reused historical admin invariant", err)
	}
	machineOnly := strings.ReplaceAll(oldImage, `{"kind":"oidc","issuer":"https://idp.example/realm","subject":"legacy"}`, `{"kind":"machine","machine_name":"legacy"}`)
	if _, err := DecodeImage([]byte(machineOnly), DefaultPolicyLimits()); !errors.Is(err, ErrLastAdministrator) {
		t.Fatal("legacy machine counted as administrator", err)
	}
	path := filepath.Join(t.TempDir(), "legacy.wal")
	options := nativeTestOptions(t, path)
	native, err := CreateNativeStore(options)
	if err != nil {
		t.Fatal(err)
	}
	first, err := SignRevision(options.Generation, 1, [32]byte{}, [16]byte{1}, legacy, options.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := native.Store().Apply(t.Context(), first.Encode()); err != nil {
		t.Fatal(err)
	}
	image := legacy.Image()
	role := Role{ID: "original"}
	image.Roles = append(image.Roles, role)
	encoded, _ := json.Marshal(image)
	next, err := DecodeImage(encoded, options.Limits)
	if err != nil {
		t.Fatal(err)
	}
	intent := sha256.Sum256([]byte(oldIntent))
	original, err := signRevisionWithHistory(options.Generation, 2, first.Digest(), [16]byte{2}, next, options.PrivateKey, native.Store().checkpointHistory(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if err := native.Store().Apply(t.Context(), original.Encode()); err != nil {
		t.Fatal(err)
	}
	if err := native.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := ResumeNativeStore(nativeTestOptions(t, path))
	if err != nil {
		t.Fatal("legacy WAL rejected", err)
	}
	defer reopened.Close()
	proof, err := reopened.Store().ChangeStatus([16]byte{2})
	if err != nil || proof.Digest != original.Digest() {
		t.Fatal("old retained proof changed", err)
	}
	// Certified signed checkpoint installation preserves the same original cut.
	_, _, replicaOptions := testStore(t, false)
	replicaOptions.Generation, replicaOptions.PublicKey = options.Generation, options.PublicKey
	replica, _ := NewStore(replicaOptions)
	decoded, err := DecodeRevision(original.Encode(), options.PublicKey, options.Limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := replica.restoreCheckpoint(decoded); err != nil {
		t.Fatal(err)
	}
	if proof, err := replica.ChangeStatus([16]byte{2}); err != nil || proof.Digest != original.Digest() {
		t.Fatal("legacy checkpoint lost proof", err)
	}
	// A later verified Code login explicitly enrolls this exact existing human.
	// It does not rewrite the old signed bytes or retained management intent.
	now := time.Now().UTC()
	if _, err := reopened.Store().IssueSession(t.Context(), SessionRequest{ChangeID: [16]byte{3}, Identity: actor, IssuerConfigRevision: 1, Digest: strings.Repeat("a", 64), CSRFDigest: strings.Repeat("b", 64), Now: now, Lifetime: time.Hour}); err != nil {
		t.Fatal("explicit Code enrollment failed", err)
	}
	request := ManagementRequest{ExpectedRevision: 1, ChangeID: [16]byte{2}, Actor: actor, Authentication: Authentication{Provenance: BrowserCode, Class: EndUser, IssuerConfigRevision: 1}, Now: now, Changes: []Change{{Kind: PutRole, Role: &role}}}
	actual, _, err := reopened.Store().managementIntent(request)
	if err != nil || actual != intent {
		t.Fatal("fixed legacy v1 bytes changed", err)
	}
	replay, err := reopened.Store().Manage(t.Context(), request)
	if err != nil || !replay.Replayed || replay.Digest != original.Digest() || replay.Revision != 2 {
		t.Fatal("enrollment/restart changed retained original", replay, err)
	}
}

func TestManagementOrdinaryMissingAndOldAuthentication(t *testing.T) {
	for _, authTime := range []time.Time{{}, time.Now().Add(-time.Hour)} {
		store, _, _ := testStore(t, true)
		if _, err := store.ReconcileBootstrap(t.Context(), 0, [16]byte{1}, testImage()); err != nil {
			t.Fatal(err)
		}
		role := Role{ID: "data", Rules: []PermissionRule{dataRule(Allow, VertexRead, "mine:")}}
		role.Rules[0].ID = "read"
		identity := testIdentity()
		request := ManagementRequest{ExpectedRevision: 1, ChangeID: [16]byte{2}, Actor: identity, Authentication: Authentication{Provenance: BrowserCode, Class: EndUser, IssuerConfigRevision: 1}, AuthTime: authTime, Now: time.Now(), Changes: []Change{{Kind: PutRole, Role: &role}, {Kind: PutAssignment, Identity: &identity, RoleID: role.ID}}}
		prepared, err := store.PrepareManagement(t.Context(), request)
		if err != nil || prepared.AuthorizationRequired {
			t.Fatal("ordinary data expansion required authentication", prepared, err)
		}
		original, err := store.Manage(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		request.AuthTime = time.Time{}
		request.Authorize = func(ManagementBinding, time.Time) error {
			t.Fatal("retry demanded new authorization")
			return ErrOperationAuthorization
		}
		replay, err := store.Manage(t.Context(), request)
		if err != nil || !replay.Replayed || replay.Digest != original.Digest {
			t.Fatal("retained retry lost original result", replay, err)
		}
		// Retained business intent remains the e60e16ec v1 encoding, excluding
		// auth evidence, clocks and authorization callbacks.
		encoded, _ := json.Marshal(struct {
			Version  int
			Expected uint64
			Actor    Identity
			Changes  []Change
		}{1, request.ExpectedRevision, request.Actor, request.Changes})
		digest, _, err := store.managementIntent(request)
		if err != nil || digest != sha256.Sum256(encoded) {
			t.Fatal("v1 intent changed", err)
		}
	}
}

func TestManagementEffectiveConsequencePreflight(t *testing.T) {
	for _, test := range []struct {
		name        string
		suspended   bool
		unqualified bool
		changes     func(Identity) []Change
		highImpact  bool
	}{
		{"unassign deny", false, false, func(identity Identity) []Change {
			return []Change{{Kind: DeleteAssignment, Identity: &identity, RoleID: "deny"}}
		}, true},
		{"delete deny role atomically", false, false, func(identity Identity) []Change {
			return []Change{{Kind: DeleteRole, RoleID: "deny"}, {Kind: DeleteAssignment, Identity: &identity, RoleID: "deny"}}
		}, true},
		{"remove deny rule", false, false, func(Identity) []Change { role := Role{ID: "deny"}; return []Change{{Kind: PutRole, Role: &role}} }, true},
		{"unqualified unassign deny", false, true, func(identity Identity) []Change {
			return []Change{{Kind: DeleteAssignment, Identity: &identity, RoleID: "deny"}}
		}, true},
		{"unqualified remove deny rule", false, true, func(Identity) []Change { role := Role{ID: "deny"}; return []Change{{Kind: PutRole, Role: &role}} }, true},
		{"unknown subject management grant", false, true, func(Identity) []Change {
			identity := testIdentity()
			identity.Subject = "unknown"
			return []Change{{Kind: PutPrincipal, Identity: &identity, State: Active}, {Kind: PutAssignment, Identity: &identity, RoleID: "allow"}}
		}, true},
		{"activate authority", true, false, func(identity Identity) []Change {
			return []Change{{Kind: PutPrincipal, Identity: &identity, State: Active}, {Kind: DeleteAssignment, Identity: &identity, RoleID: "deny"}}
		}, true},
		{"unassigned management role", false, false, func(Identity) []Change {
			role := Role{ID: "unassigned", Rules: []PermissionRule{globalRule(Allow, SecurityManage)}}
			role.Rules[0].ID = "manage"
			return []Change{{Kind: PutRole, Role: &role}}
		}, false},
		{"issuer trust", false, false, func(Identity) []Change {
			issuer := testImage().Issuers[0]
			issuer.URL = "https://second.example"
			issuer.ConfigRevision = 0
			return []Change{{Kind: PutIssuer, Issuer: &issuer}}
		}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, sink, _ := testStore(t, true)
			image := testImage()
			image.Roles = append(image.Roles, Role{ID: "allow", Rules: []PermissionRule{globalRule(Allow, SecurityManage)}}, Role{ID: "deny", Rules: []PermissionRule{globalRule(Deny, SecurityManage)}})
			identity := testIdentity()
			identity.Subject = "target"
			state := Active
			if test.suspended {
				state = Suspended
			}
			humanRevision := uint64(1)
			if test.unqualified {
				humanRevision = 0
			}
			image.Principals = append(image.Principals, Principal{Identity: identity, State: state, HumanIssuerConfigRevision: humanRevision, Assignments: []RoleAssignment{{RoleID: "allow"}, {RoleID: "deny"}}})
			if _, err := store.ReconcileBootstrap(t.Context(), 0, [16]byte{1}, image); err != nil {
				t.Fatal(err)
			}
			request := ManagementRequest{ExpectedRevision: 1, ChangeID: [16]byte{2}, Actor: testIdentity(), Authentication: Authentication{Provenance: BrowserCode, Class: EndUser, IssuerConfigRevision: 1}, Now: time.Now(), Changes: test.changes(identity)}
			prepared, err := store.PrepareManagement(t.Context(), request)
			if err != nil || prepared.AuthorizationRequired != test.highImpact || sink.calls != 1 {
				t.Fatal("incorrect nonmutating consequence", prepared, err, sink.calls)
			}
			if test.highImpact {
				if _, err := store.Manage(t.Context(), request); !errors.Is(err, ErrOperationAuthorization) || sink.calls != 1 {
					t.Fatal("unapproved operation changed policy", err)
				}
				request.Authorize = func(binding ManagementBinding, _ time.Time) error {
					if binding != prepared.Binding {
						t.Fatal("prepared proof context changed")
					}
					return nil
				}
			}
			original, err := store.Manage(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			request.Authorize = nil
			replay, err := store.Manage(t.Context(), request)
			if err != nil || !replay.Replayed || replay.Digest != original.Digest {
				t.Fatal("known committed retry required new proof", replay, err)
			}
		})
	}
}

func TestManagementAtomicRolesAssignmentsAuditAndRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "system.wal")
	options := nativeTestOptions(t, path)
	native, err := CreateNativeStore(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := native.Store().ReconcileBootstrap(t.Context(), 0, [16]byte{1}, testImage()); err != nil {
		t.Fatal(err)
	}
	identity := testIdentity()
	identity.Subject = "registered"
	allow := dataRule(Allow, VertexRead, "orders:")
	allow.ID = "orders"
	deny := dataRule(Deny, VertexRead, "orders:private:")
	deny.ID = "private"
	role := Role{ID: "scoped_reader", Rules: []PermissionRule{allow, deny}}
	now := time.Now().UTC()
	request := ManagementRequest{ExpectedRevision: 1, ChangeID: [16]byte{2}, Actor: testIdentity(), Authentication: Authentication{Provenance: BrowserCode, Class: EndUser, IssuerConfigRevision: 1}, AuthTime: now, Now: now, Changes: []Change{
		{Kind: PutPrincipal, Identity: &identity, State: Active},
		{Kind: PutRole, Role: &role},
		{Kind: PutAssignment, Identity: &identity, RoleID: role.ID},
	}}
	result, err := native.Store().Manage(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := native.Store().Current()
	access, active := current.Snapshot().AccessFor(identity)
	if !active || !access.Allows(VertexRead, "orders:public:1") || access.Allows(VertexRead, "orders:private:1") || access.AllowsGlobal(SecurityManage) {
		t.Fatal("Role-only derived access is wrong")
	}
	if len(current.Snapshot().Image().Audit) != 1 {
		t.Fatal("audit did not commit atomically")
	}
	request.Now = now.Add(time.Second)
	replay, err := native.Store().Manage(t.Context(), request)
	if err != nil || !replay.Replayed || replay.Digest != result.Digest {
		t.Fatal("server-owned audit time broke retry", replay, err)
	}
	changed := request
	changed.Changes = []Change{{Kind: DeletePrincipal, Identity: &identity}}
	if _, err := native.Store().Manage(t.Context(), changed); !errors.Is(err, ErrChangeConflict) {
		t.Fatal("change ID accepted different intent", err)
	}
	inUse := ManagementRequest{ExpectedRevision: 2, ChangeID: [16]byte{3}, Actor: testIdentity(), Authentication: request.Authentication, AuthTime: now, Now: now.Add(time.Second), Changes: []Change{{Kind: DeleteRole, RoleID: role.ID}}}
	if _, err := native.Store().Manage(t.Context(), inUse); !errors.Is(err, ErrUnknownRole) {
		t.Fatal("assigned Role deletion succeeded", err)
	}
	before, _ := native.Store().Current()
	if before.sequence != 2 || len(before.Snapshot().Image().Audit) != 1 {
		t.Fatal("failed batch changed authority or audit")
	}
	if err := native.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := ResumeNativeStore(nativeTestOptions(t, path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	replay, err = reopened.Store().Manage(t.Context(), request)
	if err != nil || !replay.Replayed || replay.Digest != result.Digest {
		t.Fatal("durable retry lost typed intent", replay, err)
	}
	// Explicitly remove membership in the same transaction as Role deletion.
	inUse.Changes = append(inUse.Changes, Change{Kind: DeleteAssignment, Identity: &identity, RoleID: role.ID})
	if _, err := reopened.Store().Manage(t.Context(), inUse); err != nil {
		t.Fatal("atomic membership removal and Role deletion failed", err)
	}
}

func TestManagementAdmissionLastAdminAndCAS(t *testing.T) {
	store, sink, _ := testStore(t, true)
	if _, err := store.ReconcileBootstrap(t.Context(), 0, [16]byte{1}, testImage()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	base := ManagementRequest{ExpectedRevision: 1, ChangeID: [16]byte{2}, Actor: testIdentity(), Authentication: Authentication{Provenance: BrowserCode, Class: EndUser, IssuerConfigRevision: 1}, AuthTime: now, Now: now}
	identity := testIdentity()
	for _, test := range []struct {
		name string
		edit func(*ManagementRequest)
		want error
	}{
		{"unresolved actor", func(r *ManagementRequest) {
			r.Authentication.Class = UnresolvedActor
			r.Changes = []Change{{Kind: RevokeSessions, Identity: &identity}}
		}, ErrPermissionDenied},
		{"future authentication", func(r *ManagementRequest) {
			r.AuthTime = now.Add(time.Second)
			r.Changes = []Change{{Kind: RevokeSessions, Identity: &identity}}
		}, ErrInvalidImage},
		{"last admin", func(r *ManagementRequest) {
			r.Changes = []Change{{Kind: PutPrincipal, Identity: &identity, State: Suspended}}
		}, ErrLastAdministrator},
		{"env assignment", func(r *ManagementRequest) {
			r.Changes = []Change{{Kind: DeleteAssignment, Identity: &identity, RoleID: "security_admin"}}
		}, ErrBootstrapLocked},
		{"protected peer", func(r *ManagementRequest) {
			r.Changes = []Change{{Kind: PutAssignment, Identity: &identity, RoleID: "cluster_replica"}}
		}, ErrInvalidPolicy},
		{"unknown actor", func(r *ManagementRequest) {
			r.Actor.Subject = "unknown"
			r.Changes = []Change{{Kind: RevokeSessions, Identity: &identity}}
		}, ErrPermissionDenied},
		{"ambiguous duplicate", func(r *ManagementRequest) {
			r.Changes = []Change{{Kind: RevokeSessions, Identity: &identity}, {Kind: RevokeSessions, Identity: &identity}}
		}, ErrInvalidImage},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := base
			test.edit(&request)
			if _, err := store.Manage(t.Context(), request); !errors.Is(err, test.want) {
				t.Fatal(err, test.want)
			}
		})
	}
	if sink.calls != 1 {
		t.Fatal("failed validation reached persistence")
	}
	var group sync.WaitGroup
	results := make(chan error, 2)
	for i := range 2 {
		group.Go(func() {
			request := base
			request.ChangeID = [16]byte{byte(i + 2)}
			role := Role{ID: []string{"one", "two"}[i]}
			request.Changes = []Change{{Kind: PutRole, Role: &role}}
			_, err := store.Manage(context.Background(), request)
			results <- err
		})
	}
	group.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrRevisionConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 || sink.calls != 2 {
		t.Fatal("CAS did not serialize one complete change", successes, conflicts, sink.calls)
	}
}
