package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type metricsRoundTrip func(*http.Request) (*http.Response, error)

func (f metricsRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestMetricsRelayOnlyFixedScrape(t *testing.T) {
	h := newMetricsRelay()
	calls := 0
	h.client.Transport = metricsRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != metricsEndpoint || r.Method != http.MethodGet || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("X-Forwarded-For") != "" {
			t.Fatal("request escaped the fixed metrics boundary")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Set-Cookie": {"private"}}, Body: io.NopCloser(strings.NewReader("metric 1\n")), ContentLength: 9}, nil
	})
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("Authorization", "private-token")
	request.Header.Set("Cookie", "private-session")
	request.Header.Set("X-Forwarded-For", "untrusted")
	out := httptest.NewRecorder()
	h.ServeHTTP(out, request)
	if out.Code != http.StatusOK || out.Body.String() != "metric 1\n" || out.Header().Get("Set-Cookie") != "" || calls != 1 {
		t.Fatal("fixed metrics response or header isolation failed")
	}
	for _, test := range []struct{ method, path string }{
		{http.MethodPost, "/metrics"}, {http.MethodGet, "/"},
		{http.MethodGet, "/healthz"}, {http.MethodGet, "/metrics?"},
		{http.MethodGet, "/metrics?target=other"}, {http.MethodGet, "/%6detrics"},
		{http.MethodGet, "http://other.example/metrics"},
	} {
		out := httptest.NewRecorder()
		h.ServeHTTP(out, httptest.NewRequest(test.method, test.path, nil))
		if out.Code == http.StatusOK || calls != 1 {
			t.Fatal("noncanonical scrape reached the upstream", test)
		}
	}
}

func TestMetricsRelayBoundsAndFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		length int64
		body   string
		encode string
		err    error
	}{
		{"redirect", http.StatusFound, 0, "", "", nil},
		{"failure", http.StatusInternalServerError, 0, "", "", nil},
		{"length overflow", http.StatusOK, 11, "12345678901", "", nil},
		{"stream overflow", http.StatusOK, -1, "12345678901", "", nil},
		{"encoding", http.StatusOK, 0, "", "gzip", nil},
		{"transport", 0, 0, "", "", errors.New("private upstream details")},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newMetricsRelay()
			h.maxBytes = 10
			calls := 0
			h.client.Transport = metricsRoundTrip(func(*http.Request) (*http.Response, error) {
				calls++
				if test.err != nil {
					return nil, test.err
				}
				return &http.Response{StatusCode: test.status, Header: http.Header{"Location": {"http://other.example"}, "Content-Encoding": {test.encode}}, Body: io.NopCloser(strings.NewReader(test.body)), ContentLength: test.length}, nil
			})
			out := httptest.NewRecorder()
			h.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/metrics", nil))
			if out.Code != http.StatusBadGateway || calls != 1 || strings.Contains(out.Body.String(), "private") || out.Header().Get("Location") != "" {
				t.Fatal("failure/overflow/redirect escaped or disclosed details")
			}
		})
	}
	h := newMetricsRelay()
	h.inFlight <- struct{}{}
	out := httptest.NewRecorder()
	h.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if out.Code != http.StatusServiceUnavailable {
		t.Fatal("scrape buffer admission was unbounded")
	}
}

func metricsTLSFixture(t *testing.T, usage x509.ExtKeyUsage) metricsRelayOptions {
	t.Helper()
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic metrics CA"}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
	rootDER, err := x509.CreateCertificate(rand.Reader, root, root, public, key)
	if err != nil {
		t.Fatal(err)
	}
	leafPublic, leafKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{"synthetic-metrics"}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, root, leafPublic, key)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		t.Fatal(err)
	}
	rootKeyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	options := metricsRelayOptions{listen: ":9443", certificate: filepath.Join(dir, "server.pem"), key: filepath.Join(dir, "server-key.pem"), clientCA: filepath.Join(dir, "metrics-ca.pem")}
	for path, raw := range map[string][]byte{
		options.certificate: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		options.key:         pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}),
		options.clientCA:    pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER}),
		filepath.Join(dir, "synthetic-ca-key.pem"): pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: rootKeyDER}),
	} {
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return options
}

func metricsTLSClientFixture(t *testing.T, options metricsRelayOptions) tls.Certificate {
	t.Helper()
	caPEM, err := os.ReadFile(options.clientCA)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(caPEM)
	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM, err := os.ReadFile(filepath.Join(filepath.Dir(options.clientCA), "synthetic-ca-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	block, _ = pem.Decode(keyPEM)
	caKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: "synthetic Prometheus client"}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter}
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca, public, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func TestMetricsRelayMutualTLSHandshake(t *testing.T) {
	options := metricsTLSFixture(t, x509.ExtKeyUsageServerAuth)
	serverConfig, err := metricsTLSConfig(options)
	if err != nil {
		t.Fatal(err)
	}
	clientCertificate := metricsTLSClientFixture(t, options)
	other := metricsTLSFixture(t, x509.ExtKeyUsageServerAuth)
	unknownClient := metricsTLSClientFixture(t, other)
	for _, test := range []struct {
		name string
		cert *tls.Certificate
		max  uint16
		want bool
	}{
		{"trusted client", &clientCertificate, tls.VersionTLS13, true},
		{"no client certificate", nil, tls.VersionTLS13, false},
		{"untrusted client", &unknownClient, tls.VersionTLS13, false},
		{"server leaf as client", &serverConfig.Certificates[0], tls.VersionTLS13, false},
		{"old TLS", &clientCertificate, tls.VersionTLS12, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			serverRaw, clientRaw := net.Pipe()
			defer func() { _ = serverRaw.Close() }()
			defer func() { _ = clientRaw.Close() }()
			server := tls.Server(serverRaw, serverConfig)
			config := &tls.Config{RootCAs: serverConfig.ClientCAs, ServerName: "synthetic-metrics", MinVersion: tls.VersionTLS12, MaxVersion: test.max}
			if test.cert != nil {
				config.Certificates = []tls.Certificate{*test.cert}
			}
			client := tls.Client(clientRaw, config)
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- server.HandshakeContext(ctx) }()
			clientErr := client.HandshakeContext(ctx)
			if !test.want && clientErr == nil {
				// TLS 1.3 clients can finish before the server rejects their
				// certificate. Drain that alert rather than letting net.Pipe's
				// synchronous write turn a real refusal into a timeout result.
				if err := clientRaw.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
					t.Fatal(err)
				}
				_, _ = client.Read(make([]byte, 1))
			}
			serverErr := <-done
			if errors.Is(serverErr, context.DeadlineExceeded) {
				t.Fatal("TLS test timed out instead of observing certificate admission")
			}
			if test.want && (serverErr != nil || clientErr != nil) || !test.want && serverErr == nil {
				t.Fatalf("mutual TLS admission: server=%v client=%v", serverErr, clientErr)
			}
		})
	}
}

func TestMetricsRelayRequiresIndependentMutualTLS(t *testing.T) {
	options := metricsTLSFixture(t, x509.ExtKeyUsageServerAuth)
	config, err := metricsTLSConfig(options)
	if err != nil || config.MinVersion != tls.VersionTLS13 || config.ClientAuth != tls.RequireAndVerifyClientCert || config.ClientCAs == nil || !config.SessionTicketsDisabled {
		t.Fatal("metrics TLS admitted missing client authentication", err)
	}
	if err := os.Chmod(options.key, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := metricsTLSConfig(options); err == nil {
		t.Fatal("publicly readable private key accepted")
	}
	options = metricsTLSFixture(t, x509.ExtKeyUsageClientAuth)
	if _, err := metricsTLSConfig(options); err == nil {
		t.Fatal("client credential accepted as relay server identity")
	}
	options = metricsTLSFixture(t, x509.ExtKeyUsageServerAuth)
	if err := os.WriteFile(options.clientCA, []byte("private-invalid-ca"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := metricsTLSConfig(options); err == nil || strings.Contains(err.Error(), "private-invalid-ca") {
		t.Fatal("missing trust or private diagnostics accepted", err)
	}
}

func TestMetricsRelayFlagsAndHelp(t *testing.T) {
	for _, args := range [][]string{nil, {"--listen=:9443"}, {"--upstream=private-unsupported"}, {"--certificate=relative", "--key=/key", "--client-ca=/ca"}} {
		if _, _, err := parseMetricsRelayOptions(args); err == nil || strings.Contains(err.Error(), "private-unsupported") {
			t.Fatal("missing/ambiguous TLS input or disclosed flag data", err)
		}
	}
	for _, listen := range []string{":", ":0", ":65536", ":09443", ":not-a-port"} {
		if _, _, err := parseMetricsRelayOptions([]string{"--listen=" + listen, "--certificate=/cert", "--key=/key", "--client-ca=/ca"}); err == nil {
			t.Fatal("noncanonical metrics listener accepted")
		}
	}
	var out strings.Builder
	if err := runMetricsRelay(context.Background(), []string{"--help"}, &out); err != nil || !strings.Contains(out.String(), "Mandatory TLS 1.3 mutual authentication") {
		t.Fatal("help attempted serving or omitted trust boundary", err)
	}
}
