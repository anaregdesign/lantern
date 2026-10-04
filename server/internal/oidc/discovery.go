package oidc

import (
	"context"
	"strings"

	"github.com/anaregdesign/lantern/server/internal/security"
)

// Discovery contains only endpoints used by the implementation. The original
// Issuer must match exactly; metadata never changes the registered identity.
type Discovery struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	JWKSURI               string   `json:"jwks_uri"`
	ResponseTypes         []string `json:"response_types_supported"`
	CodeChallengeMethods  []string `json:"code_challenge_methods_supported"`
}

func (f *Fetcher) Discover(ctx context.Context, registered security.Issuer) (Discovery, error) {
	if !registered.Enabled {
		return Discovery{}, ErrInvalidToken
	}
	if _, err := endpointURL(registered.URL); err != nil {
		return Discovery{}, err
	}
	var document Discovery
	if err := f.GetJSON(ctx, strings.TrimSuffix(registered.URL, "/")+"/.well-known/openid-configuration", &document); err != nil {
		return Discovery{}, err
	}
	if document.Issuer != registered.URL {
		return Discovery{}, ErrInvalidDocument
	}
	for _, endpoint := range []string{document.AuthorizationEndpoint, document.TokenEndpoint, document.JWKSURI} {
		if _, err := endpointURL(endpoint); err != nil {
			return Discovery{}, ErrInvalidDocument
		}
	}
	return document, nil
}
