package security

import (
	"crypto/sha256"
	"strings"
	"testing"
	"time"
)

func TestCurrentNativeSessionPolicyAndProfileBoundaries(t *testing.T) {
	now := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	client := NativeClient{ID: "mobile", Kind: GoogleIDToken, ClientID: "backend", ServiceOrigin: "https://lantern.example"}
	issuer := Issuer{URL: GoogleIssuer, Enabled: true, ConfigRevision: 1, Algorithms: []string{"RS256"}, NativeClients: []NativeClient{client}}
	identity := Identity{Kind: OIDCPrincipal, Issuer: GoogleIssuer, Subject: "existing-native-user"}
	base := s1Image()
	base.Issuers = append(base.Issuers, issuer)
	base.Principals = append(base.Principals, Principal{Identity: identity, State: Active, Assignments: []RoleAssignment{{RoleID: "reader"}}})
	session := Session{Profile: NativeEndUserProfile, ClientProfileID: client.ID, ServiceOrigin: client.ServiceOrigin, IssuerConfigRevision: 1, Digest: strings.Repeat("c", 64), Identity: identity, CreatedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}
	base.Sessions = []Session{session}
	state := s1Fixture(t, base)
	clock, _ := fakeAuthorityTimeOwnerAt(t, now)
	view := CurrentCredentialView{projection: state.projection, clock: clock, browserOrigin: "https://admin.example"}
	facts := CurrentCredentialFacts{Session: &session}
	c, err := captureAuthorityCredential(view, facts)
	if err != nil || c.authentication.Provenance != NativeEndUserSession || c.authentication.Class != EndUser || c.browserReadOnly || c.claim.Origin != [32]byte{} || !c.matches(state.projection) {
		t.Fatal("native session classification", err)
	}
	if c.claim.AdmissionDeadline != session.ExpiresAt || !session.AuthTime.IsZero() {
		t.Fatal("sliding expiry or fabricated authentication time")
	}

	t.Run("wide interval fits actual credential window", func(t *testing.T) {
		saved := clock.anchor.utc
		defer func() { clock.anchor.utc = saved }()
		clock.anchor.utc = authorityTimeRange{uint64(now.Add(-4 * time.Second).UnixNano()), uint64(now.Add(4 * time.Second).UnixNano())}
		if _, err := captureAuthorityCredential(view, facts); err != nil {
			t.Fatal("5s refresh goal became an admission gate", err)
		}
		clock.anchor.utc.high = uint64(session.ExpiresAt.UnixNano())
		if _, err := captureAuthorityCredential(view, facts); err == nil {
			t.Fatal("upper equality expiry accepted")
		}
	})
	for _, expiredAt := range []time.Time{session.ExpiresAt, session.ExpiresAt.Add(time.Hour)} {
		expired := view
		expired.clock, _ = fakeAuthorityTimeOwnerAt(t, expiredAt)
		if _, err := captureAuthorityUseCredential(expired, facts, false); err == nil {
			t.Fatal("absolute native expiry was renewed by reuse")
		}
	}
	for _, mutate := range []func(*CurrentCredentialFacts){func(f *CurrentCredentialFacts) { f.Origin = [32]byte{1} }, func(f *CurrentCredentialFacts) { f.CSRF = [32]byte{1} }, func(f *CurrentCredentialFacts) { f.BrowserReadOnly = true }} {
		f := facts
		mutate(&f)
		if _, err := captureAuthorityUseCredential(view, f, false); err == nil {
			t.Fatal("browser proof substituted for native session")
		}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Image)
	}{
		{"revocation", func(i *Image) { i.Sessions[0].Revoked = true }},
		{"suspension", func(i *Image) { i.Principals[len(i.Principals)-1].State = Suspended }},
		{"issuer disable", func(i *Image) { i.Issuers[len(i.Issuers)-1].Enabled = false }},
		{"issuer revision", func(i *Image) { i.Issuers[len(i.Issuers)-1].ConfigRevision++ }},
		{"profile removal", func(i *Image) { i.Issuers[len(i.Issuers)-1].NativeClients[0].ID = "other" }},
		{"service origin", func(i *Image) {
			i.Issuers[len(i.Issuers)-1].NativeClients[0].ServiceOrigin = "https://different.example"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			image := state.projection.snapshot.Image()
			tc.mutate(&image)
			changed := s1Fixture(t, image)
			v := view
			v.projection = changed.projection
			candidate, _ := v.Snapshot().Session(session.Digest)
			if _, err := captureAuthorityUseCredential(v, CurrentCredentialFacts{Session: &candidate}, false); err == nil {
				t.Fatal("changed current policy accepted")
			}
		})
	}
}

func TestCurrentNativeLoginEventIsNotBrowserCode(t *testing.T) {
	now := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	client := NativeClient{ID: "mobile", Kind: GoogleIDToken, ClientID: "backend", ServiceOrigin: "https://lantern.example"}
	issuer := Issuer{URL: GoogleIssuer, Enabled: true, ConfigRevision: 1, Algorithms: []string{"RS256"}, NativeClients: []NativeClient{client}}
	identity := Identity{Kind: OIDCPrincipal, Issuer: GoogleIssuer, Subject: "existing"}
	image := s1Image()
	image.Issuers = append(image.Issuers, issuer)
	image.Principals = append(image.Principals, Principal{Identity: identity, State: Active})
	state := s1Fixture(t, image)
	clock, _ := fakeAuthorityTimeOwnerAt(t, now)
	view := CurrentCredentialView{projection: state.projection, clock: clock}
	e := TokenAuthenticationEvidence{Version: 2, Policy: "lantern-native-login-v1", Mode: "native-login", Profile: NativeEndUserProfile, Identity: identity, Algorithm: "RS256", KeyID: "key", Credential: [32]byte{1}, Key: [32]byte{2}, Configuration: authorityIssuerCommitment(issuer, view.Cut().Generation), AudienceClient: [32]byte{3}, Generation: view.Cut().Generation, ConfigRevision: 1, IssuedAt: authorityNumericTime(now.Add(-time.Minute)), ExpiresAt: authorityNumericTime(now.Add(time.Hour)), Native: NativeAuthenticationEvidence{Kind: GoogleIDToken, ClientProfileID: client.ID, ServiceOrigin: client.ServiceOrigin, NoncePolicy: "google-initialize-unbound", Client: NativeClientCommitment(client), Transaction: [32]byte{4}, CreatedAt: now.Add(-time.Minute), ConsumedAt: now.Add(-time.Second), ExpiresAt: now.Add(9 * time.Minute)}}
	c, err := captureAuthorityCredential(view, CurrentCredentialFacts{Token: &e})
	if err != nil || c.authentication.Provenance != NativeProviderLogin || c.claim.AdmissionDeadline != e.Native.ExpiresAt || c.claim.Token.AuthTime.Present {
		t.Fatal("native event contract", err)
	}
	for _, mutate := range []func(*TokenAuthenticationEvidence){func(e *TokenAuthenticationEvidence) { e.Native.Client[0] ^= 1 }, func(e *TokenAuthenticationEvidence) { e.Native.ConsumedAt = now.Add(time.Second) }, func(e *TokenAuthenticationEvidence) { e.Identity.Subject = "unregistered" }, func(e *TokenAuthenticationEvidence) { e.Mode = "code" }, func(e *TokenAuthenticationEvidence) { e.Native.ExpiresAt = now }} {
		bad := e
		mutate(&bad)
		if _, err := captureAuthorityCredential(view, CurrentCredentialFacts{Token: &bad}); err == nil {
			t.Fatal("invalid native event accepted")
		}
	}
}

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
