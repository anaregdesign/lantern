package authn

import (
	"context"
	"errors"

	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func (w *Workflow) NativeEnabled() bool {
	return w != nil && w.current != nil && w.oidc.NativeLogins != nil
}

func (w *Workflow) BeginNativeLogin(ctx context.Context, issuerURL, clientProfileID string) (oidc.NativeStart, error) {
	return w.beginNative(ctx, issuerURL, clientProfileID, security.NativeRegistrationPurpose{})
}

func (w *Workflow) beginNative(ctx context.Context, issuerURL, clientProfileID string, purpose security.NativeRegistrationPurpose) (oidc.NativeStart, error) {
	if !w.NativeEnabled() {
		return oidc.NativeStart{}, ErrUnavailable
	}
	if issuerURL == "" || len(issuerURL) > 2048 || clientProfileID == "" || len(clientProfileID) > 64 {
		return oidc.NativeStart{}, ErrInvalidInput
	}
	issuer, cut, err := w.current.LoginIssuer(ctx, issuerURL)
	if err != nil {
		return oidc.NativeStart{}, ErrUnauthenticated
	}
	if _, ok := issuer.NativeClient(clientProfileID); !ok {
		return oidc.NativeStart{}, ErrUnauthenticated
	}
	discovery, err := w.oidc.Fetcher.Discover(ctx, issuer)
	if err != nil {
		return oidc.NativeStart{}, ErrUnavailable
	}
	_, after, err := w.current.LoginIssuer(ctx, issuerURL)
	if err != nil || after != cut {
		return oidc.NativeStart{}, ErrUnavailable
	}
	trust := oidc.Trust{Issuer: issuer, Generation: cut.Generation, ConfigRevision: issuer.ConfigRevision}
	var start oidc.NativeStart
	if purpose == (security.NativeRegistrationPurpose{}) {
		start, err = w.oidc.NativeLogins.Begin(trust, w.current.Profile(), clientProfileID, discovery)
	} else {
		start, err = w.oidc.NativeLogins.BeginRegistration(trust, w.current.Profile(), clientProfileID, discovery, purpose)
	}
	if err != nil {
		return oidc.NativeStart{}, err
	}
	if w.current.BindLoginOutput(ctx, cut, issuerURL) != nil {
		return oidc.NativeStart{}, ErrUnavailable
	}
	return start, nil
}

type NativeCompletionInput struct {
	AttemptID, CompletionSecret, Affinity string
	Provider                              oidc.NativeInput
}

func (NativeCompletionInput) String() string   { return "[redacted native completion input]" }
func (NativeCompletionInput) GoString() string { return "[redacted native completion input]" }

type NativeSessionResult struct {
	Credential string
	Session    security.Session
	Context    context.Context
}

func (NativeSessionResult) String() string   { return "[redacted native session result]" }
func (NativeSessionResult) GoString() string { return "[redacted native session result]" }

func (w *Workflow) checkNativeAttempt(ctx context.Context, a oidc.NativeAttempt) error {
	trust := a.Trust()
	issuer, cut, err := w.current.LoginIssuer(ctx, trust.Issuer.URL)
	if err != nil || a.Profile() != w.current.Profile() || cut.Generation != trust.Generation || issuer.ConfigRevision != trust.ConfigRevision {
		return ErrUnauthenticated
	}
	return nil
}

func (w *Workflow) CompleteNativeLogin(ctx context.Context, input NativeCompletionInput) (NativeSessionResult, error) {
	if !w.NativeEnabled() {
		return NativeSessionResult{}, ErrUnavailable
	}
	attempt, err := w.oidc.NativeLogins.Lookup(input.AttemptID, input.CompletionSecret, input.Affinity)
	if err != nil || !attempt.IsLogin() || w.checkNativeAttempt(ctx, attempt) != nil {
		return NativeSessionResult{}, ErrUnauthenticated
	}
	completion, tokens, err := w.completeNativeProvider(ctx, attempt, input.Provider)
	if err != nil {
		return NativeSessionResult{}, err
	}
	producer, err := w.oidc.Credentials.Native(completion, tokens)
	if err != nil {
		return NativeSessionResult{}, ErrUnauthenticated
	}
	secret, err := newSessionSecret()
	if err != nil {
		return NativeSessionResult{}, ErrUnavailable
	}
	value := security.NativeCredentialPrefix + secret
	digest, valid := security.NativeCredentialDigest(value)
	if !valid {
		return NativeSessionResult{}, ErrUnavailable
	}
	ctx, session, err := w.issueCurrentSession(ctx, producer, digest, "", "")
	if errors.Is(err, errSessionOutput) {
		return NativeSessionResult{}, ErrUnavailable
	}
	if err != nil {
		return NativeSessionResult{}, err
	}
	return NativeSessionResult{Credential: value, Session: session, Context: ctx}, nil
}

func (w *Workflow) completeNativeProvider(ctx context.Context, attempt oidc.NativeAttempt, input oidc.NativeInput) (oidc.NativeCompletion, oidc.NativeTokens, error) {
	credential, err := w.oidc.Verifier.VerifyNativeCredential(ctx, attempt, input)
	if err != nil {
		return oidc.NativeCompletion{}, oidc.NativeTokens{}, nativeVerificationError(err)
	}
	completion, err := w.oidc.NativeLogins.Consume(attempt, credential)
	if err != nil {
		return oidc.NativeCompletion{}, oidc.NativeTokens{}, ErrUnauthenticated
	}
	var tokens oidc.NativeTokens
	if input.Provider == "apple" {
		tokens, err = w.oidc.Fetcher.ExchangeApple(ctx, completion, w.oidc.Secrets)
		if err != nil {
			return oidc.NativeCompletion{}, oidc.NativeTokens{}, nativeVerificationError(err)
		}
	}
	return completion, tokens, nil
}

// StageAppleCallback verifies provider output before returning the registered
// application package. The HTTP adapter constructs the fixed plugin intent;
// this workflow never returns a Lantern credential or completion proof there.
func (w *Workflow) StageAppleCallback(ctx context.Context, profileID string, input oidc.NativeInput) (string, error) {
	if !w.NativeEnabled() {
		return "", ErrUnavailable
	}
	a, err := w.oidc.NativeLogins.CallbackAttempt(profileID, input.State)
	if err != nil || w.checkNativeAttempt(ctx, a) != nil {
		return "", ErrUnauthenticated
	}
	credential, err := w.oidc.Verifier.VerifyNativeCredential(ctx, a, input)
	if err != nil {
		return "", nativeVerificationError(err)
	}
	pkg, err := w.oidc.NativeLogins.StageCallback(a, credential)
	if err != nil {
		return "", ErrUnauthenticated
	}
	trust := a.Trust()
	_, cut, err := w.current.LoginIssuer(ctx, trust.Issuer.URL)
	if err != nil || w.checkNativeAttempt(ctx, a) != nil || w.current.BindLoginOutput(ctx, cut, trust.Issuer.URL) != nil {
		return "", ErrUnavailable
	}
	return pkg, nil
}

func (w *Workflow) PrepareNativeLogout(ctx context.Context, a *security.Admission) (security.CurrentReview, error) {
	if !w.NativeEnabled() || a == nil || a.Authentication().Provenance != security.NativeEndUserSession {
		return security.CurrentReview{}, ErrUnauthenticated
	}
	return w.PrepareLogout(ctx, a)
}
func (w *Workflow) ApplyNativeLogout(ctx context.Context, a *security.Admission, review security.CurrentReview) (*security.CurrentChangeResult, error) {
	if !w.NativeEnabled() || a == nil || a.Authentication().Provenance != security.NativeEndUserSession {
		return nil, ErrUnauthenticated
	}
	return w.applyLogout(ctx, a, review, security.CurrentLocalNativeClear)
}

func nativeVerificationError(err error) error {
	if errors.Is(err, oidc.ErrFetch) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return ErrUnavailable
	}
	return ErrUnauthenticated
}
