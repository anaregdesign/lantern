package security

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestBootstrapExplicitHumanBearerQualificationPreservesSubjectsAndLifecycle(t *testing.T) {
	store, _, options := testStore(t, true)
	legacy := testImage()
	legacy.Principals[0].HumanIssuerConfigRevision = 0
	legacy.Principals[0].State = Suspended
	legacy.Issuers[0].EnvOwned = true
	human := testIdentity()
	human.Subject = "legacy-human"
	client := testIdentity()
	client.Subject = "oauth-client"
	legacy.Principals = append(legacy.Principals, Principal{Identity: human, State: Active, Assignments: []RoleAssignment{{RoleID: "security_admin", EnvOwned: true}}}, Principal{Identity: client, State: Active, Assignments: []RoleAssignment{{RoleID: "security_admin"}}})
	encoded, _ := json.Marshal(legacy)
	snapshot, err := DecodeImage(encoded, options.Limits)
	if err != nil {
		t.Fatal(err)
	}
	old, err := SignRevision(options.Generation, 1, [32]byte{}, [16]byte{1}, snapshot, options.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Apply(t.Context(), old.Encode()); err != nil {
		t.Fatal(err)
	}
	configuration := Bootstrap{Revision: 2, Issuer: legacy.Issuers[0], AdminSubjects: []string{"admin", "legacy-human"}}
	configuration.Issuer.HumanSubjectNamespaceQualified = true // verified operator issuance contract
	if _, err := store.ApplyBootstrap(t.Context(), configuration); err != nil {
		t.Fatal("explicit legacy qualification failed", err)
	}
	current, _ := store.Current()
	if current.Snapshot().BearerActor(human) != EndUser || current.Snapshot().BearerActor(client) != UnresolvedActor {
		t.Fatal("qualification inferred all OIDC subjects as human")
	}
	if _, active := current.Snapshot().AccessFor(testIdentity()); active {
		t.Fatal("qualification resurrected suspended bootstrap human")
	}
	if len(current.Snapshot().Image().Sessions) != 0 {
		t.Fatal("durable enrollment required browser session")
	}
}

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

func TestBootstrapCanonicalPrefixesAreDetached(t *testing.T) {
	prefix := "heads:"
	configuration := Bootstrap{Revision: 1, Issuer: testImage().Issuers[0], AdminSubjects: []string{"admin"}, Roles: []Role{{ID: "heads", Rules: []PermissionRule{{ID: "write", Effect: Allow, Action: VertexWrite, Resource: DataResource, Prefix: &prefix}}}}}
	canonical, digest, err := configuration.canonical()
	if err != nil {
		t.Fatal(err)
	}
	prefix = ""
	if *canonical.Roles[0].Rules[0].Prefix != "heads:" {
		t.Fatal("canonical policy retained mutable prefix input")
	}
	_, changed, err := configuration.canonical()
	if err != nil || changed == digest {
		t.Fatal("literal prefix omitted from bootstrap digest", err)
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
