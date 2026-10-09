package security

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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
	revision       *Revision
	access         *Access
	fence          func(context.Context, *Revision) error
	authentication Authentication
	current        *currentPublicAdmission
}

func NewAdmission(identity Identity, authTime, expiresAt time.Time, revision *Revision,
	fence func(context.Context, *Revision) error) (*Admission, error) {
	if !identity.valid() || revision == nil || expiresAt.IsZero() || fence == nil {
		return nil, ErrAuthorityUnavailable
	}
	access, known := revision.snapshot.AccessFor(identity)
	if !known {
		return nil, ErrPermissionDenied
	}
	return &Admission{identity: identity, authTime: authTime, expiresAt: expiresAt,
		revision: revision, access: access, fence: fence}, nil
}

func (a *Admission) Check(ctx context.Context, now time.Time) error {
	if a != nil && a.current != nil {
		return a.current.check(ctx)
	}
	if a == nil || !now.Before(a.expiresAt) || a.fence == nil {
		return ErrAuthorityUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return a.fence(ctx, a.revision)
}

func (a *Admission) Revision() *Revision  { return a.revision }
func (a *Admission) Access() *Access      { return a.access }
func (a *Admission) Identity() Identity   { return a.identity }
func (a *Admission) AuthTime() time.Time  { return a.authTime }
func (a *Admission) ExpiresAt() time.Time { return a.expiresAt }
func (a *Admission) Browser() bool        { return a.browser }
func (a *Admission) CSRFToken() string    { return a.csrfToken }

// Snapshot is policy data captured by this admission, not fresh authority.
// Current-profile admissions deliberately have no legacy signed Revision.
func (a *Admission) Snapshot() *Snapshot {
	if a == nil {
		return nil
	}
	if a.current != nil {
		return a.current.snapshot
	}
	if a.revision == nil {
		return nil
	}
	return a.revision.snapshot
}

// WithBrowserProof attaches the separately verified browser-session CSRF proof.
// It cannot change the authenticated identity or captured policy cut.
func (a *Admission) WithBrowserProof(csrf string) *Admission {
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
	if a != nil && a.current != nil {
		return a.current.binding
	}
	if a == nil || a.revision == nil {
		return [32]byte{}
	}
	encoded, _ := json.Marshal(struct {
		Identity   Identity
		Generation [16]byte
		Revision   uint64
		Digest     [32]byte
	}{a.identity, a.revision.generation, a.revision.sequence, a.revision.digest})
	return sha256.Sum256(encoded)
}

type admissionContextKey struct{}

func WithAdmission(ctx context.Context, admission *Admission) context.Context {
	return context.WithValue(ctx, admissionContextKey{}, admission)
}

func AdmissionFromContext(ctx context.Context) (*Admission, bool) {
	admission, ok := ctx.Value(admissionContextKey{}).(*Admission)
	return admission, ok && admission != nil
}
