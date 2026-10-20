package peerauth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

func TestWorkloadTransportRefusesUnapprovedDestinationsAndPublicCredentials(t *testing.T) {
	m, key, options, _ := membershipFixture(t)
	s, err := CreateStore(options, signFixture(t, m, key))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	client := NewHTTPClient(s, tls.Certificate{}, x509.NewCertPool())
	for _, url := range []string{"http://localhost:6381/test", "https://localhost:6382/test", "https://user@localhost:6381/test", "https://localhost:6381/test?q=x", "https://localhost:6381/test#x"} {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Do(request); err == nil {
			t.Fatal("unapproved URL accepted", url)
		}
	}
	for _, header := range []string{"Authorization", "Cookie", "Proxy-Authorization"} {
		request, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, m.Members[0].Origin+"/test", nil)
		request.Header.Set(header, "public credential")
		if _, err := client.Do(request); err == nil {
			t.Fatal("public credential forwarded", header)
		}
	}
}

// Only the client's controlled observations advance. This peer deliberately
// has no admission/expiry watcher, so server-side cancellation cannot hide a
// missing outbound guard. All traffic is local TLS; no time source is queried.
func TestWorkloadTransportWatchesRestrictedAdmissionDuringBlockedIO(t *testing.T) {
	for _, phase := range []string{"handshake native headers", "handshake absolute headers", "response native body"} {
		t.Run(phase, func(t *testing.T) {
			entered, headers, joined := make(chan struct{}), make(chan struct{}), make(chan struct{})
			expiry := 12 * time.Second
			if phase == "response native body" {
				expiry = 25 * time.Second
			}
			store, clock, client, origin := transportDeadlineFixture(t, expiry, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(joined)
				close(entered)
				if phase == "response native body" {
					select {
					case <-headers:
					case <-r.Context().Done():
						return
					}
					if _, err := w.Write([]byte("ready")); err != nil {
						return
					}
					if err := http.NewResponseController(w).Flush(); err != nil {
						return
					}
				}
				<-r.Context().Done()
			}))
			entry, err := store.admitOrigin(origin, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			type result struct {
				response *http.Response
				err      error
			}
			returned := make(chan result, 1)
			go func() {
				request, e := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/blocked", nil)
				if e != nil {
					returned <- result{err: e}
					return
				}
				response, e := client.Do(request)
				returned <- result{response, e}
			}()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("TLS request never reached peer")
			}
			if phase != "response native body" {
				if phase == "handshake native headers" {
					// Absolute certificate expiry is still future. Only the
					// handshake's frozen 11s native budget has expired.
					clock.set(12*time.Second, 11*time.Second, 11500*time.Millisecond)
				} else {
					// The native handshake budget is still live; only its
					// absolute certificate window has reached equality.
					clock.set(10500*time.Millisecond, 11*time.Second, 12*time.Second)
				}
				if err = entry.Check(t.Context()); err != nil {
					t.Fatal("entry's original 30s bound expired", err)
				}
				select {
				case got := <-returned:
					if got.response != nil {
						_ = got.response.Body.Close()
					}
					if got.err == nil {
						t.Fatal("blocked headers returned success after peer admission expiry")
					}
				case <-time.After(2 * time.Second):
					t.Fatal("short handshake expiry did not cancel/join blocked headers before old local timer")
				}
			} else {
				clock.set(20*time.Second, 20500*time.Millisecond, 22*time.Second)
				close(headers)
				var response *http.Response
				select {
				case got := <-returned:
					if got.err != nil {
						t.Fatal(got.err)
					}
					response = got.response
				case <-time.After(3 * time.Second):
					t.Fatal("response headers were not received")
				}
				t.Cleanup(func() { _ = response.Body.Close() })
				prefix := make([]byte, 5)
				if _, err = io.ReadFull(response.Body, prefix); err != nil || string(prefix) != "ready" {
					t.Fatal("response prefix", string(prefix), err)
				}
				admission, ok := ResponseAdmission(response)
				if !ok || admission.budget.count != 3 {
					t.Fatal("response did not retain all three limits")
				}
				body, ok := response.Body.(*workloadBody)
				if !ok {
					t.Fatal("missing workload body")
				}
				reading := make(chan struct{})
				body.ReadCloser = transportReadProbe{body.ReadCloser, reading}
				readDone := make(chan error, 1)
				go func() { _, e := response.Body.Read(make([]byte, 1)); readDone <- e }()
				select {
				case <-reading:
				case <-time.After(time.Second):
					t.Fatal("body did not enter underlying blocked read")
				}
				clock.set(23500*time.Millisecond, 23500*time.Millisecond, 24500*time.Millisecond)
				if err = entry.Check(t.Context()); err != nil {
					t.Fatal("entry budget expired", err)
				}
				current := clock.observe()
				for _, prior := range admission.budget.limits[1:admission.budget.count] {
					elapsed, e := current.ElapsedSince(prior.original)
					if e != nil || elapsed >= prior.lifetime {
						t.Fatal("a prior handshake/entry limit also expired", elapsed, prior.lifetime, e)
					}
				}
				if !current.high.Before(admission.expiresAt) {
					t.Fatal("absolute expiry also elapsed")
				}
				select {
				case e := <-readDone:
					if e == nil {
						t.Fatal("expired response body returned success")
					}
				case <-time.After(2 * time.Second):
					t.Fatal("response-only native limit did not cancel/join blocked body before old local timer")
				}
			}
			select {
			case <-joined:
			case <-time.After(2 * time.Second):
				t.Fatal("peer handler did not observe client cancellation")
			}
		})
	}
}

type transportReadProbe struct {
	io.ReadCloser
	entered chan struct{}
}

func (r transportReadProbe) Read(p []byte) (int, error) {
	close(r.entered)
	return r.ReadCloser.Read(p)
}

type transportDeadlineClock struct {
	mu     sync.Mutex
	sample peerTestObservation
	base   time.Time
}

func (c *transportDeadlineClock) observe() peerTestObservation {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sample
}
func (c *transportDeadlineClock) set(elapsed, low, high time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sample.elapsed = elapsed
	c.sample.low = c.base.Add(low)
	c.sample.high = c.base.Add(high)
}

func transportDeadlineFixture(t *testing.T, expiry time.Duration, handler http.Handler) (*Store, *transportDeadlineClock, *http.Client, string) {
	t.Helper()
	base := time.Now().UTC().Truncate(time.Second)
	clock := &transportDeadlineClock{base: base, sample: peerTestObservation{new(byte), base.Add(-time.Second), base.Add(time.Second), 0}}
	caPublic, caKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: base.Add(-time.Hour), NotAfter: base.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, caPublic, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err = x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	uri, err := url.Parse("spiffe://lantern.test/node-a")
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: base.Add(-time.Hour), NotAfter: base.Add(expiry + ClockMargin), URIs: []*url.URL{uri}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, public, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err = x509.ParseCertificate(leafDER)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER}, PrivateKey: key}}}
	server.StartTLS()
	t.Cleanup(server.Close)
	m, operator, options, _ := membershipFixture(t)
	m.IssuedAt, m.ExpiresAt = base.Add(-time.Minute), base.Add(5*time.Minute)
	m.Domain.TrustDigest = sha256.Sum256(caDER)
	m.Members[0].SPKI = sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	m.Members[0].Origin = server.URL
	self := Member{ID: [16]byte{5}, Identity: "spiffe://lantern.test/client", SPKI: [32]byte{7}, Origin: "https://localhost:1"}
	m.Members = append([]Member{self}, m.Members...)
	options.Domain, options.Self = m.Domain, self
	options.Time = func() (TimeObservation, error) { return clock.observe(), nil }
	options.Now = func() time.Time { return clock.observe().high }
	store, err := CreateStore(options, signFixture(t, m, operator))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if e := store.Close(); e != nil {
			t.Error(e)
		}
	})
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	return store, clock, NewHTTPClient(store, tls.Certificate{}, roots), server.URL
}
