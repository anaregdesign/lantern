package securitytransport

import (
	"context"
	"time"

	"github.com/anaregdesign/lantern/server/internal/authn"
	"github.com/anaregdesign/lantern/server/internal/security"
)

// AuthenticationOptions borrows only the resources used by public transport.
// Provider owns their construction, certification and lifecycle.
type AuthenticationOptions struct {
	Credentials   *CredentialAdapter
	Workflow      *authn.Workflow
	Current       *security.CurrentAuthority
	Now           func() time.Time
	Ready         func(context.Context) bool
	Output        func() *security.CurrentOutput
	AttemptPrefix string
}

type AuthenticationAdapter struct {
	*CredentialAdapter
	workflow      *authn.Workflow
	current       *security.CurrentAuthority
	now           func() time.Time
	ready         func(context.Context) bool
	output        func() *security.CurrentOutput
	attemptPrefix string
}

func NewAuthenticationAdapter(o AuthenticationOptions) *AuthenticationAdapter {
	return &AuthenticationAdapter{CredentialAdapter: o.Credentials, workflow: o.Workflow, current: o.Current, now: o.Now, ready: o.Ready, output: o.Output, attemptPrefix: o.AttemptPrefix}
}
