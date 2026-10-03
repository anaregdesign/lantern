package security

import (
	"strings"
	"testing"
	"time"
)

func TestSnapshotIdentityAndOwnership(t *testing.T) {
	image := testImage()
	reader := testIdentity()
	reader.Subject = "user1"
	image.Principals = append(image.Principals, Principal{Identity: reader, State: Active, Assignments: []RoleAssignment{{RoleID: "reader"}}})
	unassigned := testIdentity()
	unassigned.Subject = "unassigned"
	image.Principals = append(image.Principals, Principal{Identity: unassigned, State: Active})
	snapshot, err := CompileImage(image, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	access, active := snapshot.AccessFor(reader)
	if !active || !access.Allows(VertexRead, "orders:1") || access.AllowsGlobal(SecurityManage) {
		t.Fatal("reader scope incorrect")
	}
	for _, unknown := range []Identity{
		{Kind: OIDCPrincipal, Issuer: reader.Issuer + "/", Subject: reader.Subject},
		{Kind: OIDCPrincipal, Issuer: reader.Issuer, Subject: "User1"},
		{Kind: MachinePrincipal, MachineName: reader.Subject},
	} {
		if _, active := snapshot.AccessFor(unknown); active {
			t.Fatal("identity implicitly linked")
		}
	}
	noGrants, active := snapshot.AccessFor(unassigned)
	if !active || noGrants.Allows(VertexRead, "orders:1") || noGrants.AllowsGlobal(SecurityManage) {
		t.Fatal("implicit user grant")
	}
	// Inputs and read copies are detached from installed state.
	image.Principals[1].State = Suspended
	image.Issuers[0].Algorithms[0] = "none"
	copy := snapshot.Image()
	copy.Principals[1].Assignments[0].RoleID = "security_admin"
	issuer, known := snapshot.Issuer(reader.Issuer)
	if !known {
		t.Fatal("Issuer missing")
	}
	issuer.Algorithms[0] = "none"
	issuer, _ = snapshot.Issuer(reader.Issuer)
	if issuer.Algorithms[0] != "RS256" {
		t.Fatal("Issuer alias escaped")
	}
	access, active = snapshot.AccessFor(reader)
	if !active || access.AllowsGlobal(SecurityManage) {
		t.Fatal("snapshot mutated")
	}
	var empty *Snapshot
	if _, active := empty.AccessFor(reader); active {
		t.Fatal("nil snapshot grants")
	}
}

func TestSnapshotSessionLifecycle(t *testing.T) {
	image := testImage()
	session := testSession()
	image.Sessions = []Session{session}
	for _, test := range []struct {
		name   string
		change func(*Image)
		now    time.Time
		want   bool
	}{
		{"active", func(*Image) {}, session.CreatedAt, true},
		{"expiry boundary", func(*Image) {}, session.ExpiresAt, false},
		{"before issuance", func(*Image) {}, session.CreatedAt.Add(-time.Nanosecond), false},
		{"revoked", func(i *Image) { i.Sessions[0].Revoked = true }, session.CreatedAt, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := testImage()
			candidate.Sessions = []Session{session}
			test.change(&candidate)
			snapshot, err := CompileImage(candidate, DefaultPolicyLimits())
			if err != nil {
				t.Fatal(err)
			}
			_, authTime, active := snapshot.SessionAccess(session.Digest, test.now)
			if active != test.want || active && !authTime.Equal(session.AuthTime) {
				t.Fatalf("session active=%v", active)
			}
		})
	}
	snapshot, err := CompileImage(image, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, active := snapshot.SessionAccess(strings.Repeat("b", 64), session.CreatedAt); active {
		t.Fatal("unknown session admitted")
	}
}

func TestSnapshotReusesUnchangedRoles(t *testing.T) {
	first, err := CompileImage(testImage(), DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	image := testImage()
	image.Sessions = []Session{testSession()}
	next, err := compileImage(image, DefaultPolicyLimits(), first)
	if err != nil {
		t.Fatal(err)
	}
	if next.policy != first.policy {
		t.Fatal("Session churn rebuilt Role ranges")
	}
	image.Roles[1].Rules[0] = dataRule(Allow, VertexRead, "users:")
	changed, err := compileImage(image, DefaultPolicyLimits(), next)
	if err != nil {
		t.Fatal(err)
	}
	if changed.policy == first.policy {
		t.Fatal("changed Role reused old policy")
	}
	// JSON substitutes malformed UTF-8. It must not allow invalid input to hit
	// a valid policy's canonical cache key.
	image.Roles[1].Rules[0] = dataRule(Allow, VertexRead, "\ufffd")
	validUnicode, err := CompileImage(image, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	image.Roles[1].Rules[0] = dataRule(Allow, VertexRead, "\xff")
	if _, err := compileImage(image, DefaultPolicyLimits(), validUnicode); err == nil {
		t.Fatal("malformed UTF8 matched cached policy")
	}
}
