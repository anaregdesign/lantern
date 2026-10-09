package provider

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

// CurrentCredentialRuntime owns only credential verification for the private
// S1 composition. It neither opens the legacy Store nor activates listeners.
// The clock is the owner's qualified upper endpoint; it is not a qualification
// switch. Every verification still receives the owner's opaque installed view.
type CurrentCredentialRuntime struct{ runtime SecurityRuntime }

func NewCurrentCredentialRuntime(fetcher *oidc.Fetcher, browserOrigin string, now func() time.Time) (*CurrentCredentialRuntime, error) {
	if fetcher == nil || now == nil {
		return nil, oidc.ErrInvalidToken
	}
	if _, err := oidc.NewLoginTransactionsWithClock(browserOrigin, []string{"/"}, now); err != nil {
		return nil, err
	}
	return &CurrentCredentialRuntime{SecurityRuntime{mode: "oidc", keys: oidc.NewKeyCacheWithClock(fetcher, now), config: SecurityConfig{BrowserOrigin: browserOrigin}}}, nil
}
func (r *CurrentCredentialRuntime) Bearer(headers http.Header) (security.CurrentCredentialProducer, error) {
	return r.runtime.CurrentBearerProducer(headers)
}
func (r *CurrentCredentialRuntime) Code(completion oidc.LoginCompletion, tokens oidc.CodeTokens) (security.CurrentCredentialProducer, error) {
	return r.runtime.CurrentCodeProducer(completion, tokens)
}
func (r *CurrentCredentialRuntime) Browser(req *http.Request) (security.CurrentCredentialProducer, error) {
	return r.runtime.CurrentBrowserProducer(req)
}

// These private-profile producers deliberately read the supplied installed S1
// view. They never read the old fixed-writer Store or manufacture a genesis.
// Ordinary public Wire/RPC paths do not select them yet.
type currentCredentialProducer struct {
	keys       *oidc.KeyCache
	kind       string
	bearer     string
	cookie     string
	csrf       string
	origin     string
	completion oidc.LoginCompletion
	tokens     oidc.CodeTokens
}

func (currentCredentialProducer) String() string   { return "[redacted current credential producer]" }
func (currentCredentialProducer) GoString() string { return "[redacted current credential producer]" }

func (r *SecurityRuntime) CurrentBearerProducer(headers http.Header) (security.CurrentCredentialProducer, error) {
	if r == nil || r.mode != "oidc" || r.keys == nil {
		return nil, oidc.ErrInvalidToken
	}
	values := headers.Values("Authorization")
	if len(values) != 1 || len(values[0]) > (16<<10)+7 {
		return nil, oidc.ErrInvalidToken
	}
	scheme, raw, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || raw == "" || strings.ContainsAny(raw, " \t\r\n") {
		return nil, oidc.ErrInvalidToken
	}
	kind := "access"
	if strings.HasPrefix(raw, security.MachineTokenPrefix) {
		if _, valid := security.MachineTokenDigest(raw); !valid {
			return nil, oidc.ErrInvalidToken
		}
		kind = "machine"
	}
	return currentCredentialProducer{keys: r.keys, kind: kind, bearer: raw}, nil
}

func (r *SecurityRuntime) CurrentCodeProducer(completion oidc.LoginCompletion, tokens oidc.CodeTokens) (security.CurrentCredentialProducer, error) {
	if r == nil || r.mode != "oidc" || r.keys == nil {
		return nil, oidc.ErrInvalidToken
	}
	return currentCredentialProducer{keys: r.keys, kind: "code", completion: completion, tokens: tokens}, nil
}

func (r *SecurityRuntime) CurrentBrowserProducer(req *http.Request) (security.CurrentCredentialProducer, error) {
	if r == nil || req == nil || r.browserOrigin(req, true) != nil || len(req.Header.Values("Authorization")) != 0 {
		return nil, errBrowserBoundary
	}
	cookie, err := browserCookie(req, sessionCookieName)
	if err != nil {
		return nil, err
	}
	csrf, err := browserCookie(req, csrfCookieName)
	if err != nil || !exactBrowserHeader(req.Header, browserCSRFHeader, csrf) {
		return nil, errBrowserBoundary
	}
	return currentCredentialProducer{kind: "session", cookie: cookie, csrf: csrf, origin: r.config.BrowserOrigin}, nil
}

func (p currentCredentialProducer) VerifyCurrentCredential(ctx context.Context, view security.CurrentCredentialView) (security.CurrentCredentialFacts, error) {
	if err := ctx.Err(); err != nil {
		return security.CurrentCredentialFacts{}, err
	}
	low, high, err := view.TimeBounds()
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
	if p.kind == "session" {
		if p.origin != view.BrowserOrigin() {
			return security.CurrentCredentialFacts{}, errBrowserBoundary
		}
		s, known := snapshot.Session(browserDigest(p.cookie))
		_, _, active := snapshot.SessionAccess(s.Digest, high)
		if !known || !active || low.Before(s.CreatedAt) || subtle.ConstantTimeCompare([]byte(s.CSRFDigest), []byte(browserDigest(p.csrf))) != 1 {
			return security.CurrentCredentialFacts{}, errBrowserBoundary
		}
		return security.CurrentCredentialFacts{Session: &s, Origin: requestCredentialCommitment("browser-origin", p.origin), CSRF: sha256.Sum256([]byte("lantern/current-CSRF/v2\x00" + s.Digest + "\x00" + p.csrf))}, nil
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
	return security.CurrentCredentialFacts{Token: &event}, nil
}
