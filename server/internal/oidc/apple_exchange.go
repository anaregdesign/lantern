package oidc

import (
	"context"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/anaregdesign/lantern/server/internal/security"
)

// NativeTokens retain only the bounded data needed to reverify one successful
// Apple exchange. No refresh token is retained or persisted.
type NativeTokens struct {
	idToken, accessToken string
	binding, exchange    [32]byte
}

func (NativeTokens) String() string   { return "[redacted native provider tokens]" }
func (NativeTokens) GoString() string { return "[redacted native provider tokens]" }

func nativeExchangeBinding(c NativeCompletion) [32]byte {
	if !c.credential.matches(c.attempt) || c.consumedAt.IsZero() {
		return [32]byte{}
	}
	t := c.attempt.transaction
	return evidenceDigest("apple-exchange-binding", struct {
		Trust                Trust
		Profile              security.CurrentProfile
		Attempt, Code, Nonce [32]byte
		Registration         security.NativeRegistrationPurpose `json:",omitzero"`
	}{t.trust, t.profile, c.attempt.id, evidenceDigest("authorization-code", c.credential.code), evidenceDigest("nonce", t.nonce), t.registration})
}

// ExchangeApple accepts only an opaque consumed Apple completion. It sends the
// operator's exact client_secret_post binding once, without Basic or PKCE. The
// workflow must never retry this operation after possible dispatch.
func (f *Fetcher) ExchangeApple(ctx context.Context, c NativeCompletion, registry *SecretRegistry) (NativeTokens, error) {
	binding := nativeExchangeBinding(c)
	if binding == [32]byte{} {
		return NativeTokens{}, ErrInvalidToken
	}
	t := c.attempt.transaction
	if t.client.Kind != security.AppleNative && t.client.Kind != security.AppleAndroidWeb || t.trust.Issuer.URL != security.AppleIssuer || validateNativeDiscovery(t.discovery) != nil {
		return NativeTokens{}, ErrInvalidToken
	}
	if c.exchangeStarted == nil || !c.exchangeStarted.CompareAndSwap(false, true) {
		return NativeTokens{}, ErrInvalidToken
	}
	secret, err := registry.loadClient(t.trust.Issuer.URL, t.client.ClientID, t.client.SecretRef, t.discovery.TokenEndpoint)
	if err != nil {
		return NativeTokens{}, err
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {c.credential.code}, "client_id": {t.client.ClientID}, "client_secret": {secret}}
	if t.client.Kind == security.AppleAndroidWeb {
		form.Set("redirect_uri", t.client.RedirectURI)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.discovery.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return NativeTokens{}, ErrFetch
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	response, err := f.do(req)
	if err != nil {
		return NativeTokens{}, ErrFetch
	}
	defer response.Body.Close()
	media, _, typeErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if typeErr != nil || media != "application/json" || response.StatusCode != http.StatusOK || response.ContentLength > 64<<10 || response.Header.Get("Content-Encoding") != "" {
		return NativeTokens{}, ErrFetch
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	var result struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
	}
	if err != nil || len(raw) > 64<<10 || decodeJSON(raw, &result) != nil || result.IDToken == "" || len(result.IDToken) > maxTokenBytes || len(result.AccessToken) > maxTokenBytes || len(result.RefreshToken) > maxTokenBytes || !strings.EqualFold(result.TokenType, "Bearer") {
		return NativeTokens{}, ErrInvalidToken
	}
	return NativeTokens{idToken: result.IDToken, accessToken: result.AccessToken, binding: binding, exchange: evidenceDigest("apple-exchange", struct{ Binding, ID, Access [32]byte }{binding, evidenceDigest("credential", result.IDToken), evidenceDigest("access-token", result.AccessToken)})}, nil
}
