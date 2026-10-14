package security

import (
	"testing"
	"time"
)

func TestRegistrationCredentialNeverOrdinaryAdmission(t *testing.T) {
	for _, state := range []string{"unknown", "legacy", "registered", "suspended", "deleted"} {
		t.Run(state, func(t *testing.T) {
			image := registrationImage()
			actor := Identity{Kind: OIDCPrincipal, Issuer: GoogleIssuer, Subject: "new-user"}
			if state != "unknown" {
				p := Principal{Identity: actor, State: Active}
				if state == "registered" || state == "deleted" {
					p.UserUID = "11111111111111111111111111111111"
				}
				if state == "suspended" {
					p.State = Suspended
				}
				if state == "deleted" {
					p.State = Deleted
				}
				image.Principals = append(image.Principals, p)
			}
			s, view, event := registrationTestView(t, image)
			facts := CurrentCredentialFacts{Token: &event}
			if _, err := captureAuthorityCredential(view, facts); err == nil {
				t.Fatal("registration became ordinary mutation admission")
			}
			if _, err := captureAuthorityUseCredential(view, facts, false); err == nil {
				t.Fatal("registration became data admission")
			}
			c, err := captureRegistrationCredential(view, facts)
			if state == "suspended" || state == "deleted" {
				if err == nil {
					t.Fatal("inactive identity admitted")
				}
				return
			}
			if err != nil || !c.matches(s.projection) || c.state.Present != (state != "unknown") || c.claim.Token.AuthTime != event.AuthTime {
				t.Fatal("registration state/facts", err)
			}
			copy := event
			event.Identity.Subject = "changed"
			if c.claim.Token.Identity != copy.Identity {
				t.Fatal("producer alias escaped")
			}
			changed := *s.projection
			changed.cut.Sequence++
			if c.matches(&changed) {
				t.Fatal("registration crossed cut")
			}
		})
	}
}

func TestRegistrationCredentialCurrentEvidenceRefusals(t *testing.T) {
	_, view, event := registrationTestView(t, registrationImage())
	for name, edit := range map[string]func(*TokenAuthenticationEvidence){
		"ordinary login": func(e *TokenAuthenticationEvidence) {
			e.Version, e.Policy, e.Mode, e.Native.Registration = 2, "lantern-native-login-v1", "native-login", NativeRegistrationPurpose{}
		},
		"configuration":     func(e *TokenAuthenticationEvidence) { e.Configuration[0] ^= 1 },
		"profile":           func(e *TokenAuthenticationEvidence) { e.Native.ClientProfileID = "other" },
		"client commitment": func(e *TokenAuthenticationEvidence) { e.Native.Client[0] ^= 1 },
		"issuer revision":   func(e *TokenAuthenticationEvidence) { e.ConfigRevision++ },
		"generation":        func(e *TokenAuthenticationEvidence) { e.Generation[0] ^= 1 },
		"future signed time": func(e *TokenAuthenticationEvidence) {
			e.AuthTime = authorityNumericTime(view.VerificationTime().Add(time.Second))
		},
		"expired attempt": func(e *TokenAuthenticationEvidence) { e.Native.ExpiresAt = view.VerificationTime() },
		"future consume":  func(e *TokenAuthenticationEvidence) { e.Native.ConsumedAt = view.VerificationTime().Add(time.Second) },
	} {
		t.Run(name, func(t *testing.T) {
			e := event
			edit(&e)
			if _, err := captureRegistrationCredential(view, CurrentCredentialFacts{Token: &e}); err == nil {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
	for _, facts := range []CurrentCredentialFacts{{}, {Token: &event, BrowserReadOnly: true}, {Token: &event, Origin: [32]byte{1}}, {Token: &event, CSRF: [32]byte{1}}, {Token: &event, MachineDigest: "machine"}, {Token: &event, Session: &Session{}}} {
		if _, err := captureRegistrationCredential(view, facts); err == nil {
			t.Fatal("mixed source accepted")
		}
	}
	if _, err := captureRegistrationCredential(CurrentCredentialView{}, CurrentCredentialFacts{Token: &event}); err == nil {
		t.Fatal("zero current view accepted")
	}
}
