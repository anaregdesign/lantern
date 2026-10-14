package security

import (
	"context"
	"net/http"
	"strings"
	"time"
)

func (o *authorityOriginOwner) authorizeRegistrationLocked(ctx context.Context, c authorityRegistrationCredential) (authorityCurrentTime, error) {
	if ctx.Err() != nil || !c.matches(o.network.kernel.replayState.projection) {
		return authorityCurrentTime{}, ErrPermissionDenied
	}
	start, expiry, err := o.eligibilityLocked(ctx)
	if err != nil {
		return authorityCurrentTime{}, err
	}
	now, _, err := o.network.receiver.currentLocked()
	if err != nil {
		return authorityCurrentTime{}, err
	}
	low, high := time.Unix(0, int64(now.utc.low)).UTC(), time.Unix(0, int64(now.utc.high)).UTC()
	if low.Before(start) || !high.Before(expiry) || !registrationEvidenceTimesValid(c.claim, low, high) {
		return authorityCurrentTime{}, ErrPermissionDenied
	}
	return now, nil
}

func (o *CurrentAuthority) BindRegistrationOutput(ctx context.Context, proof *CurrentRegistration, producer CurrentCredentialProducer) error {
	if err := o.registrationOutputPath(ctx, false); err != nil {
		return err
	}
	c, err := o.refreshRegistration(ctx, proof, producer)
	if err != nil {
		return err
	}
	return bindCurrentGrant(ctx, o, currentOutputGrant{kind: "registration", registration: &c, resource: RegistrationReviewBinding(proof.review)})
}

// Final issuance output requires both the still-valid original registration
// verification and the exact newly installed native session's own admission.
// Status has no route to this API or to raw credential recovery.
func (o *CurrentAuthority) BindRegistrationSessionOutput(ctx context.Context, proof *CurrentRegistration, producer CurrentCredentialProducer, a *Admission, session Session) error {
	if err := o.registrationOutputPath(ctx, true); err != nil {
		return err
	}
	if proof == nil || proof.RecoveryOnly() || o.currentAdmission(ctx, a, false) != nil || a.Authentication().Provenance != NativeEndUserSession || a.Authentication().SessionDigest != session.Digest || a.Identity() != session.Identity {
		return ErrPermissionDenied
	}
	installed, known := a.Snapshot().Session(session.Digest)
	if !known || installed != session || session.Revoked {
		return ErrPermissionDenied
	}
	c, err := o.refreshRegistration(ctx, proof, producer)
	if err != nil || c.actor != session.Identity || c.cut != a.current.credential.cut {
		return ErrPermissionDenied
	}
	return bindCurrentGrant(ctx, o, currentOutputGrant{kind: "registration-session", registration: &c, admission: a.current, projection: a.current.snapshot,
		resource: s2cHash("public-registration-session-output-v1", struct {
			Original [32]byte
			Session  Session
		}{RegistrationReviewBinding(proof.review), session})})
}

// Registration's unauthenticated entrypoints have a closed output use. A proof
// cannot be composed as a generic data/admin response grant.
func (o *CurrentAuthority) registrationOutputPath(ctx context.Context, session bool) error {
	r, err := currentRequest(ctx, o)
	if err != nil {
		return err
	}
	path := strings.TrimPrefix(r.path, "/browser")
	if r.method != http.MethodPost {
		return ErrPermissionDenied
	}
	if path == "/graph.v1.LanternNativeAuthService/ApplyNativeRegistration" {
		return nil
	}
	if !session && (path == "/graph.v1.LanternNativeAuthService/PrepareNativeRegistration" || path == "/graph.v1.LanternNativeAuthService/GetNativeRegistrationStatus") {
		return nil
	}
	return ErrPermissionDenied
}
