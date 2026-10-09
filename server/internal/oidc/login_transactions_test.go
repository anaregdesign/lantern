package oidc

import (
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/security"
)

func loginTransactionFixture(t *testing.T) (*LoginTransactions, Trust, Discovery) {
	t.Helper()
	manager, err := NewLoginTransactions("https://admin.example", []string{"/", "/security/roles"})
	if err != nil {
		t.Fatal(err)
	}
	issuer := security.Issuer{URL: "https://idp.example/realm", Enabled: true, ClientID: "admin", ConfigRevision: 1, Algorithms: []string{"EdDSA"}}
	issuer.RedirectURI = "https://admin.example" + CallbackPath(issuer.URL)
	return manager, Trust{Issuer: issuer, Generation: [16]byte{1}, ConfigRevision: 1}, Discovery{Issuer: issuer.URL, AuthorizationEndpoint: "https://idp.example/auth", TokenEndpoint: "https://idp.example/token", JWKSURI: "https://idp.example/keys", ResponseTypes: []string{"code"}, CodeChallengeMethods: []string{"S256"}}
}

func TestLoginTransactionsCurrentQualifiedBoundsAndAttemptAffinity(t *testing.T) {
	_, trust, discovery := loginTransactionFixture(t)
	low := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	high := low.Add(20 * time.Millisecond)
	bounds := func() (time.Time, time.Time, error) { return low, high, nil }
	process := [32]byte{1}
	prefix := "v2.2." + base64.RawURLEncoding.EncodeToString(process[:])
	m, err := NewLoginTransactionsWithBounds("https://admin.example", []string{"/"}, bounds, prefix)
	if err != nil {
		t.Fatal(err)
	}
	start, err := m.Begin(trust, discovery, "/", "", false)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(start.AuthorizationURL)
	state := u.Query().Get("state")
	if !strings.HasPrefix(state, prefix+".") {
		t.Fatal("missing process routing identity")
	}
	created := high
	if _, err := m.Consume(state, start.TransactionCookie, CallbackPath(trust.Issuer.URL), ""); err == nil {
		t.Fatal("upper endpoint substituted for strict start")
	}
	low, high = low.Add(time.Second), high.Add(time.Second)
	other, err := NewLoginTransactionsWithBounds("https://admin.example", []string{"/"}, bounds, "v2.3."+base64.RawURLEncoding.EncodeToString(process[:]))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Consume(state, start.TransactionCookie, CallbackPath(trust.Issuer.URL), ""); err == nil {
		t.Fatal("another node accepted attempt")
	}
	restarted, err := NewLoginTransactionsWithBounds("https://admin.example", []string{"/"}, bounds, prefix)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Consume(state, start.TransactionCookie, CallbackPath(trust.Issuer.URL), ""); err == nil {
		t.Fatal("routing hint became restored attempt authority")
	}
	c, err := m.Consume(state, start.TransactionCookie, CallbackPath(trust.Issuer.URL), "")
	if err != nil || c.transaction.createdAt != created || c.consumedAt != low {
		t.Fatal("Code event lost conservative endpoints", err)
	}
	if _, err := m.Consume(state, start.TransactionCookie, CallbackPath(trust.Issuer.URL), ""); err == nil {
		t.Fatal("current Code event consumed twice")
	}
	start, err = m.Begin(trust, discovery, "/", "", false)
	if err != nil {
		t.Fatal(err)
	}
	u, _ = url.Parse(start.AuthorizationURL)
	low, high = start.ExpiresAt.Add(-time.Millisecond), start.ExpiresAt
	if _, err := m.Consume(u.Query().Get("state"), start.TransactionCookie, CallbackPath(trust.Issuer.URL), ""); err == nil {
		t.Fatal("lower endpoint substituted for strict expiry")
	}
}
func TestLoginTransactionsPKCESingleUseMixUpAndRestart(t *testing.T) {
	manager, trust, discovery := loginTransactionFixture(t)
	start, err := manager.Begin(trust, discovery, "/security/roles", "", false)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(start.AuthorizationURL)
	query := parsed.Query()
	state := query.Get("state")
	if query.Get("claims") != "" || query.Get("max_age") != "" || query.Get("prompt") != "" {
		t.Fatal("ordinary login forced provider reauthentication")
	}
	if query.Get("code_challenge_method") != "S256" || query.Get("response_type") != "code" || query.Get("scope") != "openid" || strings.Contains(start.String(), state) {
		t.Fatal("unsafe login redirect")
	}
	if _, err = manager.Consume(state, start.TransactionCookie, CallbackPath("https://other.example"), ""); err == nil {
		t.Fatal("wrong callback accepted")
	}
	if _, err = manager.Consume(state, start.TransactionCookie, CallbackPath(trust.Issuer.URL), "https://other.example"); err == nil {
		t.Fatal("mix-up accepted")
	}
	wrong, _ := randomLoginValue()
	if _, err = manager.Consume(state, wrong, CallbackPath(trust.Issuer.URL), ""); err == nil {
		t.Fatal("state without host cookie accepted")
	}
	completion, err := manager.Consume(state, start.TransactionCookie, CallbackPath(trust.Issuer.URL), trust.Issuer.URL)
	if err != nil {
		t.Fatal(err)
	}
	challenge := sha256.Sum256([]byte(completion.Verifier()))
	if query.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(challenge[:]) || completion.Nonce() != query.Get("nonce") || completion.ReturnPath() != "/security/roles" || completion.RequiresRecentAuthentication() {
		t.Fatal("transaction not bound")
	}
	if _, err = manager.Consume(state, start.TransactionCookie, CallbackPath(trust.Issuer.URL), ""); err == nil {
		t.Fatal("replay accepted")
	}
	restarted, _, _ := loginTransactionFixture(t)
	if _, err = restarted.Consume(state, start.TransactionCookie, CallbackPath(trust.Issuer.URL), ""); err == nil {
		t.Fatal("restart resumed unfinished login")
	}
}
func TestLoginTransactionsConcurrentConsumeAndBounds(t *testing.T) {
	manager, trust, discovery := loginTransactionFixture(t)
	now := time.Now()
	manager.now = func() time.Time { return now }
	start, err := manager.Begin(trust, discovery, "/", "", true)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(start.AuthorizationURL)
	if parsed.Query().Get("max_age") != "0" || parsed.Query().Get("prompt") != "login" || parsed.Query().Get("claims") != `{"id_token":{"auth_time":{"essential":true}}}` {
		t.Fatal("step-up did not force auth")
	}
	var successes atomic.Int32
	var group sync.WaitGroup
	for range 32 {
		group.Go(func() {
			if _, err := manager.Consume(parsed.Query().Get("state"), start.TransactionCookie, CallbackPath(trust.Issuer.URL), ""); err == nil {
				successes.Add(1)
			}
		})
	}
	group.Wait()
	if successes.Load() != 1 {
		t.Fatal("state consumed more than once", successes.Load())
	}
	for range MaxLoginTransactions {
		if _, err := manager.Begin(trust, discovery, "/", "", false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = manager.Begin(trust, discovery, "/", "", false); err == nil {
		t.Fatal("unbounded pending logins")
	}
	now = now.Add(LoginTransactionLifetime)
	if _, err = manager.Begin(trust, discovery, "/", "", false); err != nil {
		t.Fatal("expired transactions not reclaimed", err)
	}
}
func TestLoginTransactionsRedirectAndExpiry(t *testing.T) {
	manager, trust, discovery := loginTransactionFixture(t)
	for _, path := range []string{"//evil.example", "https://evil.example", "/unknown", "/../security/roles", "/%2F%2Fevil.example", "/auth/callback", "/?return=https://evil.example"} {
		if _, err := manager.Begin(trust, discovery, path, "", false); err == nil {
			t.Fatal("open redirect", path)
		}
	}
	other := trust
	other.Issuer.RedirectURI = "https://admin.example/auth/callback"
	if _, err := manager.Begin(other, discovery, "/", "", false); err == nil {
		t.Fatal("shared callback accepted")
	}
	now := time.Now()
	manager.now = func() time.Time { return now }
	start, err := manager.Begin(trust, discovery, "/", "", false)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(start.AuthorizationURL)
	now = now.Add(LoginTransactionLifetime)
	if _, err = manager.Consume(parsed.Query().Get("state"), start.TransactionCookie, CallbackPath(trust.Issuer.URL), ""); err == nil {
		t.Fatal("expired transaction accepted")
	}
}

func TestLoginTransactionsPurposeIndependentOfReplacement(t *testing.T) {
	for _, stepUp := range []bool{false, true} {
		manager, trust, discovery := loginTransactionFixture(t)
		digest := strings.Repeat("a", 64)
		start, err := manager.Begin(trust, discovery, "/", digest, stepUp)
		if err != nil {
			t.Fatal(err)
		}
		parsed, _ := url.Parse(start.AuthorizationURL)
		completion, err := manager.Consume(parsed.Query().Get("state"), start.TransactionCookie, CallbackPath(trust.Issuer.URL), "")
		if err != nil || completion.RequiresRecentAuthentication() != stepUp || completion.ReplacesDigest() != digest {
			t.Fatal("replacement changed saved purpose", err)
		}
	}
}

func TestLoginTransactionsOperationPurposeCannotReplaceSession(t *testing.T) {
	manager, trust, discovery := loginTransactionFixture(t)
	id := [32]byte{8}
	start, err := manager.BeginAuthorization(trust, discovery, id)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(start.AuthorizationURL)
	completion, err := manager.Consume(parsed.Query().Get("state"), start.TransactionCookie, CallbackPath(trust.Issuer.URL), trust.Issuer.URL)
	got, purpose := completion.AuthorizationID()
	if err != nil || !purpose || got != id || !completion.RequiresRecentAuthentication() || completion.ReplacesDigest() != "" {
		t.Fatal("operation purpose became ordinary session replacement", err)
	}
	if parsed.Query().Get("max_age") != "0" || parsed.Query().Get("claims") != `{"id_token":{"auth_time":{"essential":true}}}` {
		t.Fatal("operation omitted signed fresh-event request")
	}
}
