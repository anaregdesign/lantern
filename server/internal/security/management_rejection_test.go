package security

import (
	"errors"
	"testing"
	"time"
)

func TestManagementRejectionValidationAndPersistenceBoundary(t *testing.T) {
	for _, test := range []struct {
		name     string
		changes  []Change
		expected uint64
		reason   ManagementRejectionReason
	}{
		{"malformed", []Change{{Kind: "invented"}}, 1, RejectInvalidChanges},
		{"duplicate", []Change{{Kind: DeleteRole, RoleID: "reader"}, {Kind: DeleteRole, RoleID: "reader"}}, 1, RejectInvalidChanges},
		{"unknown role", []Change{{Kind: PutAssignment, Identity: identityPointer(testIdentity()), RoleID: "unknown"}}, 1, RejectUnknownRole},
		{"env assignment", []Change{{Kind: DeleteAssignment, Identity: identityPointer(testIdentity()), RoleID: "security_admin"}}, 1, RejectEnvironmentOwned},
		{"stale revision", []Change{{Kind: DeleteRole, RoleID: "reader"}}, 2, RejectRevisionConflict},
		{"last human", []Change{{Kind: PutPrincipal, Identity: identityPointer(testIdentity()), State: Suspended}}, 1, RejectLastAdministrator},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, sink, _ := testStore(t, true)
			if _, err := store.ReconcileBootstrap(t.Context(), 0, [16]byte{1}, testImage()); err != nil {
				t.Fatal(err)
			}
			request := ManagementRequest{ExpectedRevision: test.expected, ChangeID: [16]byte{2}, Actor: testIdentity(), Authentication: Authentication{Provenance: BrowserCode, Class: EndUser, IssuerConfigRevision: 1}, Now: time.Now(), Changes: test.changes}
			_, err := store.Manage(t.Context(), request)
			var rejected *ManagementRejection
			if !errors.As(err, &rejected) || rejected.Reason != test.reason || sink.calls != 1 {
				t.Fatal("missing local refusal", err, sink.calls)
			}
			if _, err := store.ChangeStatus(request.ChangeID); !errors.Is(err, ErrUnknownChange) {
				t.Fatal("refusal recorded a commit", err)
			}
		})
	}
	t.Run("retained changed intent", func(t *testing.T) {
		store, sink, _ := testStore(t, true)
		if _, err := store.ReconcileBootstrap(t.Context(), 0, [16]byte{1}, testImage()); err != nil {
			t.Fatal(err)
		}
		role := Role{ID: "ordinary"}
		request := ManagementRequest{ExpectedRevision: 1, ChangeID: [16]byte{2}, Actor: testIdentity(), Authentication: Authentication{Provenance: BrowserCode, Class: EndUser, IssuerConfigRevision: 1}, Now: time.Now(), Changes: []Change{{Kind: PutRole, Role: &role}}}
		if _, err := store.Manage(t.Context(), request); err != nil {
			t.Fatal(err)
		}
		role.Name = "changed"
		_, err := store.Manage(t.Context(), request)
		var rejected *ManagementRejection
		if !errors.Is(err, ErrChangeConflict) || errors.As(err, &rejected) || sink.calls != 2 {
			t.Fatal("retained conflict classified as fresh refusal", err)
		}
	})
}

func identityPointer(identity Identity) *Identity { return &identity }
