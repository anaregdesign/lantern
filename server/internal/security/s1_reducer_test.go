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

func TestS1IssuerRevocationPurposeAndRestrictiveReserve(t *testing.T) {
	for _, kind := range []string{DisableIssuer, DeleteIssuer} {
		t.Run(kind, func(t *testing.T) {
			image := s1Image()
			other := image.Issuers[0]
			other.URL = "https://surviving-admin.example"
			admin := testIdentity()
			admin.Issuer = other.URL
			candidate := admin
			candidate.Subject = "candidate"
			image.Issuers = append(image.Issuers, other)
			image.Principals = append(image.Principals, Principal{Identity: admin, State: Active, HumanIssuerConfigRevision: 1, Assignments: []RoleAssignment{{RoleID: "security_admin"}}}, Principal{Identity: candidate, State: Active, HumanIssuerConfigRevision: 1})
			session := testSession()
			session.CSRFDigest = strings.Repeat("b", 64)
			image.Sessions = []Session{session}
			base := s1Fixture(t, image)
			capacity := base.configuration.Capacity
			capacity.LedgerEntries, capacity.RestrictiveEntries = 2, 1
			base, err := NewS1ApplyState(base.projection, base.membership, capacity, S1Retention{})
			if err != nil {
				t.Fatal(err)
			}
			command := s1Changes(Change{Kind: kind, IssuerURL: testIdentity().Issuer})
			op := s1Operation(t, base.projection, admin, command)
			missing := s1Apply(t, base, s1Seal(base.projection, op, 1, false))
			if missing.Outcome.Disposition() != S1RejectedPurpose || missing.State.projection != base.projection {
				t.Fatal("trust revocation lost purpose requirement")
			}
			a, err := AssessS1(missing.State.projection, op)
			if err != nil || !a.NeedsPurpose || !a.ProvenNonexpanding {
				t.Fatal("trust revocation classification", a, err)
			}
			if a.Next.SessionLineage(testIdentity()) != 2 || a.Next.SessionLineage(s1Bob()) != 2 || a.Next.SessionLineage(admin) != 1 {
				t.Fatal("full issuer lineage closure")
			}
			stored, _ := a.Next.snapshot.Session(session.Digest)
			if !stored.Revoked || !a.Next.snapshot.HumanIdentity(admin) {
				t.Fatal("session/qualified admin closure")
			}
			if _, active := a.Next.snapshot.AccessFor(testIdentity()); active {
				t.Fatal("disabled issuer still active")
			}
			mixed := s1Operation(t, missing.State.projection, admin, s1Changes(Change{Kind: kind, IssuerURL: testIdentity().Issuer}, Change{Kind: PutAssignment, Identity: &candidate, RoleID: "security_admin"}))
			ma, err := AssessS1(missing.State.projection, mixed)
			if err != nil || !ma.NeedsPurpose || ma.ProvenNonexpanding {
				t.Fatal("mixed expansion spent restrictive reserve", err)
			}
			if _, err := ApplyS1(missing.State, s1Next(missing.State, s1Seal(missing.State.projection, mixed, 2, true))); !errors.Is(err, ErrControlReserve) {
				t.Fatal("mixed expansion admitted into reserve", err)
			}
			applied := s1Apply(t, missing.State, s1Seal(missing.State.projection, op, 3, true))
			if applied.Outcome.Disposition() != S1Applied || len(applied.State.ledger) != 2 {
				t.Fatal("purpose-bearing trust revocation cannot use reserve")
			}
			locked := image
			locked.Issuers = append([]Issuer(nil), image.Issuers...)
			locked.Issuers[0].EnvOwned = true
			lockedState := s1Fixture(t, locked)
			if _, err := AssessS1(lockedState.projection, s1Operation(t, lockedState.projection, admin, command)); !errors.Is(err, ErrBootstrapLocked) {
				t.Fatal("operator-owned issuer lock lost", err)
			}
		})
	}
}
