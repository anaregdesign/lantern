package security

import "time"

// This variant is never an ordinary authorityCredential or Admission. Only the
// registration origin path can retain it; unknown Principals remain ineligible
// for every ordinary credential, data and management path.
type authorityRegistrationCredential struct {
	actor   Identity
	cut     SemanticCut
	lineage uint64
	claim   authorityCredentialClaim
	state   authorityRegistrationState
}

type authorityRegistrationState struct {
	Present bool
	UserUID string
}

// An applied original is checked against the same durable ledger at prepare,
// consume and Apply. The record grants only a separate native session issuance;
// it is not a fabricated native-login event or a reusable registration grant.
type authorityRegistrationOriginal struct {
	ID      FullChangeID
	Intent  [32]byte
	Commit  CommitRef
	UserUID string
}

type authorityRegistrationAuthorization struct {
	Version         uint8
	State           authorityRegistrationState
	ClientProfileID string
	Original        *authorityRegistrationOriginal `json:",omitempty"`
}

func captureRegistrationCredential(view CurrentCredentialView, facts CurrentCredentialFacts) (authorityRegistrationCredential, error) {
	var result authorityRegistrationCredential
	if view.Snapshot() == nil || facts.Token == nil || facts.Session != nil || facts.MachineDigest != "" || facts.BrowserReadOnly || facts.Origin != [32]byte{} || facts.CSRF != [32]byte{} {
		return result, ErrPermissionDenied
	}
	e := *facts.Token
	commitment, err := e.Commitment()
	if err != nil || e.Mode != "native-registration" || !e.Native.Registration.Valid() {
		return result, ErrPermissionDenied
	}
	issuer, known := view.Snapshot().Issuer(e.Identity.Issuer)
	if !known || !issuer.Enabled || issuer.Deleted || e.Generation != view.Cut().Generation || e.ConfigRevision != issuer.ConfigRevision ||
		e.Configuration != authorityIssuerCommitment(issuer, view.Cut().Generation) || !e.NativeBindingMatches(issuer) {
		return result, ErrPermissionDenied
	}
	low, high, err := view.TimeBounds()
	if err != nil {
		return result, err
	}
	c := authorityCredentialClaim{Kind: "native-registration", Token: &e, Verification: commitment, AdmissionDeadline: nativeEvidenceExpiry(e).UTC().Round(0)}
	c.DerivedStart = authorityCredentialStart(c)
	if !registrationEvidenceTimesValid(c, low, high) {
		return result, ErrPermissionDenied
	}
	principal, present := view.projection.snapshot.principals[e.Identity]
	lineage := view.projection.lineage[e.Identity]
	if present && (principal.state != Active || lineage == 0) || !present && lineage != 0 {
		return result, ErrPermissionDenied
	}
	return authorityRegistrationCredential{actor: e.Identity, cut: view.Cut(), lineage: lineage, claim: c, state: authorityRegistrationState{Present: present, UserUID: principal.userUID}}, nil
}

func registrationEvidenceTimesValid(c authorityCredentialClaim, low, high time.Time) bool {
	if c.Token == nil || c.DerivedStart != authorityCredentialStart(c) || !c.DerivedStart.Numeric || low.Before(c.DerivedStart.Time()) || !high.Before(c.AdmissionDeadline) {
		return false
	}
	e := c.Token
	return e.ExpiresAt.Time().After(e.IssuedAt.Time()) && c.AdmissionDeadline == nativeEvidenceExpiry(*e).UTC().Round(0) &&
		(!e.AuthTime.Numeric || !e.AuthTime.Time().After(e.IssuedAt.Time()) && !low.Before(e.AuthTime.Time())) &&
		!low.Before(e.Native.ConsumedAt) && nativeExchangeTimesValid(e.Native, low)
}

func (c authorityRegistrationCredential) matches(p *S1Projection) bool {
	if p == nil || c.cut != p.cut || c.claim.Token == nil || c.actor != c.claim.Token.Identity || c.lineage != p.lineage[c.actor] {
		return false
	}
	e := c.claim.Token
	issuer, known := p.snapshot.Issuer(c.actor.Issuer)
	if !known || !issuer.Enabled || issuer.Deleted || issuer.ConfigRevision != e.ConfigRevision || !e.NativeBindingMatches(issuer) {
		return false
	}
	principal, present := p.snapshot.principals[c.actor]
	return present == c.state.Present && principal.userUID == c.state.UserUID &&
		(present && principal.state == Active && c.lineage != 0 || !present && c.lineage == 0)
}

// RegistrationReviewBinding is public data, not proof. It binds recovery to
// the complete original identity, command, cut, namespace and intent.
func RegistrationReviewBinding(review CurrentReview) [32]byte {
	c, err := review.Command()
	if err != nil || c.Kind != S1RegisterPrincipal || !review.ID.valid() || review.Profile.Binding() == "" ||
		review.ID.Domain != review.Profile.Domain || review.ID.Cohort != review.Profile.Cohort ||
		review.Operation.reviewed.Domain != review.Profile.Domain || review.Operation.reviewed.Cohort != review.Profile.Cohort || review.Operation.reviewed.Generation != review.Profile.Generation {
		return [32]byte{}
	}
	return s2cHash("native-registration-original-review-v1", struct {
		Profile   CurrentProfile
		ID        FullChangeID
		Operation string
	}{review.Profile, review.ID, review.Operation.canonical})
}

func registrationOriginalMatches(s *S1ApplyState, actor Identity, origin *authorityRegistrationOriginal) bool {
	if s == nil || origin == nil || !origin.ID.valid() || origin.Intent == [32]byte{} {
		return false
	}
	o := s.ledger[origin.ID]
	if o == nil || o.disposition != S1Applied || o.operation.actor != actor || o.operation.digest != origin.Intent || o.commit != origin.Commit {
		return false
	}
	c, err := o.operation.command()
	if err != nil || c.Kind != S1RegisterPrincipal || c.Registration == nil || c.Registration.UserUID != origin.UserUID {
		return false
	}
	uid, _, active := s.projection.snapshot.PersonalArea(actor)
	// The session transition is deliberately confined to the original result's
	// exact cut. Any intervening policy/lineage change requires ordinary login;
	// it cannot reuse registration evidence to cross a revocation boundary.
	return active && uid == origin.UserUID && s.projection.lineage[actor] != 0 && s.projection.cut == o.resulting
}
