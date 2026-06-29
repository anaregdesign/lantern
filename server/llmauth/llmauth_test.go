package llmauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"golang.org/x/oauth2"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestAzureCredentialHTTPClientInjectsBearerToken(t *testing.T) {
	cred := &fakeAzureCredential{token: "azure-token"}
	var gotAuth string
	client, err := NewAzureCredentialHTTPClient(cred, "", WithBaseTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotAuth = r.Header.Get("Authorization")
		return textResponse(http.StatusOK, "ok"), nil
	})))
	if err != nil {
		t.Fatalf("NewAzureCredentialHTTPClient: %v", err)
	}

	resp, err := client.Get("https://example.invalid/test")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	_ = resp.Body.Close()

	if gotAuth != "Bearer "+cred.token {
		t.Errorf("Authorization = %q, want bearer token", gotAuth)
	}
	if len(cred.scopes) != 1 || cred.scopes[0] != AzureOpenAIScope {
		t.Errorf("scopes = %v, want %q", cred.scopes, AzureOpenAIScope)
	}
}

func TestGoogleTokenSourceHTTPClientInjectsBearerToken(t *testing.T) {
	const token = "google-token"
	var gotAuth string
	client, err := NewGoogleTokenSourceHTTPClient(
		oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token, TokenType: "Bearer"}),
		WithBaseTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
			gotAuth = r.Header.Get("Authorization")
			return textResponse(http.StatusOK, "ok"), nil
		})),
	)
	if err != nil {
		t.Fatalf("NewGoogleTokenSourceHTTPClient: %v", err)
	}

	resp, err := client.Get("https://example.invalid/test")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	_ = resp.Body.Close()

	if gotAuth != "Bearer "+token {
		t.Errorf("Authorization = %q, want bearer token", gotAuth)
	}
}

func TestConstructorsRejectNilAuthSources(t *testing.T) {
	if _, err := NewAzureCredentialHTTPClient(nil, "scope"); err == nil {
		t.Fatal("NewAzureCredentialHTTPClient nil credential: got nil err")
	}
	if _, err := NewGoogleTokenSourceHTTPClient(nil); err == nil {
		t.Fatal("NewGoogleTokenSourceHTTPClient nil source: got nil err")
	}
}

func TestAzureClientSecretHTTPClientBuilds(t *testing.T) {
	client, err := NewAzureClientSecretHTTPClient(
		"00000000-0000-0000-0000-000000000000",
		"11111111-1111-1111-1111-111111111111",
		"secret",
	)
	if err != nil {
		t.Fatalf("NewAzureClientSecretHTTPClient: %v", err)
	}
	if client == nil {
		t.Fatal("nil client")
	}
	if _, ok := client.Transport.(azureBearerTransport); !ok {
		t.Errorf("transport = %T, want azureBearerTransport", client.Transport)
	}
	// An invalid (empty) tenant ID must surface as a construction error.
	if _, err := NewAzureClientSecretHTTPClient("", "client", "secret"); err == nil {
		t.Error("empty tenant ID: got nil err, want error")
	}
}

func TestAuthTransportReturnsTokenErrors(t *testing.T) {
	want := errors.New("no token")
	client, err := NewAzureCredentialHTTPClient(&fakeAzureCredential{err: want}, "scope", WithBaseTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("base transport should not be called after token error")
		return nil, nil
	})))
	if err != nil {
		t.Fatalf("NewAzureCredentialHTTPClient: %v", err)
	}

	_, err = client.Get("https://example.invalid/test")
	if !errors.Is(err, want) {
		t.Fatalf("Get err = %v, want %v", err, want)
	}
}

type fakeAzureCredential struct {
	token  string
	scopes []string
	err    error
}

func (f *fakeAzureCredential) GetToken(ctx context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error) {
	f.scopes = append(f.scopes[:0], opts.Scopes...)
	if f.err != nil {
		return azcore.AccessToken{}, f.err
	}
	return azcore.AccessToken{Token: f.token, ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func textResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}
