package security

import (
	"crypto/sha256"
	"testing"
	"time"
)

func TestCurrentCredentialReadOnlySessionCannotBecomeMutation(t *testing.T) {
	session := testSession()
	image := s1Image()
	image.Sessions = []Session{session}
	state := s1Fixture(t, image)
	clock, _ := fakeAuthorityTimeOwnerAt(t, session.CreatedAt.Add(time.Minute))
	view := CurrentCredentialView{projection: state.projection, clock: clock, browserOrigin: "https://admin.example"}
	facts := CurrentCredentialFacts{Session: &session, Origin: sha256.Sum256([]byte("lantern/authentication/browser-origin/v1\x00https://admin.example")), CSRF: [32]byte{1}, BrowserReadOnly: true}
	c, err := captureAuthorityUseCredential(view, facts, false)
	if err != nil || !c.browserReadOnly {
		t.Fatal("read-only bootstrap did not retain its limited evidence", err)
	}
	if _, err = captureAuthorityCredential(view, facts); err == nil {
		t.Fatal("read-only session bootstrapped a mutation credential")
	}
	facts.BrowserReadOnly = false
	if _, err = captureAuthorityCredential(view, facts); err != nil {
		t.Fatal("separately proven mutation session refused", err)
	}
}

func TestCurrentCredentialCapturesInstalledS1AndOriginalFacts(t *testing.T) {
	n, _ := authorityTestNativeCluster(t, 3)
	state, _, err := n.nodes[1].ReadLocalCut()
	if err != nil {
		t.Fatal(err)
	}
	clock, _ := fakeAuthorityTimeOwnerAt(t, time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC))
	view := CurrentCredentialView{projection: state.projection, clock: clock}
	h, _ := authorityTestHeader(t, n.fixture)
	event := *h.Credential.Token
	issuer, _ := view.Snapshot().Issuer(event.Identity.Issuer)
	event.Configuration = authorityIssuerCommitment(issuer, view.Cut().Generation)
	credential, err := captureAuthorityCredential(view, CurrentCredentialFacts{Token: &event})
	if err != nil || credential.claim.Token.AuthTime.Present || credential.authentication.Class != EndUser || credential.lineage != state.projection.lineage[event.Identity] || !credential.matches(state.projection) {
		t.Fatal("lost authentic S1 facts", err)
	}
	if credential.claim.Token.ExpiresAt != event.ExpiresAt || credential.claim.AdmissionDeadline != event.ExpiresAt.Time() {
		t.Fatal("source expiry collapsed into admission")
	}
	event.Identity.Subject = "changed after producer returned"
	if credential.claim.Token.Identity.Subject == event.Identity.Subject {
		t.Fatal("retained mutable producer alias")
	}
	stale := *state.projection
	stale.cut.Sequence++
	if credential.matches(&stale) {
		t.Fatal("credential rebound to another S1 cut")
	}
	if (CurrentCredentialView{}).Snapshot() != nil || (CurrentCredentialView{}).VerificationTime().Year() != 2262 {
		t.Fatal("zero view granted time/state")
	}
	clock.mu.Lock()
	clock.failed, clock.anchor = true, nil
	clock.mu.Unlock()
	if _, _, err := view.TimeBounds(); err == nil {
		t.Fatal("source loss used cached time")
	}
}

func TestCurrentCredentialRejectsIncorrectTrustAndClass(t *testing.T) {
	n, _ := authorityTestNativeCluster(t, 3)
	state, _, err := n.nodes[1].ReadLocalCut()
	if err != nil {
		t.Fatal(err)
	}
	clock, _ := fakeAuthorityTimeOwnerAt(t, time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC))
	view := CurrentCredentialView{projection: state.projection, clock: clock}
	h, _ := authorityTestHeader(t, n.fixture)
	base := *h.Credential.Token
	issuer, _ := view.Snapshot().Issuer(base.Identity.Issuer)
	base.Configuration = authorityIssuerCommitment(issuer, view.Cut().Generation)
	for name, edit := range map[string]func(*TokenAuthenticationEvidence){
		"issuer config":      func(e *TokenAuthenticationEvidence) { e.Configuration[0] ^= 1 },
		"revision":           func(e *TokenAuthenticationEvidence) { e.ConfigRevision++ },
		"generation":         func(e *TokenAuthenticationEvidence) { e.Generation[0] ^= 1 },
		"machine":            func(e *TokenAuthenticationEvidence) { e.Identity.Kind = MachinePrincipal },
		"unenrolled subject": func(e *TokenAuthenticationEvidence) { e.Identity.Subject += "-other" },
		"bare ID":            func(e *TokenAuthenticationEvidence) { e.Mode, e.Profile = "id", "oidc-id" },
		"nbf inside old leeway": func(e *TokenAuthenticationEvidence) {
			e.NotBefore = authorityNumericTime(time.Date(2026, 10, 9, 0, 0, 20, 0, time.UTC))
		},
	} {
		t.Run(name, func(t *testing.T) {
			event := base
			edit(&event)
			if _, err := captureAuthorityCredential(view, CurrentCredentialFacts{Token: &event}); err == nil {
				t.Fatal("unqualified source/class accepted")
			}
		})
	}
}

func TestCurrentCredentialPermitsMachineAndUnresolvedReadsOnly(t *testing.T) {
	now := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	clock, _ := fakeAuthorityTimeOwnerAt(t, now)
	image := s1Image()
	token, err := NewMachineToken()
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := MachineTokenDigest(token)
	machine := Identity{Kind: MachinePrincipal, MachineName: "status-worker"}
	image.Principals = append(image.Principals, Principal{Identity: machine, State: Active, Assignments: []RoleAssignment{{RoleID: "security_admin"}}})
	image.MachineCredentials = []MachineCredential{{Identity: machine, Digest: digest, CreatedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}}
	for i := range image.Principals {
		if image.Principals[i].Identity == s1Bob() {
			image.Principals[i].HumanIssuerConfigRevision = 0
			image.Principals[i].Assignments = []RoleAssignment{{RoleID: "security_admin"}}
		}
	}
	state := s1Fixture(t, image)
	view := CurrentCredentialView{projection: state.projection, clock: clock}
	facts := CurrentCredentialFacts{MachineDigest: digest}
	c, err := captureAuthorityUseCredential(view, facts, false)
	if err != nil || c.authentication.Class != MachineActor || !c.matches(state.projection) || !(authorityOutputRequirement{action: SecurityManage}).allows(state.projection, c) {
		t.Fatal("authorized machine reference denied", err)
	}
	if _, err := captureAuthorityCredential(view, facts); err == nil {
		t.Fatal("machine minted management credential")
	}
	f, _ := authorityTestFixture(t, 3)
	h, _ := authorityTestHeader(t, f)
	event := *h.Credential.Token
	event.Identity = s1Bob()
	issuer, _ := view.Snapshot().Issuer(event.Identity.Issuer)
	event.Configuration = authorityIssuerCommitment(issuer, view.Cut().Generation)
	c, err = captureAuthorityUseCredential(view, CurrentCredentialFacts{Token: &event}, false)
	if err != nil || c.authentication.Class != UnresolvedActor || !c.matches(state.projection) || !(authorityOutputRequirement{action: SecurityManage}).allows(state.projection, c) {
		t.Fatal("authorized unresolved reference denied", err)
	}
	if _, err := captureAuthorityCredential(view, CurrentCredentialFacts{Token: &event}); err == nil {
		t.Fatal("unresolved bearer minted management credential")
	}
}
