package security

import (
	"errors"
	"testing"
)

func TestBootstrapMonotonicConfigAndNoUserResurrection(t *testing.T) {
	store, _, _ := testStore(t, true)
	configuration := Bootstrap{Revision: 1, Issuer: testImage().Issuers[0], AdminSubjects: []string{"admin", "other"}}
	first, err := store.ApplyBootstrap(t.Context(), configuration)
	if err != nil {
		t.Fatal(err)
	}
	repeat, err := store.ApplyBootstrap(t.Context(), configuration)
	if err != nil || !repeat.Replayed || repeat.Digest != first.Digest {
		t.Fatal(repeat, err)
	}
	conflicting := configuration
	conflicting.AdminSubjects = []string{"admin"}
	if _, err = store.ApplyBootstrap(t.Context(), conflicting); !errors.Is(err, ErrBootstrapLocked) {
		t.Fatal("same revision changed config", err)
	}
	current, _ := store.Current()
	image := current.Snapshot().Image()
	for i := range image.Principals {
		if image.Principals[i].Identity.Subject == "admin" {
			image.Principals[i].State = Suspended
		}
	}
	if _, err = store.Commit(t.Context(), current.Sequence(), [16]byte{9}, image); err != nil {
		t.Fatal(err)
	}
	next := configuration
	next.Revision = 2
	next.AdminSubjects = []string{"admin", "third"}
	if _, err = store.ApplyBootstrap(t.Context(), next); err != nil {
		t.Fatal(err)
	}
	current, _ = store.Current()
	result := current.Snapshot().Image()
	for _, principal := range result.Principals {
		if principal.Identity.Subject == "admin" && principal.State != Suspended {
			t.Fatal("suspended admin resurrected")
		}
		if principal.Identity.Subject == "other" && len(principal.Assignments) != 0 {
			t.Fatal("removed subject kept env Role")
		}
	}
	if _, err = store.ApplyBootstrap(t.Context(), configuration); !errors.Is(err, ErrBootstrapLocked) {
		t.Fatal("old pod restored removed subject", err)
	}
}

func TestBootstrapCanonicalDirectedPairsAreDetached(t *testing.T) {
	pair := &PrefixPair{Tail: "users:alice:", Head: "profiles:"}
	configuration := Bootstrap{Revision: 1, Issuer: testImage().Issuers[0], AdminSubjects: []string{"admin"}, Roles: []Role{{ID: "connections", Rules: []PermissionRule{{ID: "create", Effect: Allow, Action: EdgeCreate, Resource: DataResource, Pair: pair}}}}}
	canonical, digest, err := configuration.canonical()
	if err != nil {
		t.Fatal(err)
	}
	pair.Tail = ""
	if canonical.Roles[0].Rules[0].Pair.Tail != "users:alice:" {
		t.Fatal("canonical policy retained mutable pair input")
	}
	_, changed, err := configuration.canonical()
	if err != nil || changed == digest {
		t.Fatal("directed pair omitted from bootstrap digest", err)
	}
}
func TestBootstrapRejectsImplicitMachineAdministration(t *testing.T) {
	store, _, _ := testStore(t, true)
	configuration := Bootstrap{Revision: 1, Issuer: testImage().Issuers[0], AdminSubjects: []string{"admin"}, Machines: []BootstrapMachine{{Name: "machine", RoleIDs: []string{"security_admin"}}}}
	if _, err := store.ApplyBootstrap(t.Context(), configuration); err == nil {
		t.Fatal("machine implicitly acquired administration")
	}
	configuration.Machines = nil
	configuration.AdminSubjects = []string{"admin", "admin"}
	if _, err := store.ApplyBootstrap(t.Context(), configuration); err == nil {
		t.Fatal("duplicate subjects accepted")
	}
}
