// Package provider: auth.go implements the opt-in static bearer-token
// authentication tier (#850) — Redis requirepass-class protection for the
// data plane. LANTERN_AUTH_TOKENS (comma-separated, so {old,new} can
// coexist during zero-downtime rotation) arms a Connect interceptor that
// rejects any unary or streaming call whose "Authorization: Bearer <token>"
// header does not match a configured token. Unset (the default) keeps
// today's open behaviour.
//
// Comparison is constant-time over SHA-256 digests: hashing both sides to a
// fixed length first means neither token length nor prefix equality leaks
// through timing, and crypto/subtle.ConstantTimeCompare does the rest.
//
// Exemptions are structural where possible: grpc.health.v1.Health and
// gRPC reflection are mounted as separate handlers outside the Lantern
// service interceptor chain, so Kubernetes gRPC probes (which cannot attach
// headers) keep working with auth enabled. When
// LANTERN_AUTH_EXEMPT_REFLECTION=false the reflection mounts are wrapped
// with RequireHTTP so schema discovery also demands the token.
//
// This tier deliberately has no users, ACLs, scopes, or token expiry; mTLS
// (LANTERN_TLS_CLIENT_CA_FILE) remains the strong option. Bearer tokens
// over plaintext h2c are sniffable — pair with TLS outside trusted
// networks (documented in the runbook decision table).
package provider

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect"

	domainmetrics "github.com/anaregdesign/lantern/server/metrics"
)

// AuthConfig carries the bearer-token auth settings (#850).
//
//   - LANTERN_AUTH_TOKENS             (default "" = auth disabled) —
//     comma-separated accepted tokens; multiple entries exist for
//     zero-downtime rotation: add the new token on every server, roll the
//     clients, then drop the old token.
//   - LANTERN_AUTH_EXEMPT_REFLECTION  (default true) — keep gRPC server
//     reflection reachable without a token. Health checks are always
//     exempt (Kubernetes gRPC probes cannot attach headers).
type AuthConfig struct {
	Tokens           []string
	ExemptReflection bool
}

// AuthInterceptor enforces AuthConfig on every Connect call it wraps —
// unary and streaming alike — and can also guard plain HTTP mounts via
// RequireHTTP. The zero value (no tokens) is disabled and enforces
// nothing.
type AuthInterceptor struct {
	// hashes holds the SHA-256 of each accepted token. Comparing digests
	// keeps the comparison constant-time regardless of attacker-supplied
	// token length.
	hashes     [][sha256.Size]byte
	exemptRefl bool
	rejectHook func()
}

// NewAuthInterceptor builds the interceptor from AuthConfig. Empty and
// whitespace-only token entries are dropped so a trailing comma in the env
// value cannot silently admit the empty token.
func NewAuthInterceptor(cfg AuthConfig) *AuthInterceptor {
	a := &AuthInterceptor{exemptRefl: cfg.ExemptReflection}
	for _, t := range cfg.Tokens {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		a.hashes = append(a.hashes, sha256.Sum256([]byte(t)))
	}
	return a
}

// WithRejectHook registers a callback fired once per rejected request —
// wired to lantern_auth_rejected_total.
func (a *AuthInterceptor) WithRejectHook(hook func()) *AuthInterceptor {
	a.rejectHook = hook
	return a
}

// Enabled reports whether any token is configured. The listener skips the
// interceptor entirely when false, so the disabled path costs nothing.
func (a *AuthInterceptor) Enabled() bool { return len(a.hashes) > 0 }

// ExemptReflection reports the LANTERN_AUTH_EXEMPT_REFLECTION setting for
// the listener's reflection mounts.
func (a *AuthInterceptor) ExemptReflection() bool { return a.exemptRefl }

// errUnauthenticated is the single rejection shape: it deliberately does
// not distinguish "no header" from "wrong token" so probing responses leak
// nothing.
var errUnauthenticated = errors.New("missing or invalid bearer token (Authorization: Bearer <token>)")

// authorize validates the Authorization header. Constant-time over the
// digest; iterates every configured token unconditionally so the match
// position does not leak either.
func (a *AuthInterceptor) authorize(header http.Header) error {
	const prefix = "Bearer "
	raw := header.Get("Authorization")
	var digest [sha256.Size]byte
	ok := 0
	if strings.HasPrefix(raw, prefix) {
		digest = sha256.Sum256([]byte(raw[len(prefix):]))
		for i := range a.hashes {
			ok |= subtle.ConstantTimeCompare(digest[:], a.hashes[i][:])
		}
	}
	if ok == 1 {
		return nil
	}
	if a.rejectHook != nil {
		a.rejectHook()
	}
	return connect.NewError(connect.CodeUnauthenticated, errUnauthenticated)
}

// WrapUnary implements connect.Interceptor.
func (a *AuthInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if err := a.authorize(req.Header()); err != nil {
			return nil, err
		}
		return next(ctx, req)
	}
}

// WrapStreamingClient implements connect.Interceptor. Server-side only —
// pass-through.
func (a *AuthInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler implements connect.Interceptor: Subscribe /
// BackupSnapshot / replication streams present the same bearer token.
func (a *AuthInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if err := a.authorize(conn.RequestHeader()); err != nil {
			return err
		}
		return next(ctx, conn)
	}
}

// RequireHTTP guards a plain http.Handler mount (the reflection handlers
// when LANTERN_AUTH_EXEMPT_REFLECTION=false) with the same token check.
func (a *AuthInterceptor) RequireHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := a.authorize(r.Header); err != nil {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// NewAuthInterceptorProvider wires the shared *AuthInterceptor with the
// lantern_auth_rejected_total hook. Always constructed; the listener keys
// off Enabled().
func NewAuthInterceptorProvider(cfg AuthConfig, dm *domainmetrics.DomainMetrics) *AuthInterceptor {
	return NewAuthInterceptor(cfg).WithRejectHook(dm.OnAuthRejected)
}
