package security

import (
	"context"
	"time"

	"github.com/anaregdesign/lantern/server/internal/authz"
)

// Closed origin uses share reservation, final consume, signing and persistence.
// They do not share ordinary Admission eligibility. Recovery has no origin use:
// it can disclose the original status but can never prepare or consume work.
type authorityOriginUse struct {
	kind     string
	original *authorityRegistrationOriginal
}

type authorityOriginCredential struct {
	actor          Identity
	authentication Authentication
	lineage        uint64
	cut            SemanticCut
	claim          authorityCredentialClaim
	ordinary       *authorityCredential
	registration   *authorityRegistrationCredential
}

func (c authorityOriginCredential) matches(s *S1ApplyState) bool {
	if s == nil {
		return false
	}
	if c.ordinary != nil && c.registration == nil && c.claim.Registration == nil {
		return c.ordinary.matches(s.projection)
	}
	if c.ordinary != nil || c.registration == nil || c.claim.Registration == nil || !c.registration.matches(s.projection) {
		return false
	}
	r := c.claim.Registration
	return r.Original == nil || registrationOriginalMatches(s, c.actor, r.Original)
}

func (u authorityOriginUse) accepts(command S1Command) bool {
	switch u.kind {
	case "":
		return u.original == nil && ordinaryS1Command(command.Kind)
	case "registration":
		return u.original == nil && command.Kind == S1RegisterPrincipal
	case "registered-session":
		return u.original != nil && command.Kind == S1IssueSession
	default:
		return false
	}
}

func (o *authorityOriginOwner) authenticateUse(ctx context.Context, producer CurrentCredentialProducer, use authorityOriginUse) (authorityOriginCredential, error) {
	if use.kind == "" && use.original == nil {
		c, err := o.authenticate(ctx, producer)
		if err != nil {
			return authorityOriginCredential{}, err
		}
		return authorityOriginCredential{actor: c.actor, authentication: c.authentication, lineage: c.lineage, cut: c.cut, claim: c.claim, ordinary: &c}, nil
	}
	if use.kind != "registration" && use.kind != "registered-session" || (use.kind == "registration") != (use.original == nil) {
		return authorityOriginCredential{}, ErrPermissionDenied
	}
	view, facts, err := o.verifyFacts(ctx, producer)
	if err != nil {
		return authorityOriginCredential{}, err
	}
	c, err := captureRegistrationCredential(view, facts)
	if err != nil || c.claim.Token.Native.Registration.Kind != "registration" {
		return authorityOriginCredential{}, ErrPermissionDenied
	}
	claim := c.claim
	claim.Registration = &authorityRegistrationAuthorization{Version: 1, State: c.state, ClientProfileID: c.claim.Token.Native.ClientProfileID}
	if use.original != nil {
		original := *use.original
		claim.Registration.Original = &original
	}
	return authorityOriginCredential{actor: c.actor, authentication: Authentication{Provenance: NativeProviderRegistration, Class: EndUser, IssuerConfigRevision: c.claim.Token.ConfigRevision},
		lineage: c.lineage, cut: c.cut, claim: claim, registration: &c}, nil
}

func verifyRegistrationCredential(h authorityHistoricalHeader, operation OperationIdentity, command S1Command) bool {
	c, a := h.Credential, h.Authentication
	r := c.Registration
	if r == nil || r.Version != 1 || c.Kind != "native-registration" || c.Token == nil || c.Session != nil ||
		c.Enrollment != [32]byte{} || c.HumanNamespace != [32]byte{} || c.Origin != [32]byte{} || c.CSRF != [32]byte{} ||
		a.Provenance != NativeProviderRegistration || a.Class != EndUser || a.IssuerConfigRevision == 0 || a.SessionDigest != "" ||
		r.State.Present != (h.Lineage != 0) || !r.State.Present && r.State.UserUID != "" {
		return false
	}
	e := c.Token
	commitment, err := e.Commitment()
	if err != nil || c.Verification != commitment || e.Mode != "native-registration" || e.Native.Registration.Kind != "registration" ||
		e.Identity != operation.actor || e.Generation != operation.reviewed.Generation || e.ConfigRevision != a.IssuerConfigRevision || r.ClientProfileID != e.Native.ClientProfileID {
		return false
	}
	low, high := h.Time.utcTimes()
	if !registrationEvidenceTimesValid(c, low, high) || h.CredentialDeadline != c.AdmissionDeadline || !s2cCanonicalTime(h.CredentialDeadline, false) ||
		h.Purpose != nil || h.PurposeEvidence != [32]byte{} || h.PurposeBinding != [32]byte{} || !h.PurposeDeadline.IsZero() {
		return false
	}
	return registrationCommandMatches(r, operation.actor, h.Lineage, a.IssuerConfigRevision, command, e, low)
}

func registrationCommandMatches(r *authorityRegistrationAuthorization, actor Identity, lineage, revision uint64, command S1Command, e *TokenAuthenticationEvidence, low time.Time) bool {
	if r.State.UserUID != "" && !authz.ValidPersonalUID(r.State.UserUID) {
		return false
	}
	if command.Kind == S1RegisterPrincipal {
		c := command.Registration
		return r.Original == nil && c.valid() && c.ClientProfileID == r.ClientProfileID && c.IssuerConfigRevision == revision &&
			(r.State.UserUID == "" || r.State.UserUID == c.UserUID)
	}
	s := command.Session
	if command.Kind != S1IssueSession || r.Original == nil || !r.State.Present || lineage == 0 || !authz.ValidPersonalUID(r.State.UserUID) ||
		!r.Original.ID.valid() || r.Original.Intent == [32]byte{} || r.Original.UserUID != r.State.UserUID || r.Original.Commit.Slot == 0 ||
		s == nil || !s.valid() || s.Revoked || s.Identity != actor || s.IssuerConfigRevision != revision || s.CredentialProfile() != NativeEndUserProfile ||
		s.ClientProfileID != e.Native.ClientProfileID || s.ServiceOrigin != e.Native.ServiceOrigin || s.CreatedAt.Before(e.Native.ConsumedAt) || low.Before(s.CreatedAt) ||
		!s.AuthTime.Equal(e.AuthTime.Time()) || command.SessionLineage != lineage || command.ReplacesDigest != "" {
		return false
	}
	return true
}

func registrationAuthorizationMatches(p *S1Projection, h *S1Handoff, c S1Command) bool {
	r := h.authorization.registration
	if r == nil || r.Version != 1 || h.authorization.authentication.Provenance != NativeProviderRegistration || h.authorization.authentication.SessionDigest != "" {
		return false
	}
	actor := h.operation.actor
	issuer, known := p.snapshot.Issuer(actor.Issuer)
	if !known || !issuer.Enabled || issuer.Deleted || issuer.ConfigRevision != h.authorization.authentication.IssuerConfigRevision || p.lineage[actor] != h.authorization.lineage {
		return false
	}
	if _, known := issuer.NativeClient(r.ClientProfileID); !known {
		return false
	}
	principal, present := p.snapshot.principals[actor]
	if present != r.State.Present || principal.userUID != r.State.UserUID || present && principal.state != Active || present != (h.authorization.lineage != 0) {
		return false
	}
	if c.Kind == S1RegisterPrincipal {
		return r.Original == nil && c.Registration != nil && c.Registration.ClientProfileID == r.ClientProfileID && c.Registration.IssuerConfigRevision == issuer.ConfigRevision &&
			(r.State.UserUID == "" || r.State.UserUID == c.Registration.UserUID)
	}
	return c.Kind == S1IssueSession && r.Original != nil && present && r.State.UserUID != "" && c.Session != nil &&
		c.Session.Identity == actor && c.Session.CredentialProfile() == NativeEndUserProfile && c.Session.ClientProfileID == r.ClientProfileID && c.SessionLineage == h.authorization.lineage
}
