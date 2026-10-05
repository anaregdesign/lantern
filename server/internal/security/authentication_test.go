package security

import (
	"errors"
	"testing"
	"time"
)

func TestAuthenticationProvenanceAndActorQualification(t *testing.T) {
	image := testImage()
	client := testIdentity()
	client.Subject = "oauth-client"
	image.Principals = append(image.Principals, Principal{Identity: client, State: Active, Assignments: []RoleAssignment{{RoleID: "security_admin"}}})
	snapshot, err := CompileImage(image, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.BearerActor(testIdentity()) != UnresolvedActor || snapshot.HumanIdentity(client) {
		t.Fatal("OIDC identity or management Role inferred a human")
	}
	image.Issuers[0].HumanSubjectNamespaceQualified = true
	snapshot, err = CompileImage(image, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.BearerActor(testIdentity()) != EndUser || snapshot.BearerActor(client) != UnresolvedActor || snapshot.BearerActor(Identity{Kind: MachinePrincipal, MachineName: "worker"}) != MachineActor {
		t.Fatal("qualified exact enrollment did not distinguish actors")
	}
	now := time.Now()
	for _, authentication := range []Authentication{
		{Provenance: RFC9068Bearer, Class: UnresolvedActor, IssuerConfigRevision: 1},
		{Provenance: NativeMachine, Class: EndUser, IssuerConfigRevision: 1},
		{Provenance: RFC9068Bearer, Class: EndUser, IssuerConfigRevision: 2},
	} {
		if err := snapshot.checkHumanAuthentication(testIdentity(), authentication, now); !errors.Is(err, ErrPermissionDenied) {
			t.Fatal("unqualified provenance admitted", authentication, err)
		}
	}
	if err := snapshot.checkHumanAuthentication(testIdentity(), Authentication{Provenance: RFC9068Bearer, Class: EndUser, IssuerConfigRevision: 1}, now); err != nil {
		t.Fatal("qualified human Bearer denied", err)
	}
	image.Issuers[0].ConfigRevision++
	if _, err := CompileImage(image, DefaultPolicyLimits()); !errors.Is(err, ErrLastAdministrator) {
		t.Fatal("stale human enrollment remained an eligible administrator", err)
	}
}

func TestAuthenticationEligibleAdministratorIsDurableHuman(t *testing.T) {
	image := testImage()
	client := testIdentity()
	client.Subject = "client"
	image.Principals = append(image.Principals, Principal{Identity: client, State: Active, Assignments: []RoleAssignment{{RoleID: "security_admin"}}})
	image.Principals[0].State = Suspended
	if _, err := CompileImage(image, DefaultPolicyLimits()); !errors.Is(err, ErrLastAdministrator) {
		t.Fatal("OIDC client satisfied last human administrator invariant", err)
	}
	image.Principals[0].State = Active
	image.Sessions = nil
	if _, err := CompileImage(image, DefaultPolicyLimits()); err != nil {
		t.Fatal("human enrollment required a live session", err)
	}
}
