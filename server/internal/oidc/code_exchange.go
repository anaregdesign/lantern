package oidc

import (
	"context"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

// CodeTokens are server-private evidence. They are never persisted in browser
// storage or returned by the Admin session API.
type CodeTokens struct {
	idToken     string
	accessToken string
}

func (t CodeTokens) IDToken() string     { return t.idToken }
func (t CodeTokens) AccessToken() string { return t.accessToken }
func (t CodeTokens) String() string      { return "[redacted OIDC tokens]" }

func (f *Fetcher) ExchangeCode(ctx context.Context, trust Trust, discovery Discovery, registry *SecretRegistry, code, verifier string) (CodeTokens, error) {
	if !trust.Issuer.Enabled || discovery.Issuer != trust.Issuer.URL || len(code) == 0 || len(code) > 4096 ||
		len(verifier) < 43 || len(verifier) > 128 {
		return CodeTokens{}, ErrInvalidToken
	}
	if _, err := endpointURL(discovery.TokenEndpoint); err != nil {
		return CodeTokens{}, err
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier},
		"redirect_uri": {trust.Issuer.RedirectURI}, "client_id": {trust.Issuer.ClientID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, discovery.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return CodeTokens{}, ErrFetch
	}
	if trust.Issuer.SecretRef != "" {
		secret, err := registry.load(trust.Issuer, discovery.TokenEndpoint)
		if err != nil {
			return CodeTokens{}, err
		}
		// OAuth client_secret_basic encodes both credentials as form components
		// before HTTP Basic encoding (RFC 6749 section 2.3.1).
		req.SetBasicAuth(url.QueryEscape(trust.Issuer.ClientID), url.QueryEscape(secret))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	response, err := f.client.Do(req)
	if err != nil {
		return CodeTokens{}, ErrFetch
	}
	defer response.Body.Close()
	contentType, _, typeErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if typeErr != nil || contentType != "application/json" || response.StatusCode != http.StatusOK || response.ContentLength > 64<<10 || response.Header.Get("Content-Encoding") != "" {
		return CodeTokens{}, ErrFetch
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	var result struct {
		IDToken     string `json:"id_token"`
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err != nil || len(raw) > 64<<10 || decodeJSON(raw, &result) != nil || result.IDToken == "" ||
		len(result.IDToken) > maxTokenBytes || len(result.AccessToken) > maxTokenBytes || !strings.EqualFold(result.TokenType, "Bearer") {
		return CodeTokens{}, ErrInvalidToken
	}
	return CodeTokens{idToken: result.IDToken, accessToken: result.AccessToken}, nil
}
