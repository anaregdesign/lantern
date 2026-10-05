package security

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestS1ManagementTransitionMatrix(t *testing.T) {
	for _, tc := range []struct {
		name    string
		changes func(Image) []Change
		want    error
	}{
		{PutIssuer, func(i Image) []Change {
			issuer := i.Issuers[0]
			issuer.URL = "https://another.example"
			issuer.ConfigRevision = 0
			return []Change{{Kind: PutIssuer, Issuer: &issuer}}
		}, nil},
		{DisableIssuer, func(i Image) []Change { return []Change{{Kind: DisableIssuer, IssuerURL: i.Issuers[0].URL}} }, ErrLastAdministrator},
		{DeleteIssuer, func(i Image) []Change { return []Change{{Kind: DeleteIssuer, IssuerURL: i.Issuers[0].URL}} }, ErrLastAdministrator},
		{PutRole, func(i Image) []Change { return []Change{s1ReaderRole()} }, nil},
		{DeleteRole, func(i Image) []Change { return []Change{{Kind: DeleteRole, RoleID: "reader"}} }, nil},
		{PutPrincipal, func(i Image) []Change {
			b := s1Bob()
			return []Change{{Kind: PutPrincipal, Identity: &b, State: Suspended}}
		}, nil},
		{DeletePrincipal, func(i Image) []Change { b := s1Bob(); return []Change{{Kind: DeletePrincipal, Identity: &b}} }, nil},
		{PutAssignment, func(i Image) []Change {
			b := s1Bob()
			return []Change{{Kind: PutAssignment, Identity: &b, RoleID: "reader"}}
		}, nil},
		{DeleteAssignment, func(i Image) []Change {
			b := s1Bob()
			return []Change{{Kind: DeleteAssignment, Identity: &b, RoleID: "reader"}}
		}, nil},
		{RevokeSessions, func(i Image) []Change { b := s1Bob(); return []Change{{Kind: RevokeSessions, Identity: &b}} }, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			image := s1Image()
			s := s1Fixture(t, image)
			op := s1Operation(t, s.projection, testIdentity(), s1Changes(tc.changes(image)...))
			a, err := AssessS1(s.projection, op)
			if !errors.Is(err, tc.want) {
				t.Fatal(err, tc.want)
			}
			if err == nil && a.Next.cut.Sequence != s.projection.cut.Sequence+1 {
				t.Fatal("semantic transition")
			}
			if s.projection.cut.Sequence != 1 {
				t.Fatal("input changed")
			}
		})
	}
}

func TestS1DenyExpansionAndFullAdminClosure(t *testing.T) {
	image := s1Image()
	b := s1Bob()
	image.Roles = append(image.Roles, Role{ID: "deny", Rules: []PermissionRule{{ID: "deny", Effect: Deny, Action: SecurityManage, Resource: GlobalResource}}})
	image.Principals[1].Assignments = []RoleAssignment{{RoleID: "security_admin"}, {RoleID: "deny"}}
	s := s1Fixture(t, image)
	for _, changes := range [][]Change{
		{{Kind: DeleteAssignment, Identity: &b, RoleID: "deny"}},
		{{Kind: PutRole, Role: &Role{ID: "deny"}}},
		{{Kind: DeleteAssignment, Identity: &b, RoleID: "deny"}, {Kind: DeleteRole, RoleID: "deny"}},
		{{Kind: DeleteRole, RoleID: "deny"}, {Kind: DeleteAssignment, Identity: &b, RoleID: "deny"}},
	} {
		a, err := AssessS1(s.projection, s1Operation(t, s.projection, testIdentity(), s1Changes(changes...)))
		if err != nil || !a.NeedsPurpose || a.ProvenNonexpanding {
			t.Fatal("Deny revealed admin without purpose/reserve", a, err)
		}
	}
	if _, err := AssessS1(s.projection, s1Operation(t, s.projection, testIdentity(), s1Changes(Change{Kind: DeleteRole, RoleID: "deny"}))); !errors.Is(err, ErrUnknownRole) {
		t.Fatal("missing dependency omitted", err)
	}
	admin := testIdentity()
	for _, c := range []S1Command{
		s1Changes(Change{Kind: PutPrincipal, Identity: &admin, State: Suspended}),
		s1Changes(Change{Kind: DeletePrincipal, Identity: &admin}),
		s1Changes(Change{Kind: DeleteAssignment, Identity: &admin, RoleID: "security_admin"}),
		s1Changes(Change{Kind: PutRole, Role: &Role{ID: "security_admin", Rules: []PermissionRule{{ID: "deny", Effect: Deny, Action: SecurityManage, Resource: GlobalResource}}}}),
		s1Changes(Change{Kind: PutIssuer, Issuer: func() *Issuer { i := image.Issuers[0]; i.ConfigRevision = 0; return &i }()}),
	} {
		base := s1Fixture(t, s1Image())
		if _, err := AssessS1(base.projection, s1Operation(t, base.projection, admin, c)); !errors.Is(err, ErrLastAdministrator) {
			t.Fatal("last qualified OIDC human lost", err)
		}
	}
	base := s1Fixture(t, s1Image())
	data := s1Operation(t, base.projection, admin, s1Changes(Change{Kind: PutAssignment, Identity: &b, RoleID: "reader"}))
	a, err := AssessS1(base.projection, data)
	if err != nil || a.NeedsPurpose || a.ProvenNonexpanding {
		t.Fatal("data expansion classification", err)
	}
}

func TestS1SessionLineageAndExclusiveRotation(t *testing.T) {
	image := s1Image()
	session := testSession()
	session.CSRFDigest = strings.Repeat("b", 64)
	image.Sessions = []Session{session}
	s := s1Fixture(t, image)
	newSession := session
	newSession.Digest = strings.Repeat("c", 64)
	newSession.CSRFDigest = strings.Repeat("d", 64)
	newSession.CreatedAt = newSession.CreatedAt.Add(time.Minute)
	newSession.ExpiresAt = newSession.CreatedAt.Add(time.Hour)
	command := S1Command{Kind: S1IssueSession, Session: &newSession, ReplacesDigest: session.Digest, SessionLineage: s.projection.lineage[testIdentity()]}
	op := s1Operation(t, s.projection, testIdentity(), command)
	a, err := AssessS1(s.projection, op)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := a.Next.snapshot.Session(session.Digest)
	if !old.Revoked {
		t.Fatal("predecessor remains usable")
	}
	if _, err := AssessS1(a.Next, s1Operation(t, a.Next, testIdentity(), command)); !errors.Is(err, ErrChangeConflict) {
		t.Fatal("duplicate session", err)
	}
	newSession.Digest = strings.Repeat("e", 64)
	command.Session = &newSession
	if _, err := AssessS1(a.Next, s1Operation(t, a.Next, testIdentity(), command)); !errors.Is(err, ErrPermissionDenied) {
		t.Fatal("second replacement", err)
	}
	actor := testIdentity()
	revoke, err := AssessS1(s.projection, s1Operation(t, s.projection, actor, s1Changes(Change{Kind: RevokeSessions, Identity: &actor})))
	if err != nil {
		t.Fatal(err)
	}
	if revoke.Next.SessionLineage(actor) != 2 {
		t.Fatal("floor did not advance")
	}
	command.ReplacesDigest = ""
	if _, err := AssessS1(revoke.Next, s1Operation(t, revoke.Next, actor, command)); !errors.Is(err, ErrPermissionDenied) {
		t.Fatal("old lineage revived", err)
	}
	command.SessionLineage = 2
	if _, err := AssessS1(revoke.Next, s1Operation(t, revoke.Next, actor, command)); err != nil {
		t.Fatal("fresh login", err)
	}
	logout := s1Operation(t, s.projection, actor, S1Command{Kind: S1RevokeSession, SessionDigest: session.Digest, SessionLineage: 1})
	if _, err := AssessS1(s.projection, logout); err != nil {
		t.Fatal(err)
	}
}
