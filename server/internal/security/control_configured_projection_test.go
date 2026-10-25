package security

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

func configuredProjectionFixture(t *testing.T, alternate bool) *S1Projection {
	t.Helper()
	image := testImage()
	image.Issuers[0].EnvOwned = true
	image.Sessions = []Session{testSession()}
	if alternate {
		issuer := image.Issuers[0].clone()
		issuer.URL, issuer.EnvOwned = "https://alternate.example", false
		image.Issuers = append(image.Issuers, issuer)
		image.Principals = append(image.Principals, Principal{Identity: Identity{Kind: OIDCPrincipal, Issuer: issuer.URL, Subject: "other-admin"}, State: Active,
			HumanIssuerConfigRevision: 1, Assignments: []RoleAssignment{{RoleID: "security_admin"}}})
	}
	return s1Fixture(t, image).projection
}

func configuredIntent(t *testing.T, issuers ...Issuer) *ConfiguredTrust {
	t.Helper()
	intent, err := NewConfiguredTrust(issuers)
	if err != nil {
		t.Fatal(err)
	}
	return intent
}

func TestConfiguredTrustCanonicalPublicIntent(t *testing.T) {
	first := testImage().Issuers[0]
	first.Algorithms = []string{"PS256", "RS256"}
	second := first.clone()
	second.URL = "https://second.example"
	one := configuredIntent(t, first, second)
	first.ConfigRevision, first.EnvOwned = 400, true
	first.Algorithms = []string{"RS256", "PS256"}
	first.NativeClients = []NativeClient{}
	two := configuredIntent(t, second, first)
	if one.Digest() == [32]byte{} || one.Digest() != two.Digest() {
		t.Fatal("state metadata or set ordering changed public semantics")
	}
	first.Algorithms[0] = "EdDSA"
	if one.Digest() != two.Digest() || !one.valid() {
		t.Fatal("caller aliases intent")
	}
	for _, issuers := range [][]Issuer{
		{second, second},
		{{URL: second.URL}},
		{func() Issuer { i := second.clone(); i.Deleted = true; return i }()},
		{func() Issuer { i := second.clone(); i.SecretRef = strings.Repeat("x", 65); return i }()},
		{func() Issuer { i := second.clone(); i.Algorithms = []string{"RS256", "RS256"}; return i }()},
		make([]Issuer, MaxIssuers+1),
	} {
		if _, err := NewConfiguredTrust(issuers); err == nil {
			t.Fatal("malformed configured intent accepted")
		}
	}
	if (&ConfiguredTrust{}).valid() || (*ConfiguredTrust)(nil).Digest() != [32]byte{} {
		t.Fatal("zero intent became a capability")
	}
}

func TestConfiguredTrustAddAndIdempotence(t *testing.T) {
	base := configuredProjectionFixture(t, true)
	old := base.Image()
	added := old.Issuers[0].clone()
	added.URL, added.ConfigRevision, added.EnvOwned = "https://new.example", 900, false
	desired := configuredIntent(t, old.Issuers[0], added)
	next, err := assessConfiguredTrust(base, desired)
	if err != nil {
		t.Fatal(err)
	}
	image := next.Image()
	if image.ConfiguredTrustRevision != 1 || !validHexDigest(image.ConfiguredTrustDigest) || next.cut.Sequence != base.cut.Sequence+1 || next.cut.Previous != s1Digest("cut", base.cut) {
		t.Fatal("missing retained history or cut progression")
	}
	if !reflect.DeepEqual(image.Principals, old.Principals) || !reflect.DeepEqual(image.Roles, old.Roles) || !reflect.DeepEqual(image.Sessions, old.Sessions) ||
		image.BootstrapRevision != old.BootstrapRevision || image.BootstrapDigest != old.BootstrapDigest {
		t.Fatal("trust addition changed enrollment, grants or historical seed")
	}
	issuer, known := next.snapshot.Issuer(added.URL)
	if !known || !issuer.EnvOwned || issuer.ConfigRevision != 1 || next.SessionLineage(testIdentity()) != base.SessionLineage(testIdentity()) {
		t.Fatal("new issuer inherited caller revision or changed unrelated lineage")
	}
	if current, err := assessConfiguredTrust(next, desired); err != nil || current != next {
		t.Fatal("identical intent was not exact no-op", err)
	}
	if !reflect.DeepEqual(base.Image(), old) {
		t.Fatal("planner mutated predecessor")
	}
	// Config must not capture a trust record owned by normal management.
	if _, err := assessConfiguredTrust(next, configuredIntent(t, old.Issuers[0], old.Issuers[1], added)); !errors.Is(err, ErrBootstrapLocked) {
		t.Fatal("management-owned issuer appropriated", err)
	}
}

func TestConfiguredTrustAdoptionDoesNotRotateEquivalentIssuer(t *testing.T) {
	base := configuredProjectionFixture(t, false)
	issuer := base.Image().Issuers[0]
	issuer.NativeClients = []NativeClient{}
	next, err := assessConfiguredTrust(base, configuredIntent(t, issuer))
	if err != nil {
		t.Fatal(err)
	}
	if next.Image().Issuers[0].ConfigRevision != issuer.ConfigRevision || next.Image().Sessions[0].Revoked ||
		next.SessionLineage(testIdentity()) != base.SessionLineage(testIdentity()) {
		t.Fatal("equivalent optional empty profile rotated trust or revoked sessions")
	}
}

func TestConfiguredTrustChangeWithdrawAndRetainedIdentity(t *testing.T) {
	base := configuredProjectionFixture(t, true)
	old := base.Image()
	changed := old.Issuers[0].clone()
	changed.APIAudience = "new-api-audience"
	next, err := assessConfiguredTrust(base, configuredIntent(t, changed))
	if err != nil {
		t.Fatal(err)
	}
	image := next.Image()
	if image.Issuers[0].ConfigRevision != 2 || image.Sessions[0].Revoked != true || next.SessionLineage(testIdentity()) != 2 ||
		!reflect.DeepEqual(image.Principals, old.Principals) || !reflect.DeepEqual(image.Roles, old.Roles) || !reflect.DeepEqual(image.Issuers[1], old.Issuers[1]) {
		t.Fatal("issuer change rewrote enrollment/grants or failed to revoke sessions")
	}
	// The original seed enrollment remains historical; it is not re-created or
	// upgraded to the new Issuer.ConfigRevision by configuration reconciliation.
	if image.Principals[0].HumanIssuerConfigRevision != 1 || !next.snapshot.HumanIdentity(testIdentity()) {
		t.Fatal("seed enrollment changed")
	}
	withdrawn, err := assessConfiguredTrust(next, configuredIntent(t))
	if err != nil {
		t.Fatal(err)
	}
	tombstone := withdrawn.Image().Issuers[0]
	if tombstone.Enabled || !tombstone.Deleted || tombstone.ConfigRevision != 3 || !tombstone.EnvOwned || withdrawn.SessionLineage(testIdentity()) != 3 ||
		!reflect.DeepEqual(withdrawn.Image().Principals, old.Principals) {
		t.Fatal("withdrawal dropped history or identities")
	}
	// A later changed desired set must not repeatedly advance an existing
	// tombstone merely because it remains absent from the supplied intent.
	added := changed.clone()
	added.URL = "https://later.example"
	later, err := assessConfiguredTrust(withdrawn, configuredIntent(t, added))
	if err != nil || later.Image().Issuers[0].ConfigRevision != 3 || later.SessionLineage(testIdentity()) != 3 {
		t.Fatal("retained tombstone repeatedly changed", err)
	}
	if _, err := assessConfiguredTrust(withdrawn, configuredIntent(t, changed)); !errors.Is(err, ErrPermissionDenied) {
		t.Fatal("re-enabling seed trust revived effective administrator grants", err)
	}
}

func TestConfiguredTrustInvariantsAndOverflow(t *testing.T) {
	base := configuredProjectionFixture(t, false)
	if _, err := assessConfiguredTrust(base, configuredIntent(t)); !errors.Is(err, ErrLastAdministrator) {
		t.Fatal("last usable administrator withdrawn", err)
	}
	for _, tc := range []struct {
		name string
		edit func(*S1Projection)
	}{
		{"sequence", func(p *S1Projection) { p.cut.Sequence = math.MaxUint64 }},
		{"lineage", func(p *S1Projection) { p.lineage[testIdentity()] = math.MaxUint64 }},
		{"issuer", func(p *S1Projection) {
			i := p.Image()
			i.Issuers[0].ConfigRevision = math.MaxUint64
			p.snapshot, _ = CompileImage(i, DefaultPolicyLimits())
		}},
		{"configured revision", func(p *S1Projection) {
			i := p.Image()
			i.ConfiguredTrustRevision, i.ConfiguredTrustDigest = math.MaxUint64, strings.Repeat("f", 64)
			p.snapshot, _ = CompileImage(i, DefaultPolicyLimits())
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := configuredProjectionFixture(t, true)
			tc.edit(p)
			issuer := p.Image().Issuers[0].clone()
			issuer.APIAudience = "changed"
			if _, err := assessConfiguredTrust(p, configuredIntent(t, issuer)); !errors.Is(err, ErrS1Contract) {
				t.Fatal("overflow accepted", err)
			}
		})
	}
	if _, err := assessConfiguredTrust(nil, configuredIntent(t)); !errors.Is(err, ErrS1Contract) {
		t.Fatal("nil predecessor accepted", err)
	}
}

func TestConfiguredTrustReenableDoesNotReviveDataGrant(t *testing.T) {
	base := configuredProjectionFixture(t, true)
	image := base.Image()
	readerIssuer := image.Issuers[0].clone()
	readerIssuer.URL, readerIssuer.Enabled = "https://reader.example", false
	image.Issuers = append(image.Issuers, readerIssuer)
	reader := Identity{Kind: OIDCPrincipal, Issuer: readerIssuer.URL, Subject: "reader"}
	image.Principals = append(image.Principals, Principal{Identity: reader, State: Active, HumanIssuerConfigRevision: 1, Assignments: []RoleAssignment{{RoleID: "reader"}}})
	base = s1Fixture(t, image).projection
	readerIssuer.Enabled = true
	if _, err := assessConfiguredTrust(base, configuredIntent(t, image.Issuers[0], readerIssuer)); !errors.Is(err, ErrPermissionDenied) {
		t.Fatal("old data grants revived", err)
	}
	image.Principals[len(image.Principals)-1].State = Suspended
	base = s1Fixture(t, image).projection
	if _, err := assessConfiguredTrust(base, configuredIntent(t, image.Issuers[0], readerIssuer)); err != nil {
		t.Fatal("suspended identity blocked trust-only re-enable", err)
	}
}

func TestConfiguredTrustPreservesApplicationSecurityState(t *testing.T) {
	image := configuredProjectionFixture(t, true).Image()
	image.Principals[0].UserUID = strings.Repeat("1", 32)
	for n, state := range []PrincipalState{Active, Suspended, Deleted} {
		identity := testIdentity()
		identity.Subject = []string{"member", "suspended", "deleted"}[n]
		principal := Principal{Identity: identity, State: state, UserUID: strings.Repeat([]string{"2", "3", "4"}[n], 32), HumanIssuerConfigRevision: 1}
		if state != Deleted {
			principal.Assignments = []RoleAssignment{{RoleID: "reader"}}
		}
		image.Principals = append(image.Principals, principal)
	}
	machine := Identity{Kind: MachinePrincipal, MachineName: "worker"}
	image.Principals = append(image.Principals, Principal{Identity: machine, State: Active, Assignments: []RoleAssignment{{RoleID: "reader"}}})
	session := testSession()
	image.MachineCredentials = []MachineCredential{{Identity: machine, Digest: strings.Repeat("b", 64), CreatedAt: session.CreatedAt, ExpiresAt: session.ExpiresAt}}
	google := registrationImage().Issuers[1]
	image.Issuers = append(image.Issuers, google)
	image.Roles = append(image.Roles, PersonalUserRole())
	image.RegistrationPolicy = &RegistrationPolicy{Version: RegistrationPolicyVersion, Enabled: true, RoleID: PersonalUserRoleID,
		Profiles: []RegistrationProfile{{Issuer: GoogleIssuer, ClientProfileID: "android"}}}
	base := s1Fixture(t, image).projection
	issuer := image.Issuers[0].clone()
	issuer.APIAudience = "changed"
	next, err := assessConfiguredTrust(base, configuredIntent(t, issuer))
	if err != nil {
		t.Fatal(err)
	}
	got := next.Image()
	if !reflect.DeepEqual(got.Principals, image.Principals) || !reflect.DeepEqual(got.Roles, image.Roles) ||
		!reflect.DeepEqual(got.MachineCredentials, image.MachineCredentials) || !reflect.DeepEqual(got.RegistrationPolicy, image.RegistrationPolicy) ||
		!reflect.DeepEqual(got.Issuers[1:], image.Issuers[1:]) {
		t.Fatal("configuration rewrote enrollment, UID, state, assignments, Roles, machines, registration or management trust")
	}
	member := testIdentity()
	member.Subject = "member"
	if next.snapshot.HumanIdentity(member) || got.Principals[2].HumanIssuerConfigRevision != 1 {
		t.Fatal("configuration silently upgraded ordinary human enrollment")
	}
	if !got.Sessions[0].Revoked || next.SessionLineage(member) != base.SessionLineage(member)+1 {
		t.Fatal("issuer-scoped revocation did not cover enrolled subjects")
	}
}
