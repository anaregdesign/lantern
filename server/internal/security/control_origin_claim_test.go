package security

import (
	"strings"
	"testing"
	"time"
)

func TestAuthorityCredentialOriginalSourceFacts(t *testing.T) {
	for _, kind := range []string{"access", "code", "session"} {
		for _, date := range []string{"absent", "null", "ancient"} {
			t.Run(kind+"/"+date, func(t *testing.T) {
				f, keys := authorityTestFixture(t, 3)
				h, op := authorityTestHeader(t, f)
				c := &h.Credential
				e := c.Token
				low, _ := h.Time.utcTimes()
				if date == "null" {
					e.NotBefore = AuthenticationTime{Present: true}
				}
				if date == "ancient" {
					e.NotBefore = AuthenticationTime{Present: true, Numeric: true, Seconds: -800_000_000_000}
					e.AuthTime = e.NotBefore
				}
				c.Kind = kind
				switch kind {
				case "code":
					h.Authentication.Provenance = BrowserCode
					e.Mode, e.Profile, e.Nonce = "code", "oidc-id", [32]byte{7}
					e.Code = CodeAuthenticationEvidence{Flow: "login", Transaction: [32]byte{8}, Exchange: [32]byte{9}, Nonce: e.Nonce, PKCE: [32]byte{10}, CreatedAt: low.Add(-20 * time.Second), ConsumedAt: low.Add(-10 * time.Second), ExpiresAt: low.Add(time.Minute)}
					c.Enrollment, c.HumanNamespace = [32]byte{}, [32]byte{}
				case "session":
					c.Token = nil
					c.Session = &Session{CSRFDigest: strings.Repeat("b", 64), IssuerConfigRevision: 1, Digest: strings.Repeat("a", 64), Identity: op.actor, CreatedAt: low.Add(-time.Hour), ExpiresAt: low.Add(time.Hour)}
					h.Authentication.Provenance, h.Authentication.SessionDigest = BrowserCode, c.Session.Digest
					c.Enrollment, c.HumanNamespace = [32]byte{}, [32]byte{}
					c.Origin, c.CSRF = [32]byte{2}, [32]byte{3}
				}
				c.DerivedStart = authorityCredentialStart(*c)
				if c.Token != nil {
					c.Verification, _ = c.Token.Commitment()
				} else {
					c.Verification = s2cHash("current-native-session-v2", *c.Session)
				}
				raw := authorityTestSealHeader(t, f, keys[1], h, op)
				verified, err := verifyHistoricalH(f.trust, raw)
				if err != nil || verified.handoff.authorization.consume != h.ConsumeUpper {
					t.Fatal("source facts narrowed or fabricated", err)
				}
			})
		}
	}
}

func TestAuthorityCredentialRefusesChangedFacts(t *testing.T) {
	for name, edit := range map[string]func(*authorityHistoricalHeader){
		"machine":               func(h *authorityHistoricalHeader) { h.Authentication.Class = MachineActor },
		"unresolved":            func(h *authorityHistoricalHeader) { h.Authentication.Class = UnresolvedActor },
		"wrong actor":           func(h *authorityHistoricalHeader) { h.Credential.Token.Identity.Subject += "x" },
		"wrong generation":      func(h *authorityHistoricalHeader) { h.Credential.Token.Generation[0] ^= 1 },
		"wrong issuer revision": func(h *authorityHistoricalHeader) { h.Credential.Token.ConfigRevision++ },
		"unknown envelope":      func(h *authorityHistoricalHeader) { h.Credential.Kind = "jwt" },
		"missing enrollment":    func(h *authorityHistoricalHeader) { h.Credential.Enrollment = [32]byte{} },
		"missing namespace":     func(h *authorityHistoricalHeader) { h.Credential.HumanNamespace = [32]byte{} },
		"made up nbf":           func(h *authorityHistoricalHeader) { h.Credential.DerivedStart.Seconds++ },
		"future start": func(h *authorityHistoricalHeader) {
			h.Credential.Token.NotBefore = authorityNumericTime(h.ConsumeUpper.Add(time.Second))
			h.Credential.DerivedStart = h.Credential.Token.NotBefore
			h.Credential.Verification, _ = h.Credential.Token.Commitment()
		},
		"expiry equality": func(h *authorityHistoricalHeader) {
			h.Credential.AdmissionDeadline = h.ConsumeUpper
			h.CredentialDeadline = h.ConsumeUpper
		},
		"unexpected session": func(h *authorityHistoricalHeader) { h.Credential.Session = &Session{} },
	} {
		t.Run(name, func(t *testing.T) {
			f, keys := authorityTestFixture(t, 3)
			h, op := authorityTestHeader(t, f)
			edit(&h)
			if _, err := verifyHistoricalH(f.trust, authorityTestSealHeader(t, f, keys[1], h, op)); err == nil {
				t.Fatal("invalid credential attestation")
			}
		})
	}
}

func TestAuthorityPurposeOriginalPostReviewEvent(t *testing.T) {
	f, keys := authorityTestFixture(t, 3)
	base, op := authorityTestHeader(t, f)
	low, _ := base.Time.utcTimes()
	event := *base.Credential.Token
	event.Mode, event.Profile, event.Nonce = "code", "oidc-id", [32]byte{10}
	event.IssuedAt = authorityNumericTime(low.Add(-2 * time.Second))
	event.AuthTime = event.IssuedAt
	event.Code = CodeAuthenticationEvidence{Flow: "operation", AuthorizationID: [32]byte{3}, Transaction: [32]byte{4}, Exchange: [32]byte{5}, Nonce: event.Nonce, PKCE: [32]byte{6}, CreatedAt: low.Add(-3 * time.Second), ConsumedAt: low.Add(-time.Second), ExpiresAt: low.Add(time.Minute)}
	purpose := authorityPurposeClaim{Kind: "full-s1-operation", Binding: authorityPurposeBinding{base.ID, op.digest, op.reviewed, op.actor, 1, 1}, Authorization: event.Code.AuthorizationID, Challenge: [32]byte{7},
		ReviewAt: low.Add(-5 * time.Second), NotBefore: low.Add(-4 * time.Second), ApprovedAt: low.Add(-time.Second), ExpiresAt: low.Add(time.Minute), Event: event, Origin: base.OriginDigest, Serial: base.Serial}
	purpose.EventCommitment, _ = event.Commitment()
	for name, edit := range map[string]func(*authorityPurposeClaim){
		"valid": func(*authorityPurposeClaim) {},
		"old event": func(p *authorityPurposeClaim) {
			p.Event.AuthTime = authorityNumericTime(low.Add(-time.Hour))
			p.EventCommitment, _ = p.Event.Commitment()
		},
		"rebound cut":    func(p *authorityPurposeClaim) { p.Binding.Reviewed.Sequence++ },
		"rebound origin": func(p *authorityPurposeClaim) { p.Origin[0] ^= 1 },
		"rebound serial": func(p *authorityPurposeClaim) { p.Serial++ },
		"other approval": func(p *authorityPurposeClaim) { p.Authorization[0]++ },
		"generic step up": func(p *authorityPurposeClaim) {
			p.Event.Code.Flow = "step-up"
			p.Event.Code.AuthorizationID = [32]byte{}
			p.EventCommitment, _ = p.Event.Commitment()
		},
		"not original expiry": func(p *authorityPurposeClaim) {
			p.ExpiresAt = low.Add(time.Hour)
			p.Event.ExpiresAt = authorityNumericTime(low.Add(time.Second))
			p.EventCommitment, _ = p.Event.Commitment()
		},
	} {
		t.Run(name, func(t *testing.T) {
			h, p := base, purpose
			edit(&p)
			h.Purpose, h.PurposeDeadline = &p, p.ExpiresAt
			_, err := verifyHistoricalH(f.trust, authorityTestSealHeader(t, f, keys[1], h, op))
			if (name == "valid") != (err == nil) {
				t.Fatal(name, err)
			}
		})
	}
}
