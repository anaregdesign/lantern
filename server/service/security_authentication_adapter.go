package service

import (
	"context"
	"time"

	"github.com/anaregdesign/lantern/server/internal/authn"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

// AuthenticationOptions borrows only the resources used by public transport.
// Provider owns their construction, certification and lifecycle.
type AuthenticationOptions struct {
	Credentials    *CredentialAdapter
	Workflow       *authn.Workflow
	Store          *security.Store
	Current        *security.CurrentAuthority
	Verifier       *oidc.Verifier
	Now            func() time.Time
	AuthorityCheck func(context.Context, *security.Revision) error
	LeaseExpiry    func() (time.Time, bool)
	WriterEnabled  func() bool
	Ready          func(context.Context) bool
	Output         func() *security.CurrentOutput
	AttemptPrefix  string
}

type AuthenticationAdapter struct {
	*CredentialAdapter
	workflow       *authn.Workflow
	store          *security.Store
	current        *security.CurrentAuthority
	verifier       *oidc.Verifier
	now            func() time.Time
	authorityCheck func(context.Context, *security.Revision) error
	leaseExpiry    func() (time.Time, bool)
	writerEnabled  func() bool
	ready          func(context.Context) bool
	output         func() *security.CurrentOutput
	attemptPrefix  string
}

func NewAuthenticationAdapter(o AuthenticationOptions) *AuthenticationAdapter {
	return &AuthenticationAdapter{CredentialAdapter: o.Credentials, workflow: o.Workflow, store: o.Store, current: o.Current, verifier: o.Verifier, now: o.Now, authorityCheck: o.AuthorityCheck, leaseExpiry: o.LeaseExpiry, writerEnabled: o.WriterEnabled, ready: o.Ready, output: o.Output, attemptPrefix: o.AttemptPrefix}
}
