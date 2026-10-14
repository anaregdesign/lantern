// Package authn orchestrates Server login and sessions using the existing OIDC
// verifier and security owners. It neither owns transport nor opens state.
package authn

import (
	"context"
	"errors"
	"time"

	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

var (
	ErrInvalidInput    = errors.New("invalid login/session input")
	ErrUnauthenticated = errors.New("login/session authentication rejected")
	ErrUnavailable     = errors.New("login/session workflow unavailable")
)

// OIDCResources are borrowed from trusted composition; no resource is closed here.
type OIDCResources struct {
	Fetcher      *oidc.Fetcher
	Secrets      *oidc.SecretRegistry
	Verifier     *oidc.Verifier
	Credentials  *Credentials
	Logins       *oidc.LoginTransactions
	NativeLogins *oidc.NativeTransactions
	Now          func() time.Time
}

type LegacyAuthority struct {
	Store          *security.Store
	Authorizations *security.ManagementAuthorizations
	Check          func(context.Context, *security.Revision) error
	Enforced       func(security.ChangeResult) bool
}

type Workflow struct {
	oidc          OIDCResources
	legacy        LegacyAuthority
	current       *security.CurrentAuthority
	attemptPrefix string
	registrations *nativeRegistrationProofs
}

func validResources(r OIDCResources) bool {
	return r.Fetcher != nil && r.Secrets != nil && r.Verifier != nil && r.Credentials != nil && r.Logins != nil && r.Now != nil
}
func NewCurrentWorkflow(owner *security.CurrentAuthority, resources OIDCResources, attemptPrefix string) (*Workflow, error) {
	if owner == nil || owner.Profile().Binding() == "" || !validResources(resources) || attemptPrefix == "" {
		return nil, ErrUnavailable
	}
	return &Workflow{current: owner, oidc: resources, attemptPrefix: attemptPrefix, registrations: newNativeRegistrationProofs(owner.TimeBounds, owner.DiscardRegistration)}, nil
}

// Close joins only Workflow-owned registration calls/proofs. Borrowed OIDC and
// durable authority resources keep their existing composition-owned lifecycle.
func (w *Workflow) Close() {
	if w != nil {
		w.registrations.close()
	}
}
func NewLegacyWorkflow(owner LegacyAuthority, resources OIDCResources) (*Workflow, error) {
	if owner.Store == nil || owner.Authorizations == nil || owner.Check == nil || owner.Enforced == nil || !validResources(resources) {
		return nil, ErrUnavailable
	}
	return &Workflow{legacy: owner, oidc: resources}, nil
}
