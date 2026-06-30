// Package llmauth builds credential-bearing HTTP clients for core/llm provider
// clients. It lives in server so cloud SDK dependencies do not leak into core.
package llmauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// AzureOpenAIScope is the Entra ID scope for Azure OpenAI data-plane calls.
const AzureOpenAIScope = "https://cognitiveservices.azure.com/.default"

// GoogleCloudPlatformScope is the OAuth scope used by Vertex AI / Gemini APIs.
const GoogleCloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

// Option configures a credential-bearing HTTP client.
type Option func(*options)

type options struct {
	base    http.RoundTripper
	timeout time.Duration
}

// WithBaseTransport sets the transport that receives requests after auth is
// added. A nil transport is ignored and http.DefaultTransport is used.
func WithBaseTransport(base http.RoundTripper) Option {
	return func(o *options) {
		if base != nil {
			o.base = base
		}
	}
}

// WithTimeout sets the returned HTTP client's timeout. Non-positive values keep
// the zero timeout, matching the default http.Client behavior.
func WithTimeout(timeout time.Duration) Option {
	return func(o *options) {
		if timeout > 0 {
			o.timeout = timeout
		}
	}
}

func resolveOptions(opts []Option) options {
	o := options{base: http.DefaultTransport}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// NewAzureManagedIdentityHTTPClient returns an HTTP client that authenticates
// requests with an Entra ID Managed Identity token for Azure OpenAI.
func NewAzureManagedIdentityHTTPClient(opts ...Option) (*http.Client, error) {
	cred, err := azidentity.NewManagedIdentityCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("llmauth: azure managed identity credential: %w", err)
	}
	return NewAzureCredentialHTTPClient(cred, AzureOpenAIScope, opts...)
}

// NewAzureClientSecretHTTPClient returns an HTTP client that authenticates
// requests with an Entra ID service-principal (client-secret) token for Azure
// OpenAI. It is a convenience wrapper over NewAzureCredentialHTTPClient so
// callers need not import azidentity directly.
func NewAzureClientSecretHTTPClient(tenantID, clientID, clientSecret string, opts ...Option) (*http.Client, error) {
	cred, err := azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, nil)
	if err != nil {
		return nil, fmt.Errorf("llmauth: azure client secret credential: %w", err)
	}
	return NewAzureCredentialHTTPClient(cred, AzureOpenAIScope, opts...)
}

// NewAzureCredentialHTTPClient returns an HTTP client that injects bearer tokens
// acquired from cred. An empty scope defaults to AzureOpenAIScope.
func NewAzureCredentialHTTPClient(cred azcore.TokenCredential, scope string, opts ...Option) (*http.Client, error) {
	if cred == nil {
		return nil, errors.New("llmauth: nil azure token credential")
	}
	if scope == "" {
		scope = AzureOpenAIScope
	}
	o := resolveOptions(opts)
	return &http.Client{
		Transport: azureBearerTransport{credential: cred, scope: scope, base: o.base},
		Timeout:   o.timeout,
	}, nil
}

type azureBearerTransport struct {
	credential azcore.TokenCredential
	scope      string
	base       http.RoundTripper
}

func (t azureBearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := t.credential.GetToken(req.Context(), policy.TokenRequestOptions{Scopes: []string{t.scope}})
	if err != nil {
		return nil, fmt.Errorf("llmauth: azure token: %w", err)
	}
	next := req.Clone(req.Context())
	next.Header.Set("Authorization", "Bearer "+token.Token)
	return t.base.RoundTrip(next)
}

// NewGoogleDefaultHTTPClient returns an HTTP client that authenticates requests
// with Google Application Default Credentials for Vertex AI / Gemini.
func NewGoogleDefaultHTTPClient(ctx context.Context, opts ...Option) (*http.Client, error) {
	source, err := google.DefaultTokenSource(ctx, GoogleCloudPlatformScope)
	if err != nil {
		return nil, fmt.Errorf("llmauth: google default credentials: %w", err)
	}
	return NewGoogleTokenSourceHTTPClient(source, opts...)
}

// NewGoogleServiceAccountHTTPClient returns an HTTP client that authenticates
// requests with the provided Google service-account JSON for Vertex AI / Gemini.
func NewGoogleServiceAccountHTTPClient(ctx context.Context, credentialsJSON []byte, opts ...Option) (*http.Client, error) {
	creds, err := google.CredentialsFromJSON(ctx, credentialsJSON, GoogleCloudPlatformScope)
	if err != nil {
		return nil, fmt.Errorf("llmauth: google service account credentials: %w", err)
	}
	return NewGoogleTokenSourceHTTPClient(creds.TokenSource, opts...)
}

// NewGoogleTokenSourceHTTPClient returns an HTTP client that injects bearer
// tokens acquired from source.
func NewGoogleTokenSourceHTTPClient(source oauth2.TokenSource, opts ...Option) (*http.Client, error) {
	if source == nil {
		return nil, errors.New("llmauth: nil google token source")
	}
	o := resolveOptions(opts)
	return &http.Client{
		Transport: &oauth2.Transport{Source: oauth2.ReuseTokenSource(nil, source), Base: o.base},
		Timeout:   o.timeout,
	}, nil
}
