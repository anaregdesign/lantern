package authn

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"time"

	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

// LoginRequest contains intent and an optional prior admission from the trusted
// browser adapter. Caller-supplied identity/digest facts are not accepted.
type LoginRequest struct {
	Issuer, ReturnPath string
	Previous           *security.Admission
}

func (w *Workflow) BeginLogin(ctx context.Context, request LoginRequest) (oidc.LoginStart, error) {
	replaces := ""
	if request.Previous != nil {
		replaces = request.Previous.Authentication().SessionDigest
	}
	i, cut, err := w.current.LoginIssuer(ctx, request.Issuer)
	if err != nil {
		return oidc.LoginStart{}, ErrUnauthenticated
	}
	d, err := w.oidc.Fetcher.Discover(ctx, i)
	if err != nil {
		return oidc.LoginStart{}, ErrUnavailable
	}
	_, after, err := w.current.LoginIssuer(ctx, i.URL)
	if err != nil || after != cut {
		return oidc.LoginStart{}, ErrUnavailable
	}
	start, err := w.oidc.Logins.Begin(oidc.Trust{Issuer: i, Generation: cut.Generation, ConfigRevision: i.ConfigRevision}, d, request.ReturnPath, replaces, false)
	if err != nil {
		return oidc.LoginStart{}, ErrInvalidInput
	}
	if w.current.BindLoginOutput(ctx, cut, i.URL) != nil {
		return oidc.LoginStart{}, ErrUnavailable
	}
	return start, nil
}

// CallbackInput is untrusted, bounded transport input. Only Consume and the
// genuine exchange/verifier below turn it into opaque authentication evidence.
type CallbackInput struct {
	State, Transaction, Path, Issuer, Code string
	ProviderError                          bool
}
type CallbackResult struct {
	// Consumed permits transaction-cookie cleanup even when later work fails.
	// It is not a credential or permission to disclose any other output.
	Consumed, Purpose         bool
	Session, CSRF, ReturnPath string
	CreatedAt, ExpiresAt      time.Time
	Context                   context.Context
}

func (CallbackInput) String() string    { return "[redacted login callback input]" }
func (CallbackInput) GoString() string  { return "[redacted login callback input]" }
func (CallbackResult) String() string   { return "[redacted login callback result]" }
func (CallbackResult) GoString() string { return "[redacted login callback result]" }

func (w *Workflow) CompleteCallback(ctx context.Context, input CallbackInput) (result CallbackResult, err error) {
	completion, err := w.oidc.Logins.Consume(input.State, input.Transaction, input.Path, input.Issuer)
	if err != nil {
		return result, ErrUnauthenticated
	}
	result.Consumed = true
	id, purpose := completion.AuthorizationID()
	approved := false
	if purpose {
		defer func() {
			if !approved {
				w.current.RejectPurpose(id)
			}
		}()
	}
	if input.ProviderError || input.Code == "" {
		return result, ErrUnauthenticated
	}
	trust := completion.Trust()
	i, cut, e := w.current.LoginIssuer(ctx, trust.Issuer.URL)
	if e != nil || cut.Generation != trust.Generation || i.ConfigRevision != trust.ConfigRevision {
		return result, ErrUnauthenticated
	}
	// Consume is atomic and precedes this one exchange. Refresh after Apply uses
	// this same detached Code event, never another request to the IdP.
	tokens, err := w.oidc.Fetcher.ExchangeCode(ctx, trust, completion.Discovery(), w.oidc.Secrets, input.Code, completion.Verifier())
	if err != nil {
		return result, ErrUnauthenticated
	}
	producer, e := w.oidc.Credentials.Code(completion, tokens)
	if e != nil {
		return result, ErrUnauthenticated
	}
	if purpose {
		if _, e = w.current.CompletePurpose(ctx, id, producer); e != nil {
			return result, ErrUnauthenticated
		}
		approved = true
		if w.current.BindPublicMetadata(ctx, security.CurrentPurposeReturn) != nil {
			return result, ErrUnavailable
		}
	} else {
		issued, e := w.establishCurrentSession(ctx, completion, producer)
		if e != nil {
			return result, e
		}
		issued.Consumed = true
		return issued, nil
	}
	result.Purpose, result.Context = true, ctx
	return result, nil
}
func newSessionSecret() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}
func sessionDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
