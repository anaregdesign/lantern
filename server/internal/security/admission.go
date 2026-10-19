package security

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"
)

var ErrAuthorityUnavailable = errors.New("authenticated serving authority unavailable")

// Admission binds verified authentication to one immutable policy cut and a
// locally checked serving fence. Construct it only after credential verification.
// A policy snapshot alone is insufficient to construct serving authority.
type Admission struct {
	identity       Identity
	authTime       time.Time
	expiresAt      time.Time
	csrfToken      string
	browser        bool
	access         *Access
	authentication Authentication
	current        *currentPublicAdmission
}

func (a *Admission) Check(ctx context.Context, _ time.Time) error {
	if a == nil || a.current == nil {
		return ErrAuthorityUnavailable
	}
	return a.current.check(ctx)
}
func (a *Admission) Access() *Access      { return a.access }
func (a *Admission) Identity() Identity   { return a.identity }
func (a *Admission) AuthTime() time.Time  { return a.authTime }
func (a *Admission) ExpiresAt() time.Time { return a.expiresAt }
func (a *Admission) Browser() bool        { return a.browser }
func (a *Admission) CSRFToken() string    { return a.csrfToken }

// Snapshot is captured policy data; it does not grant serving authority.
func (a *Admission) Snapshot() *Snapshot {
	if a == nil || a.current == nil {
		return nil
	}
	return a.current.snapshot
}

// WithBrowserProof attaches the separately verified browser-session CSRF proof.
// It cannot change the authenticated identity or captured policy cut.
func (a *Admission) WithBrowserProof(csrf string) *Admission {
	if a == nil || a.current == nil {
		return a
	}
	if a.current != nil {
		session := a.current.credential.claim.Session
		if session == nil || sha256.Sum256([]byte("lantern/current-CSRF/v2\x00"+session.Digest+"\x00"+csrf)) != a.current.credential.claim.CSRF {
			return a
		}
	}
	copy := *a
	copy.browser, copy.csrfToken = true, csrf
	return &copy
}

// ScopeBinding is stable only within this exact Principal/policy/generation
// cut. Public cursors and caches must include it, never a mutable Role lookup.
func (a *Admission) ScopeBinding() [32]byte {
	if a == nil || a.current == nil {
		return [32]byte{}
	}
	return a.current.binding
}

// UID-aware views bind the canonical selector subject into every cursor scope.
func bindSubjectScope(base [32]byte, access *Access) [32]byte {
	uid := access.BindingDigest()
	if uid == [32]byte{} {
		return base
	}
	return s2cHash("public-subject-scope-v1", struct{ Base, Subject [32]byte }{base, uid})
}

type admissionContextKey struct{}

func WithAdmission(ctx context.Context, admission *Admission) context.Context {
	return context.WithValue(ctx, admissionContextKey{}, admission)
}

func AdmissionFromContext(ctx context.Context) (*Admission, bool) {
	admission, ok := ctx.Value(admissionContextKey{}).(*Admission)
	return admission, ok && admission != nil
}
