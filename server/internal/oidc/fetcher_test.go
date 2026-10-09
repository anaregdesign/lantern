package oidc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestPublicAddress(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.0.1", "169.254.169.254", "100.100.100.200", "192.0.2.1", "198.18.0.1", "255.255.255.255", "0.0.0.0", "::", "::1", "::ffff:169.254.169.254", "fc00::1", "fe80::1", "fec0::1", "64:ff9b::a9fe:a9fe", "2001::1", "2002:a9fe:a9fe::", "2001:db8::1", "ff02::1"} {
		if publicAddress(netip.MustParseAddr(raw)) {
			t.Errorf("unsafe destination allowed: %s", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111", "::ffff:8.8.8.8"} {
		if !publicAddress(netip.MustParseAddr(raw)) {
			t.Errorf("public destination denied: %s", raw)
		}
	}
}

func TestFetcherDialPinsDNS(t *testing.T) {
	f, err := NewFetcher(FetcherOptions{})
	if err != nil {
		t.Fatal(err)
	}
	resolutions, dials := 0, 0
	f.resolve = func(context.Context, string) ([]netip.Addr, error) {
		resolutions++
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}
	f.dial = func(_ context.Context, _ string, address string) (net.Conn, error) {
		dials++
		if address != "8.8.8.8:443" {
			t.Fatal("dial did not pin the validated numeric address")
		}
		return nil, errors.New("offline")
	}
	_, _ = f.dialValidated(context.Background(), "tcp", "idp.example:443")
	if resolutions != 1 || dials != 1 {
		t.Fatalf("DNS/dial calls: %d/%d", resolutions, dials)
	}
	f.resolve = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("::ffff:127.0.0.1")}, nil
	}
	_, _ = f.dialValidated(context.Background(), "tcp", "idp.example:443")
	if dials != 1 {
		t.Fatal("mixed public/private DNS answers reached dial")
	}
}

func TestFetcherHTTPSAndBounds(t *testing.T) {
	p := newTestProvider(t)
	var out map[string]any
	if err := p.fetcher.GetJSON(context.Background(), p.server.URL+"/jwks", &out); err != nil {
		t.Fatal(err)
	}
	defaultFetcher, _ := NewFetcher(FetcherOptions{})
	t.Cleanup(defaultFetcher.CloseIdleConnections)
	if err := defaultFetcher.GetJSON(context.Background(), p.server.URL+"/jwks", &out); err == nil {
		t.Fatal("private endpoint accepted without operator exception")
	}
	for _, raw := range []string{"http://idp.example", "https://user:secret@idp.example", "https://idp.example/#fragment", "https://[fe80::1%25en0]/", "file:///tmp/secret"} {
		if err := p.fetcher.GetJSON(context.Background(), raw, &out); err == nil {
			t.Fatal("unsafe endpoint accepted")
		}
	}
	p.mu.Lock()
	p.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "/jwks", http.StatusFound)
		case "/compressed":
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write([]byte(`{}`))
		case "/duplicate":
			_, _ = w.Write([]byte(`{"keys":[],"keys":[]}`))
		default:
			_, _ = w.Write([]byte(strings.Repeat(" ", maxDocumentBytes+1)))
		}
	})
	p.mu.Unlock()
	for _, path := range []string{"/redirect", "/compressed", "/duplicate", "/oversize"} {
		if err := p.fetcher.GetJSON(context.Background(), p.server.URL+path, &out); err == nil {
			t.Errorf("unbounded/ambiguous response accepted: %s", path)
		}
	}
}

func TestFetcherQualifiedTLSBoundsOnReusedResponse(t *testing.T) {
	p := newTestProvider(t)
	roots := x509.NewCertPool()
	roots.AddCert(p.server.Certificate())
	now := p.now()
	low, high := now.Add(-time.Second), now.Add(time.Second)
	var fault error
	bounds := func() (time.Time, time.Time, error) { return low, high, fault }
	f, err := NewFetcher(FetcherOptions{Roots: roots, PrivateOrigins: map[string][]netip.Prefix{p.server.URL: {netip.MustParsePrefix("127.0.0.1/32")}}, TimeBounds: bounds})
	if err != nil {
		t.Fatal(err)
	}
	defer f.CloseIdleConnections()
	var out map[string]any
	if err := f.GetJSON(t.Context(), p.server.URL+"/jwks", &out); err != nil {
		t.Fatal(err)
	}
	// The next request uses the same TLS session, so a handshake-only check
	// would miss this equality boundary.
	high = p.server.Certificate().NotAfter
	if err := f.GetJSON(t.Context(), p.server.URL+"/jwks", &out); err == nil {
		t.Fatal("reused expired TLS connection")
	}
	high = now.Add(time.Second)
	low = p.server.Certificate().NotBefore.Add(-time.Nanosecond)
	if err := f.checkTLS(&tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{p.server.Certificate()}}}); err == nil {
		t.Fatal("optimistic NotBefore endpoint")
	}
	low = now
	fault = ErrFetch
	if _, err := f.do(mustFetcherRequest(t, p.server.URL+"/jwks")); err == nil {
		t.Fatal("source fault used cached TLS")
	}
}
func mustFetcherRequest(t *testing.T, endpoint string) *http.Request {
	t.Helper()
	r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
