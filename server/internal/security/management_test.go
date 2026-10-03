package security

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

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
	request := ManagementRequest{ExpectedRevision: 1, ChangeID: [16]byte{2}, Actor: testIdentity(), AuthTime: now, Now: now, Changes: []Change{
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
	inUse := ManagementRequest{ExpectedRevision: 2, ChangeID: [16]byte{3}, Actor: testIdentity(), AuthTime: now, Now: now.Add(time.Second), Changes: []Change{{Kind: DeleteRole, RoleID: role.ID}}}
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
	base := ManagementRequest{ExpectedRevision: 1, ChangeID: [16]byte{2}, Actor: testIdentity(), AuthTime: now, Now: now}
	identity := testIdentity()
	for _, test := range []struct {
		name string
		edit func(*ManagementRequest)
		want error
	}{
		{"old authentication", func(r *ManagementRequest) {
			r.AuthTime = now.Add(-6 * time.Minute)
			r.Changes = []Change{{Kind: RevokeSessions, Identity: &identity}}
		}, ErrRecentAuthentication},
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
