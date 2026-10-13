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
	if len(registered.NativeClients) != 0 {
		if security.ValidateIssuerConfiguration(registered) != nil || validateNativeDiscovery(document) != nil {
			return Discovery{}, ErrInvalidDocument
		}
	}
	return document, nil
}

// Native profiles pin provider-owned destinations before any JWKS fetch or
// secret-bearing exchange. No URL from a login request reaches this boundary.
func validateNativeDiscovery(d Discovery) error {
	switch d.Issuer {
	case security.GoogleIssuer:
		if d.AuthorizationEndpoint != "https://accounts.google.com/o/oauth2/v2/auth" || d.TokenEndpoint != "https://oauth2.googleapis.com/token" || d.JWKSURI != "https://www.googleapis.com/oauth2/v3/certs" {
			return ErrInvalidDocument
		}
	case security.AppleIssuer:
		if d.AuthorizationEndpoint != "https://appleid.apple.com/auth/authorize" || d.TokenEndpoint != "https://appleid.apple.com/auth/token" || d.JWKSURI != "https://appleid.apple.com/auth/keys" {
			return ErrInvalidDocument
		}
	default:
		return ErrInvalidDocument
	}
	return nil
}
