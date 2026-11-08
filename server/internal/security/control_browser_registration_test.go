package security

import (
	"context"
	"testing"
	"time"
)

func browserRegistrationImage() Image {
	image := registrationImage()
	image.Issuers[1].ClientID = "browser-client"
	image.Issuers[1].RedirectURI = "https://admin.example/auth/callback"
	image.RegistrationPolicy.Profiles = []RegistrationProfile{{Issuer: GoogleIssuer, ClientProfileID: BrowserRegistrationProfileID, Kind: BrowserRegistrationProfile}}
	return image
}

func browserRegistrationEvent(view CurrentCredentialView, actor Identity) (TokenAuthenticationEvidence, error) {
	e, err := registrationTestEvent(view, actor, NativeRegistrationPurpose{Kind: "registration"})
	if err != nil {
		return e, err
	}
	low, _, err := view.TimeBounds()
	if err != nil {
		return e, err
	}
	e.Version, e.Policy, e.Mode, e.Profile = 1, "lantern-oidc-v1", "code", "oidc-id"
	e.Native = NativeAuthenticationEvidence{}
	e.Nonce = [32]byte{7}
	e.Code = CodeAuthenticationEvidence{Flow: "login", Transaction: [32]byte{8}, Exchange: [32]byte{9}, Nonce: e.Nonce, PKCE: [32]byte{10}, CreatedAt: low.Add(-time.Minute), ConsumedAt: low.Add(-time.Second), ExpiresAt: low.Add(9 * time.Minute)}
	return e, nil
}

func TestBrowserRegistrationRequiresClosedCurrentCodeProof(t *testing.T) {
	_, view, _ := registrationTestView(t, browserRegistrationImage())
	actor := Identity{Kind: OIDCPrincipal, Issuer: GoogleIssuer, Subject: "browser-first-login"}
	event, err := browserRegistrationEvent(view, actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := captureAuthorityUseCredential(view, CurrentCredentialFacts{Token: &event}, false); err == nil {
		t.Fatal("unknown browser identity acquired ordinary admission")
	}
	c, err := captureRegistrationCredential(view, CurrentCredentialFacts{Token: &event})
	if err != nil || c.claim.Kind != "browser-registration" {
		t.Fatal("consumed Code proof could not enter restricted registration", err)
	}
	for name, edit := range map[string]func(*TokenAuthenticationEvidence){
		"raw id token":          func(e *TokenAuthenticationEvidence) { e.Mode, e.Code = "id", CodeAuthenticationEvidence{} },
		"purpose callback":      func(e *TokenAuthenticationEvidence) { e.Code.Flow, e.Code.AuthorizationID = "operation", [32]byte{1} },
		"step-up callback":      func(e *TokenAuthenticationEvidence) { e.Code.Flow = "step-up" },
		"wrong issuer revision": func(e *TokenAuthenticationEvidence) { e.ConfigRevision++ },
		"missing nonce":         func(e *TokenAuthenticationEvidence) { e.Nonce = [32]byte{} },
		"both nonces absent":    func(e *TokenAuthenticationEvidence) { e.Nonce, e.Code.Nonce = [32]byte{}, [32]byte{} },
		"missing exchange":      func(e *TokenAuthenticationEvidence) { e.Code.Exchange = [32]byte{} },
		"missing created time":  func(e *TokenAuthenticationEvidence) { e.Code.CreatedAt = time.Time{} },
		"expired transaction":   func(e *TokenAuthenticationEvidence) { e.Code.ExpiresAt = view.VerificationTime() },
		"future consume":        func(e *TokenAuthenticationEvidence) { e.Code.ConsumedAt = view.VerificationTime().Add(time.Second) },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := event
			edit(&candidate)
			if _, err := captureRegistrationCredential(view, CurrentCredentialFacts{Token: &candidate}); err == nil {
				t.Fatal("invalid browser registration proof accepted")
			}
		})
	}
}

func TestBrowserRegistrationAtomicPolicyAndIdentityScope(t *testing.T) {
	image := browserRegistrationImage()
	actor := Identity{Kind: OIDCPrincipal, Issuer: GoogleIssuer, Subject: "browser-first-login"}
	command := &PrincipalRegistration{UserUID: "11111111111111111111111111111111", ProfileKind: BrowserRegistrationProfile, ClientProfileID: BrowserRegistrationProfileID, IssuerConfigRevision: 1}
	if err := applyPrincipalRegistration(&image, actor, command); err != nil {
		t.Fatal(err)
	}
	snapshot, err := CompileImage(image, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	uid, prefix, known := snapshot.PersonalArea(actor)
	access, active := snapshot.AccessFor(actor)
	if !known || !active || uid != command.UserUID || !access.Allows(VertexWrite, prefix+"profile") || access.Allows(VertexRead, "users:other:profile") || access.AllowsGlobal(SecurityManage) {
		t.Fatal("browser registration widened identity or permission scope", err)
	}
	for name, edit := range map[string]func(*Image){
		"OFF": func(i *Image) { i.RegistrationPolicy.Enabled = false },
		"native only": func(i *Image) {
			i.RegistrationPolicy.Profiles = []RegistrationProfile{{Issuer: GoogleIssuer, ClientProfileID: "android", Kind: NativeRegistrationProfile}}
		},
		"no browser profile":   func(i *Image) { i.Issuers[1].ClientID, i.Issuers[1].RedirectURI = "", "" },
		"missing joining Role": func(i *Image) { i.Roles = i.Roles[:len(i.Roles)-1] },
		"deleted identity":     func(i *Image) { i.Principals = append(i.Principals, Principal{Identity: actor, State: Deleted}) },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := browserRegistrationImage()
			edit(&candidate)
			before := len(candidate.Principals)
			if applyPrincipalRegistration(&candidate, actor, command) == nil || len(candidate.Principals) != before {
				t.Fatal("refused browser registration created partial Principal/grants")
			}
		})
	}
}

func TestBrowserRegistrationDurableFirstLoginAndRepeatIsUnchanged(t *testing.T) {
	image := browserRegistrationImage()
	_, owners, _ := authorityTestCompositeImage(t, 2, &image)
	registrationTestRenew(t, owners)
	o := &CurrentAuthority{origin: owners[1]}
	actor := Identity{Kind: OIDCPrincipal, Issuer: GoogleIssuer, Subject: "browser-first-login"}
	var event *TokenAuthenticationEvidence
	producer := authorityPublicProducerFunc(func(_ context.Context, view CurrentCredentialView) (CurrentCredentialFacts, error) {
		if event == nil {
			e, err := browserRegistrationEvent(view, actor)
			if err != nil {
				return CurrentCredentialFacts{}, err
			}
			event = &e
		}
		return CurrentCredentialFacts{Token: event}, nil
	})
	if err := o.EnrollBrowser(s3aTestContext(t), producer); err != nil {
		t.Fatal("durable browser enrollment", err)
	}
	registrationTestRenew(t, owners)
	before := o.origin.network.kernel.replayState.projection.Image()
	uid, _, registered := o.origin.network.kernel.replayState.projection.snapshot.PersonalArea(actor)
	if !registered || len(before.Sessions) != 0 {
		t.Fatal("registration did not precede separate session issuance")
	}
	if err := o.EnrollBrowser(s3aTestContext(t), producer); err != nil {
		t.Fatal("repeat known login", err)
	}
	after := o.origin.network.kernel.replayState.projection.Image()
	if len(before.Principals) != len(after.Principals) || o.origin.network.kernel.replayState.projection.snapshot.principals[actor].userUID != uid {
		t.Fatal("repeated browser callback created duplicate identity")
	}
}
