package security

import (
	"errors"
	"testing"
)

func TestPrincipalRegistrationTransitions(t *testing.T) {
	const uid = "11111111111111111111111111111111"
	actor := Identity{Kind: OIDCPrincipal, Issuer: GoogleIssuer, Subject: "new-user"}
	command := S1Command{Kind: S1RegisterPrincipal, Registration: &PrincipalRegistration{UserUID: uid, ClientProfileID: "android", IssuerConfigRevision: 1}}
	initial := s1Fixture(t, registrationImage()).projection
	operation := s1Operation(t, initial, actor, command)
	assessment, err := AssessS1(initial, operation)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.ProvenNonexpanding || assessment.NeedsPurpose {
		t.Fatal("registration capacity/purpose classification")
	}
	registered := assessment.Next
	if initial.snapshot.principals[actor].userUID != "" || registered.lineage[actor] != 1 || !registered.snapshot.HumanIdentity(actor) {
		t.Fatal("atomic registration facts")
	}
	access, active := registered.snapshot.AccessFor(actor)
	if !active || !access.Allows(VertexWrite, "users:"+uid+":profile:name") || access.AllowsGlobal(SecurityManage) || len(registered.Image().Sessions) != 0 {
		t.Fatal("registration grants/session coupling")
	}
	if _, err := AssessS1(registered, operation); !errors.Is(err, ErrRevisionConflict) {
		t.Fatal("old reviewed operation was restamped", err)
	}
	withdrawnImage := registered.Image()
	withdrawnImage.RegistrationPolicy.Enabled = false
	if err := applyManagementChanges(&withdrawnImage, []Change{{Kind: DeleteAssignment, Identity: &actor, RoleID: PersonalUserRoleID}}); err != nil {
		t.Fatal(err)
	}
	withdrawn := s1Fixture(t, withdrawnImage).projection
	repeated, err := AssessS1(withdrawn, s1Operation(t, withdrawn, actor, command))
	if err != nil {
		t.Fatal("registered confirmation needs open signup", err)
	}
	if uidGot, _, ok := repeated.Next.snapshot.PersonalArea(actor); !ok || uidGot != uid {
		t.Fatal("UID changed on repeat")
	}
	access, _ = repeated.Next.snapshot.AccessFor(actor)
	if access.Allows(VertexWrite, "users:"+uid+":profile:name") {
		t.Fatal("repeat restored withdrawn assignment")
	}
	for _, tc := range []struct {
		name string
		edit func(*Image)
	}{
		{"policy absent", func(i *Image) { i.RegistrationPolicy = nil }},
		{"policy closed", func(i *Image) { i.RegistrationPolicy.Enabled = false }},
		{"unlisted profile", func(i *Image) { i.RegistrationPolicy.Profiles[0].ClientProfileID = "ios" }},
		{"UID collision", func(i *Image) {
			i.Principals = append(i.Principals, Principal{Identity: Identity{Kind: OIDCPrincipal, Issuer: GoogleIssuer, Subject: "another"}, State: Deleted, UserUID: uid})
		}},
		{"suspended", func(i *Image) { i.Principals = append(i.Principals, Principal{Identity: actor, State: Suspended}) }},
		{"deleted", func(i *Image) {
			i.Principals = append(i.Principals, Principal{Identity: actor, State: Deleted, UserUID: uid})
		}},
		{"broadened Role", func(i *Image) {
			i.Roles[len(i.Roles)-1].Rules = append(i.Roles[len(i.Roles)-1].Rules, dataRule(Allow, VertexRead, ""))
		}},
		{"issuer disabled", func(i *Image) { i.Issuers[1].Enabled = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			image := registrationImage()
			tc.edit(&image)
			before := s1Fixture(t, image).projection
			if _, err := AssessS1(before, s1Operation(t, before, actor, command)); err == nil {
				t.Fatal("invalid registration accepted")
			}
			if before.snapshot.principals[actor].userUID != "" && tc.name != "deleted" {
				t.Fatal("input image mutated")
			}
		})
	}
	t.Run("legacy grants preserved", func(t *testing.T) {
		image := registrationImage()
		image.Principals = append(image.Principals, Principal{Identity: actor, State: Active, Assignments: []RoleAssignment{{RoleID: "reader"}}})
		prior := s1Fixture(t, image).projection
		after, err := AssessS1(prior, s1Operation(t, prior, actor, command))
		if err != nil {
			t.Fatal(err)
		}
		principal := after.Next.Image().Principals[len(image.Principals)-1]
		if len(principal.Assignments) != 2 || principal.Assignments[0].RoleID != "reader" || principal.UserUID != uid {
			t.Fatal("legacy grants overwritten")
		}
	})
}

func TestPrincipalRegistrationRequiresSeparateAuthority(t *testing.T) {
	image := registrationImage()
	actor := Identity{Kind: OIDCPrincipal, Issuer: GoogleIssuer, Subject: "existing"}
	image.Principals = append(image.Principals, Principal{Identity: actor, State: Active, HumanIssuerConfigRevision: 1})
	projection := s1Fixture(t, image).projection
	command := S1Command{Kind: S1RegisterPrincipal, Registration: &PrincipalRegistration{UserUID: "11111111111111111111111111111111", ClientProfileID: "android", IssuerConfigRevision: 1}}
	h := s1Seal(projection, s1Operation(t, projection, actor, command), 1, false)
	h.authorization.authentication.Provenance = BrowserCode
	if s1CurrentAuthority(projection, h, command) {
		t.Fatal("ordinary browser credential authorized registration")
	}
	var owner *CurrentAuthority
	if _, _, err := owner.Prepare(t.Context(), nil, nil, CurrentProfile{}, projection.cut, command); !errors.Is(err, ErrPermissionDenied) {
		t.Fatal("ordinary Prepare reached registration", err)
	}
	var origin *authorityOriginOwner
	if _, err := origin.prepare(t.Context(), nil, projection.cut, command); !errors.Is(err, ErrPermissionDenied) {
		t.Fatal("ordinary origin reached registration", err)
	}
}
