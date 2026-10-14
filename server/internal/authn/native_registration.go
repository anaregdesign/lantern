package authn

import (
	"context"
	"time"

	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

type NativeRegistrationPrepared struct {
	Proof, Affinity string
	Review          security.CurrentReview
	ExpiresAt       time.Time
}

func (NativeRegistrationPrepared) String() string   { return "[redacted prepared native registration]" }
func (NativeRegistrationPrepared) GoString() string { return "[redacted prepared native registration]" }

type NativeRegistrationRequest struct {
	Proof, Affinity string
	Review          security.CurrentReview
}

func (NativeRegistrationRequest) String() string   { return "[redacted native registration request]" }
func (NativeRegistrationRequest) GoString() string { return "[redacted native registration request]" }

type NativeRegistrationResult struct {
	Status  security.CurrentChangeResult
	Session *NativeSessionResult
	Context context.Context
}

func (NativeRegistrationResult) String() string   { return "[redacted native registration result]" }
func (NativeRegistrationResult) GoString() string { return "[redacted native registration result]" }

func (w *Workflow) BeginNativeRegistration(ctx context.Context, issuer, profile string, recovery *security.CurrentReview) (oidc.NativeStart, error) {
	if !w.NativeEnabled() {
		return oidc.NativeStart{}, ErrUnavailable
	}
	ctx, done, err := w.registrations.enter(ctx)
	if err != nil {
		return oidc.NativeStart{}, err
	}
	defer done()
	purpose := security.NativeRegistrationPurpose{Kind: "registration"}
	if recovery != nil {
		binding := security.RegistrationReviewBinding(*recovery)
		if binding == [32]byte{} || recovery.Profile != w.current.Profile() || recovery.Operation.Actor().Issuer != issuer {
			return oidc.NativeStart{}, ErrInvalidInput
		}
		purpose = security.NativeRegistrationPurpose{Kind: "registration-recovery", Original: binding}
	}
	return w.beginNative(ctx, issuer, profile, purpose)
}

func (w *Workflow) PrepareNativeRegistration(ctx context.Context, input NativeCompletionInput, recovery *security.CurrentReview) (NativeRegistrationPrepared, error) {
	if !w.NativeEnabled() {
		return NativeRegistrationPrepared{}, ErrUnavailable
	}
	ctx, done, err := w.registrations.enter(ctx)
	if err != nil {
		return NativeRegistrationPrepared{}, err
	}
	defer done()
	attempt, err := w.oidc.NativeLogins.Lookup(input.AttemptID, input.CompletionSecret, input.Affinity)
	if err != nil || !attempt.RegistrationPurpose().Valid() || w.checkNativeAttempt(ctx, attempt) != nil {
		return NativeRegistrationPrepared{}, ErrUnauthenticated
	}
	purpose := attempt.RegistrationPurpose()
	if recovery == nil {
		if purpose.Kind != "registration" {
			return NativeRegistrationPrepared{}, ErrInvalidInput
		}
	} else if recovery.Profile != w.current.Profile() || purpose.Kind != "registration-recovery" || purpose.Original != security.RegistrationReviewBinding(*recovery) {
		return NativeRegistrationPrepared{}, ErrInvalidInput
	}
	completion, tokens, err := w.completeNativeProvider(ctx, attempt, input.Provider)
	if err != nil {
		return NativeRegistrationPrepared{}, err
	}
	producer, err := w.oidc.Credentials.NativeRegistration(completion, tokens)
	if err != nil {
		return NativeRegistrationPrepared{}, ErrUnauthenticated
	}
	var proof *security.CurrentRegistration
	if recovery == nil {
		proof, err = w.current.PrepareRegistration(ctx, producer)
	} else {
		proof, err = w.current.RecoverRegistration(ctx, producer, *recovery)
	}
	if err != nil {
		return NativeRegistrationPrepared{}, err
	}
	retained := false
	defer func() {
		if !retained {
			w.current.DiscardRegistration(proof)
		}
	}()
	charge, err := completion.RetainedBytes(tokens)
	if err != nil {
		return NativeRegistrationPrepared{}, ErrUnavailable
	}
	review := proof.Review()
	charge += uint64(3*len(review.Operation.Encode()) + 32<<10)
	entry := nativeRegistrationEntry{proof: proof, producer: producer, binding: security.RegistrationReviewBinding(review), expires: proof.ExpiresAt(), bytes: charge}
	if err := w.current.BindRegistrationOutput(ctx, proof, producer); err != nil {
		return NativeRegistrationPrepared{}, ErrUnavailable
	}
	handle, err := w.registrations.put(entry)
	if err != nil {
		return NativeRegistrationPrepared{}, err
	}
	retained = true
	return NativeRegistrationPrepared{Proof: handle, Affinity: w.attemptPrefix, Review: review, ExpiresAt: proof.ExpiresAt()}, nil
}

func (w *Workflow) nativeRegistrationEntry(input NativeRegistrationRequest, apply bool) (nativeRegistrationEntry, bool, error) {
	if input.Affinity != "" && input.Affinity != w.attemptPrefix {
		return nativeRegistrationEntry{}, false, ErrUnauthenticated
	}
	return w.registrations.get(input.Proof, security.RegistrationReviewBinding(input.Review), apply)
}

func (w *Workflow) GetNativeRegistrationStatus(ctx context.Context, input NativeRegistrationRequest) (NativeRegistrationResult, error) {
	requestCtx := ctx
	if !w.NativeEnabled() {
		return NativeRegistrationResult{}, ErrUnavailable
	}
	ctx, done, err := w.registrations.enter(ctx)
	if err != nil {
		return NativeRegistrationResult{}, err
	}
	defer done()
	entry, _, err := w.nativeRegistrationEntry(input, false)
	if err != nil {
		return NativeRegistrationResult{}, err
	}
	status, err := w.current.RegistrationStatus(ctx, entry.proof, entry.producer)
	if err != nil {
		return NativeRegistrationResult{}, err
	}
	if err := w.current.BindRegistrationOutput(ctx, entry.proof, entry.producer); err != nil {
		return NativeRegistrationResult{}, ErrUnavailable
	}
	return NativeRegistrationResult{Status: status, Context: requestCtx}, nil
}

func (w *Workflow) ApplyNativeRegistration(ctx context.Context, input NativeRegistrationRequest) (NativeRegistrationResult, error) {
	requestCtx := ctx
	if !w.NativeEnabled() {
		return NativeRegistrationResult{}, ErrUnavailable
	}
	ctx, done, err := w.registrations.enter(ctx)
	if err != nil {
		return NativeRegistrationResult{}, err
	}
	defer done()
	entry, duplicate, err := w.nativeRegistrationEntry(input, true)
	if err != nil {
		return NativeRegistrationResult{}, err
	}
	var status security.CurrentChangeResult
	if duplicate {
		status, err = w.current.RegistrationStatus(ctx, entry.proof, entry.producer)
	} else {
		status, err = w.current.ApplyRegistration(ctx, entry.proof, entry.producer)
	}
	// An error after possible dispatch cannot justify a replacement review or
	// another session. Preserve the actual strongest available original status.
	if err != nil && status.Profile != w.current.Profile() {
		return NativeRegistrationResult{}, err
	}
	if ctx.Err() != nil {
		return NativeRegistrationResult{}, ctx.Err()
	}
	if err := w.current.RefreshRegistration(ctx, entry.proof, entry.producer); err != nil {
		return NativeRegistrationResult{}, ErrUnavailable
	}
	if !duplicate && status.Original != nil && status.Original.Disposition() == security.S1Applied {
		if session, err := w.issueRegisteredSession(ctx, requestCtx, entry); err == nil {
			return NativeRegistrationResult{Status: status, Session: &session, Context: session.Context}, nil
		}
	}
	if err := w.current.BindRegistrationOutput(ctx, entry.proof, entry.producer); err != nil {
		return NativeRegistrationResult{}, ErrUnavailable
	}
	return NativeRegistrationResult{Status: status, Context: requestCtx}, nil
}

// Raw credential exists only in this invocation. Entry/status retain neither
// it nor any capability to recover it after cancellation or response loss.
func (w *Workflow) issueRegisteredSession(ctx, requestCtx context.Context, entry nativeRegistrationEntry) (NativeSessionResult, error) {
	secret, err := newSessionSecret()
	if err != nil {
		return NativeSessionResult{}, ErrUnavailable
	}
	value := security.NativeCredentialPrefix + secret
	digest, valid := security.NativeCredentialDigest(value)
	if !valid {
		return NativeSessionResult{}, ErrUnavailable
	}
	review, session, err := w.current.PrepareRegistrationSession(ctx, entry.proof, entry.producer, digest)
	if err != nil {
		return NativeSessionResult{}, err
	}
	defer w.current.DiscardRegistrationSession(entry.proof, review)
	result, err := w.current.Apply(ctx, review, entry.producer, [32]byte{})
	if err != nil || result.Original == nil || result.Original.Disposition() != security.S1Applied {
		return NativeSessionResult{}, ErrUnavailable
	}
	if err := w.current.RefreshRegistration(ctx, entry.proof, entry.producer); err != nil {
		return NativeSessionResult{}, ErrUnavailable
	}
	producer, err := w.oidc.Credentials.Bearer(value)
	if err != nil {
		return NativeSessionResult{}, ErrUnavailable
	}
	ctx, a, err := w.current.WithRequestCredential(ctx, producer)
	if err != nil {
		return NativeSessionResult{}, ErrUnavailable
	}
	if err := w.current.BindRegistrationSessionOutput(ctx, entry.proof, entry.producer, a, session); err != nil {
		return NativeSessionResult{}, ErrUnavailable
	}
	return NativeSessionResult{Credential: value, Session: session, Context: security.WithAdmission(requestCtx, a)}, nil
}
