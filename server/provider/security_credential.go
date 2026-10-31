package provider

import (
	"net/http"
	"time"

	"github.com/anaregdesign/lantern/server/internal/authn"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/internal/securitytransport"
)

// CurrentCredentialRuntime composes the same production adapters for private
// current qualification. It opens no Store, authority or listener.
type CurrentCredentialRuntime struct {
	credentials *authn.Credentials
	transport   *securitytransport.CredentialAdapter
}

func NewCurrentCredentialRuntime(fetcher *oidc.Fetcher, browserOrigin string, now func() time.Time) (*CurrentCredentialRuntime, error) {
	if fetcher == nil || now == nil {
		return nil, oidc.ErrInvalidToken
	}
	if _, err := oidc.NewLoginTransactionsWithClock(browserOrigin, []string{"/"}, now); err != nil {
		return nil, err
	}
	credentials := authn.NewCredentials(oidc.NewKeyCacheWithClock(fetcher, now))
	return &CurrentCredentialRuntime{credentials: credentials, transport: securitytransport.NewCredentialAdapter("oidc", securitytransport.BrowserConfig{BrowserOrigin: browserOrigin}, credentials)}, nil
}
func (r *CurrentCredentialRuntime) Bearer(headers http.Header) (security.CurrentCredentialProducer, error) {
	return r.transport.CurrentBearerProducer(headers)
}
func (r *CurrentCredentialRuntime) Code(completion oidc.LoginCompletion, tokens oidc.CodeTokens) (security.CurrentCredentialProducer, error) {
	return r.credentials.Code(completion, tokens)
}
func (r *CurrentCredentialRuntime) Browser(req *http.Request) (security.CurrentCredentialProducer, error) {
	return r.transport.CurrentBrowserProducer(req)
}
