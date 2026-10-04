package oidc

import (
	"context"
	"slices"

	"github.com/anaregdesign/lantern/server/internal/security"
)

// ValidateIssuer proves the configured login flow and verifies pinned-algorithm
// public key material. A secret is read only from an exact operator binding and
// never sent to discovery/JWKS, returned to the caller or stored in sys:.
func (f *Fetcher) ValidateIssuer(ctx context.Context, issuer security.Issuer, secrets *SecretRegistry) error {
	issuer.Enabled = true
	discovery, err := f.Discover(ctx, issuer)
	if err != nil {
		return err
	}
	if !slices.Contains(discovery.ResponseTypes, "code") || !slices.Contains(discovery.CodeChallengeMethods, "S256") {
		return ErrInvalidDocument
	}
	var document struct {
		Keys []jsonKey `json:"keys"`
	}
	if err := f.GetJSON(ctx, discovery.JWKSURI, &document); err != nil {
		return err
	}
	if _, err := decodeKeys(document.Keys, issuer.Algorithms); err != nil {
		return err
	}
	if issuer.SecretRef != "" {
		if _, err := secrets.load(issuer, discovery.TokenEndpoint); err != nil {
			return err
		}
	}
	return nil
}
