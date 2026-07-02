package llmauth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

// countingCredential is a fake azcore.TokenCredential that counts GetToken
// calls and can be scripted to fail — the #854 cache-contract probe.
type countingCredential struct {
	mu      sync.Mutex
	calls   int
	expires time.Time
	fail    error
	block   chan struct{} // when non-nil, GetToken waits here (concurrency tests)
}

func (c *countingCredential) GetToken(ctx context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	c.mu.Lock()
	c.calls++
	n := c.calls
	fail := c.fail
	expires := c.expires
	block := c.block
	c.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return azcore.AccessToken{}, ctx.Err()
		}
	}
	if fail != nil {
		return azcore.AccessToken{}, fail
	}
	return azcore.AccessToken{Token: fmt.Sprintf("tok-%d", n), ExpiresOn: expires}, nil
}

func (c *countingCredential) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// TestCachedTokenCredential pins the #854 token-cache contract so
// correctness never again depends on MSAL internals: (a) N concurrent
// cold-start requests coalesce into exactly one GetToken (singleflight),
// (b) passage of the expiry margin triggers exactly one refresh, (c) a
// failed flight delivers the error to every waiter WITHOUT poisoning the
// cache — the next request starts a fresh flight.
func TestCachedTokenCredential(t *testing.T) {
	ctx := context.Background()
	opts := policy.TokenRequestOptions{Scopes: []string{AzureOpenAIScope}}

	t.Run("concurrent cold start coalesces to one GetToken", func(t *testing.T) {
		block := make(chan struct{})
		inner := &countingCredential{expires: time.Now().Add(time.Hour), block: block}
		cache := &cachedTokenCredential{inner: inner}

		const n = 16
		var wg sync.WaitGroup
		tokens := make([]string, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				tok, err := cache.GetToken(ctx, opts)
				if err != nil {
					t.Errorf("GetToken %d: %v", i, err)
					return
				}
				tokens[i] = tok.Token
			}(i)
		}
		// Give the herd time to pile onto the single flight, then release.
		time.Sleep(20 * time.Millisecond)
		close(block)
		wg.Wait()
		if got := inner.count(); got != 1 {
			t.Fatalf("GetToken calls = %d, want 1 (singleflight)", got)
		}
		for i, tok := range tokens {
			if tok != "tok-1" {
				t.Fatalf("waiter %d got %q, want the shared tok-1", i, tok)
			}
		}
	})

	t.Run("fresh token served from cache; margin passage refreshes once", func(t *testing.T) {
		inner := &countingCredential{expires: time.Now().Add(time.Hour)}
		cache := &cachedTokenCredential{inner: inner}
		for i := 0; i < 5; i++ {
			if _, err := cache.GetToken(ctx, opts); err != nil {
				t.Fatalf("GetToken: %v", err)
			}
		}
		if got := inner.count(); got != 1 {
			t.Fatalf("calls = %d, want 1 (cache hit)", got)
		}
		// Age the cached token into the refresh margin.
		cache.mu.Lock()
		cache.token.ExpiresOn = time.Now().Add(tokenRefreshMargin / 2)
		cache.mu.Unlock()
		for i := 0; i < 5; i++ {
			if _, err := cache.GetToken(ctx, opts); err != nil {
				t.Fatalf("GetToken after aging: %v", err)
			}
		}
		if got := inner.count(); got != 2 {
			t.Fatalf("calls = %d, want 2 (exactly one refresh)", got)
		}
	})

	t.Run("failed flight surfaces to all waiters and does not poison", func(t *testing.T) {
		boom := errors.New("aad down")
		block := make(chan struct{})
		inner := &countingCredential{expires: time.Now().Add(time.Hour), fail: boom, block: block}
		cache := &cachedTokenCredential{inner: inner}

		const n = 8
		var wg sync.WaitGroup
		errs := make([]error, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, errs[i] = cache.GetToken(ctx, opts)
			}(i)
		}
		time.Sleep(20 * time.Millisecond)
		close(block)
		wg.Wait()
		if got := inner.count(); got != 1 {
			t.Fatalf("calls = %d, want 1", got)
		}
		for i, err := range errs {
			if !errors.Is(err, boom) {
				t.Fatalf("waiter %d err = %v, want the flight error", i, err)
			}
		}
		// Not poisoned: the next request starts a fresh (now succeeding) flight.
		inner.mu.Lock()
		inner.fail = nil
		inner.block = nil
		inner.mu.Unlock()
		if _, err := cache.GetToken(ctx, opts); err != nil {
			t.Fatalf("retry after failed flight: %v", err)
		}
		if got := inner.count(); got != 2 {
			t.Fatalf("calls = %d, want 2 (one retry flight)", got)
		}
	})
}

// TestGoogleTransport_ReusesToken is the Google-side counting twin: the
// oauth2.ReuseTokenSource wrapper must serve repeated requests from one
// underlying token fetch.
func TestGoogleTransport_ReusesToken(t *testing.T) {
	var fetches int
	source := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "g-tok", Expiry: time.Now().Add(time.Hour)})
	counting := oauth2.TokenSource(countingTokenSource{inner: source, calls: &fetches})

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer g-tok" {
			t.Errorf("Authorization = %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	client, err := NewGoogleTokenSourceHTTPClient(counting)
	if err != nil {
		t.Fatalf("NewGoogleTokenSourceHTTPClient: %v", err)
	}
	for i := 0; i < 5; i++ {
		resp, err := client.Get(backend.URL)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		_ = resp.Body.Close()
	}
	if fetches != 1 {
		t.Fatalf("token fetches = %d, want 1 (ReuseTokenSource)", fetches)
	}
}

type countingTokenSource struct {
	inner oauth2.TokenSource
	calls *int
}

func (c countingTokenSource) Token() (*oauth2.Token, error) {
	*c.calls++
	return c.inner.Token()
}
