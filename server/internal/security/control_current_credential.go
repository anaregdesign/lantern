package security

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"time"
)

// CurrentCredentialView is supplied only by the private composite owner from
// its installed, authenticated B state. Its zero value is unusable. An adapter
// must not relabel a fixed-writer Revision or a pure genesis as this view.
type CurrentCredentialView struct {
	projection    *S1Projection
	clock         *authorityTimeOwner
	browserOrigin string
}

func (v CurrentCredentialView) Snapshot() *Snapshot {
	if v.projection == nil || v.clock == nil {
		return nil
	}
	return v.projection.snapshot
}
func (v CurrentCredentialView) Cut() SemanticCut {
	if v.Snapshot() == nil {
		return SemanticCut{}
	}
	return v.projection.cut
}
func (v CurrentCredentialView) BrowserOrigin() string { return v.browserOrigin }
func (v CurrentCredentialView) TimeBounds() (time.Time, time.Time, error) {
	if v.Snapshot() == nil {
		return time.Time{}, time.Time{}, ErrAuthorityUnavailable
	}
	s, err := v.clock.current()
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return time.Unix(0, int64(s.utc.low)).UTC(), time.Unix(0, int64(s.utc.high)).UTC(), nil
}

// Used by the actual verifier, whose existing clock port returns a scalar.
// Final current admission separately checks both interval endpoints. Failure
// never falls back to the host wall clock or an old cached sample.
func (v CurrentCredentialView) VerificationTime() time.Time {
	_, high, err := v.TimeBounds()
	if err != nil {
		return time.Unix(0, int64(^uint64(0)>>1)).UTC()
	}
	return high
}

// CurrentCredentialFacts are detached producer output, not an authorization
// capability. The provider adapter must obtain Token from opaque verifier/Code
// completion results, or Session through the exact native cookie/CSRF path.
type CurrentCredentialFacts struct {
	Token         *TokenAuthenticationEvidence
	Session       *Session
	Origin, CSRF  [32]byte
	MachineDigest string
}

// CurrentCredentialProducer is trusted private composition, never a request
// parameter, decoder, plugin callback or public RPC constructor. Production
// implementations live in provider to preserve oidc -> security dependencies.
type CurrentCredentialProducer interface {
	VerifyCurrentCredential(context.Context, CurrentCredentialView) (CurrentCredentialFacts, error)
}

type authorityCredential struct {
	actor          Identity
	authentication Authentication
	lineage        uint64
	cut            SemanticCut
	claim          authorityCredentialClaim
	machine        *MachineCredential
}

// Matches oidc's original trust commitment without importing that higher layer.
// A paired adapter test compares a real verifier's result to this transcript.
func authorityIssuerCommitment(issuer Issuer, generation [16]byte) [32]byte {
	raw, err := json.Marshal(struct {
		Issuer         Issuer
		Generation     [16]byte
		ConfigRevision uint64
	}{issuer, generation, issuer.ConfigRevision})
	if err != nil {
		return [32]byte{}
	}
	return sha256.Sum256(append([]byte("lantern/authentication/trust/v1\x00"), raw...))
}

func captureAuthorityCredential(view CurrentCredentialView, facts CurrentCredentialFacts) (authorityCredential, error) {
	return captureAuthorityUseCredential(view, facts, true)
}

func captureAuthorityUseCredential(view CurrentCredentialView, facts CurrentCredentialFacts, humanOnly bool) (authorityCredential, error) {
	var result authorityCredential
	kinds := 0
	if facts.Token != nil {
		kinds++
	}
	if facts.Session != nil {
		kinds++
	}
	if facts.MachineDigest != "" {
		kinds++
	}
	if view.Snapshot() == nil || kinds != 1 {
		return result, ErrPermissionDenied
	}
	low, high, err := view.TimeBounds()
	if err != nil {
		return result, err
	}
	c := authorityCredentialClaim{Origin: facts.Origin, CSRF: facts.CSRF}
	var actor Identity
	var auth Authentication
	var expiry time.Time
	if facts.MachineDigest != "" {
		m, known := view.projection.snapshot.machines[facts.MachineDigest]
		if humanOnly || !known || facts.Origin != [32]byte{} || facts.CSRF != [32]byte{} || low.Before(m.CreatedAt) || !high.Before(m.ExpiresAt) {
			return result, ErrPermissionDenied
		}
		if _, active := view.projection.snapshot.AccessFor(m.Identity); !active {
			return result, ErrPermissionDenied
		}
		c := authorityCredentialClaim{Kind: "machine", DerivedStart: authorityNumericTime(m.CreatedAt), AdmissionDeadline: m.ExpiresAt}
		return authorityCredential{actor: m.Identity, authentication: Authentication{Provenance: NativeMachine, Class: MachineActor}, cut: view.Cut(), claim: c, machine: &m}, nil
	}
	if facts.Token != nil {
		if facts.Origin != [32]byte{} || facts.CSRF != [32]byte{} {
			return result, ErrPermissionDenied
		}
		e := *facts.Token // Detach pointers owned by the producer before retaining.
		c.Token = &e
		actor = e.Identity
		issuer, known := view.projection.snapshot.Issuer(actor.Issuer)
		commitment, evidenceErr := e.Commitment()
		if !known || !issuer.Enabled || issuer.Deleted || evidenceErr != nil || e.Generation != view.projection.cut.Generation || e.ConfigRevision != issuer.ConfigRevision || e.Configuration != authorityIssuerCommitment(issuer, view.projection.cut.Generation) {
			return result, ErrPermissionDenied
		}
		c.Verification = commitment
		if e.AuthTime.Numeric && (e.AuthTime.Time().After(e.IssuedAt.Time()) || low.Before(e.AuthTime.Time())) {
			return result, ErrPermissionDenied
		}
		auth = Authentication{Class: EndUser, IssuerConfigRevision: issuer.ConfigRevision}
		switch e.Mode {
		case "access":
			auth.Class = view.projection.snapshot.BearerActor(actor)
			if humanOnly && auth.Class != EndUser {
				return result, ErrPermissionDenied
			}
			c.Kind, auth.Provenance = "access", RFC9068Bearer
			principal := view.projection.snapshot.principals[actor]
			c.Enrollment = s2cHash("current-human-enrollment-v2", struct {
				Identity  Identity
				Revision  uint64
				Bootstrap bool
			}{actor, principal.humanIssuerConfigRevision, principal.bootstrapHuman})
			c.HumanNamespace = s2cHash("current-human-namespace-v2", issuer)
		case "code":
			if e.Code.Flow != "login" {
				return result, ErrPermissionDenied
			}
			c.Kind, auth.Provenance = "code", BrowserCode
		default:
			return result, ErrPermissionDenied
		}
		expiry = e.ExpiresAt.Time()
	} else {
		s := *facts.Session
		c.Session = &s
		actor = s.Identity
		native, known := view.projection.snapshot.Session(s.Digest)
		if !known || native != s || s.Revoked || view.browserOrigin == "" || c.Origin != sha256.Sum256([]byte("lantern/authentication/browser-origin/v1\x00"+view.browserOrigin)) || c.CSRF == [32]byte{} {
			return result, ErrPermissionDenied
		}
		c.Kind, c.Verification = "session", s2cHash("current-native-session-v2", s)
		auth = Authentication{Provenance: BrowserCode, Class: EndUser, IssuerConfigRevision: s.IssuerConfigRevision, SessionDigest: s.Digest}
		expiry = s.ExpiresAt
	}
	if actor.Kind != OIDCPrincipal || (auth.Class == EndUser && view.projection.snapshot.checkHumanAuthentication(actor, auth, high) != nil) {
		return result, ErrPermissionDenied
	}
	if _, active := view.projection.snapshot.AccessFor(actor); !active {
		return result, ErrPermissionDenied
	}
	c.DerivedStart = authorityCredentialStart(c)
	if low.Before(c.DerivedStart.Time()) || !high.Before(expiry) {
		return result, ErrPermissionDenied
	}
	// Absolute credential expiry and native elapsed renewal lifetime are
	// independent. Do not invent another short wall deadline for a still-valid
	// credential; final use must separately hold the live receiver capability.
	c.AdmissionDeadline = expiry.UTC().Round(0)
	lineage := view.projection.lineage[actor]
	if lineage == 0 {
		return result, ErrPermissionDenied
	}
	return authorityCredential{actor: actor, authentication: auth, lineage: lineage, cut: view.projection.cut, claim: c}, nil
}

func (c authorityCredential) matches(p *S1Projection) bool {
	if p == nil || c.cut != p.cut {
		return false
	}
	if c.machine != nil {
		m, known := p.snapshot.machines[c.machine.Digest]
		_, active := p.snapshot.AccessFor(c.actor)
		return known && m == *c.machine && active
	}
	if c.lineage == 0 || c.lineage != p.lineage[c.actor] {
		return false
	}
	i, known := p.snapshot.Issuer(c.actor.Issuer)
	if !known || !i.Enabled || i.Deleted || i.ConfigRevision != c.authentication.IssuerConfigRevision {
		return false
	}
	if c.claim.Session != nil {
		s, known := p.snapshot.Session(c.claim.Session.Digest)
		if !known || s != *c.claim.Session || s.Revoked {
			return false
		}
	}
	if c.authentication.Provenance == RFC9068Bearer && p.snapshot.BearerActor(c.actor) != c.authentication.Class {
		return false
	}
	_, active := p.snapshot.AccessFor(c.actor)
	return active
}
