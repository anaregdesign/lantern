package authn

import (
	"context"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

// Credentials borrows the composition-owned key cache. Fact structs and request
// callbacks cannot substitute for the actual opaque OIDC completion/verifier.
type Credentials struct{ keys *oidc.KeyCache }

func NewCredentials(keys *oidc.KeyCache) *Credentials { return &Credentials{keys: keys} }

type currentCredentialProducer struct {
	keys         *oidc.KeyCache
	kind, bearer string
	completion   oidc.LoginCompletion
	tokens       oidc.CodeTokens
}

func (currentCredentialProducer) String() string   { return "[redacted current credential producer]" }
func (currentCredentialProducer) GoString() string { return "[redacted current credential producer]" }
func (c *Credentials) Bearer(raw string) (security.CurrentCredentialProducer, error) {
	if c == nil || c.keys == nil || raw == "" {
		return nil, oidc.ErrInvalidToken
	}
	kind := "access"
	if strings.HasPrefix(raw, security.MachineTokenPrefix) {
		if _, valid := security.MachineTokenDigest(raw); !valid {
			return nil, oidc.ErrInvalidToken
		}
		kind = "machine"
	}
	return currentCredentialProducer{keys: c.keys, kind: kind, bearer: raw}, nil
}
func (c *Credentials) Code(completion oidc.LoginCompletion, tokens oidc.CodeTokens) (security.CurrentCredentialProducer, error) {
	if c == nil || c.keys == nil {
		return nil, oidc.ErrInvalidToken
	}
	return currentCredentialProducer{keys: c.keys, kind: "code", completion: completion, tokens: tokens}, nil
}
func (p currentCredentialProducer) VerifyCurrentCredential(ctx context.Context, view security.CurrentCredentialView) (security.CurrentCredentialFacts, error) {
	if err := ctx.Err(); err != nil {
		return security.CurrentCredentialFacts{}, err
	}
	_, high, err := view.TimeBounds()
	if err != nil || view.Snapshot() == nil {
		return security.CurrentCredentialFacts{}, security.ErrAuthorityUnavailable
	}
	snapshot := view.Snapshot()
	if p.kind == "machine" {
		if _, _, active := snapshot.MachineAccess(p.bearer, high); !active {
			return security.CurrentCredentialFacts{}, oidc.ErrInvalidToken
		}
		digest, valid := security.MachineTokenDigest(p.bearer)
		if !valid {
			return security.CurrentCredentialFacts{}, oidc.ErrInvalidToken
		}
		return security.CurrentCredentialFacts{MachineDigest: digest}, nil
	}
	verifier := oidc.NewVerifierWithClock(p.keys, view.VerificationTime)
	var verified oidc.VerifiedIdentity
	if p.kind == "access" {
		issuerURL, err := oidc.TokenIssuer(p.bearer)
		if err != nil {
			return security.CurrentCredentialFacts{}, err
		}
		issuer, known := snapshot.Issuer(issuerURL)
		if !known || !issuer.Enabled || issuer.Deleted {
			return security.CurrentCredentialFacts{}, oidc.ErrInvalidToken
		}
		verified, err = verifier.VerifyAccess(ctx, p.bearer, oidc.Trust{Issuer: issuer, Generation: view.Cut().Generation, ConfigRevision: issuer.ConfigRevision})
		if err != nil {
			return security.CurrentCredentialFacts{}, err
		}
	} else if p.kind == "code" {
		verified, err = verifier.VerifyLoginCompletion(ctx, p.completion, p.tokens)
		if err != nil {
			return security.CurrentCredentialFacts{}, err
		}
	} else {
		return security.CurrentCredentialFacts{}, oidc.ErrInvalidToken
	}
	if _, _, err := view.TimeBounds(); err != nil {
		return security.CurrentCredentialFacts{}, err
	}
	event := verified.Evidence()
	if p.kind == "code" {
		// A newly issued Code token can be valid at the qualified upper
		// endpoint while its signed start is still ahead of the lower one.
		// Keep this one exchange; pace locally until the exact original facts
		// are usable. Capture/consume still recheck the full current view.
		start := event.IssuedAt.Time()
		for _, candidate := range []time.Time{event.NotBefore.Time(), event.AuthTime.Time(), event.Code.ConsumedAt} {
			if candidate.After(start) {
				start = candidate
			}
		}
		expires := event.ExpiresAt.Time()
		if event.Code.ExpiresAt.Before(expires) {
			expires = event.Code.ExpiresAt
		}
		if err := waitCurrentCodeStart(ctx, view.TimeBounds, start, expires); err != nil {
			return security.CurrentCredentialFacts{}, err
		}
	}
	return security.CurrentCredentialFacts{Token: &event}, nil
}

// Timers only pace the wait; each decision uses the existing native owner.

func waitCurrentCodeStart(ctx context.Context, bounds func() (time.Time, time.Time, error), start, expires time.Time) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		low, high, err := bounds()
		if err != nil {
			return err
		}
		if !high.Before(expires) {
			return oidc.ErrInvalidToken
		}
		if !low.Before(start) {
			return nil
		}
		timer := time.NewTimer(min(start.Sub(low), 250*time.Millisecond))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
