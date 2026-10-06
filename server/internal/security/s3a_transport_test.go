package security

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

func TestS3ATransportDirectSenderAndBounds(t *testing.T) {
	n := s3aTestCluster(t, nil)
	n.startAll()
	ctx := s3aTestContext(t)
	message, err := s2cSignPrepare(n.f.trust, n.f.keys[1], 1, 1, n.f.genesis.state.prefix, s2cBallot{Counter: 1, Member: 1})
	if err != nil {
		t.Fatal(err)
	}
	before, err := n.nodes[3].Floors()
	if err != nil {
		t.Fatal(err)
	}
	if err := n.nodes[2].send(ctx, 3, []byte(message.raw)); err == nil {
		t.Fatal("TLS-valid wrong direct sender accepted")
	}
	after, _ := n.nodes[3].Floors()
	if before != after {
		t.Fatal("wrong sender mutated kernel")
	}
	for _, raw := range [][]byte{nil, []byte("partial"), append([]byte(message.raw), 0)} {
		if n.nodes[1].send(ctx, 3, raw) == nil {
			t.Fatal("malformed frame")
		}
	}
	oversized := make([]byte, n.nodes[1].frameBytes()+1)
	if n.nodes[1].send(ctx, 3, oversized) == nil {
		t.Fatal("oversized native request")
	}
	for _, body := range [][]byte{nil, make([]byte, 15), make([]byte, 17), make([]byte, 16), append(binary.BigEndian.AppendUint64(nil, 1), binary.BigEndian.AppendUint64(nil, 999)...)} {
		_, status, err := n.nodes[1].request(ctx, 3, s3aRangePath, body, 1024)
		if err == nil && status == http.StatusOK {
			t.Fatal("invalid range")
		}
	}
	if _, err := s3aReadBody(strings.NewReader("12345"), 4); err == nil {
		t.Fatal("oversized body")
	}
	if _, err := n.nodes[1].decodeRange([]byte(s3aRangeMagic+"\xff\xff\xff\xff"), 1); err == nil {
		t.Fatal("oversized range count")
	}
	if err := n.nodes[1].send(ctx, 3, []byte(message.raw)); err != nil {
		t.Fatal("valid direct prepare", err)
	}
}

func TestS3ATransportPinnedPrivateWireContract(t *testing.T) {
	if s3aMessagePath != "/lantern-private/control/v1/message" || s3aRangePath != "/lantern-private/control/v1/chosen-range" || s3aRangeMagic != "LNS3R001" || s3aMediaType != "application/octet-stream" {
		t.Fatal("unversioned private wire change")
	}
}

func TestS3ATransportPlaintextPublicHeadersAndProxy(t *testing.T) {
	n := s3aTestCluster(t, nil)
	n.startAll()
	ctx := s3aTestContext(t)
	o := n.nodes[1]
	target := n.configs[2].Membership.Self.Origin + s3aMessagePath
	for _, header := range []string{"Authorization", "Cookie", "Proxy-Authorization", peerauth.DomainHeader} {
		r, _ := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader([]byte("bad")))
		r.Header.Set("Content-Type", s3aMediaType)
		r.Header.Set(header, "forbidden")
		if response, err := o.client.Do(r); err == nil {
			_ = response.Body.Close()
			t.Fatal("outgoing public/foreign header", header)
		}
	}
	plain, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.Replace(target, "https:", "http:", 1), nil)
	if response, err := o.client.Do(plain); err == nil {
		_ = response.Body.Close()
		t.Fatal("plaintext client")
	}
	if err := o.client.CheckRedirect(nil, nil); err == nil {
		t.Fatal("redirect permitted")
	}
	// An environment proxy must never receive workload credentials/traffic.
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "")
	message, err := s2cSignPrepare(n.f.trust, n.f.keys[1], 1, 1, n.f.genesis.state.prefix, s2cBallot{Counter: 1, Member: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := o.send(ctx, 2, []byte(message.raw)); err != nil {
		t.Fatal("fixed-origin transport consulted environment proxy", err)
	}
	// A raw valid mTLS client cannot bypass handler checks with public headers.
	client := &http.Client{Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: o.identity.roots, Certificates: []tls.Certificate{o.identity.certificate}}}}
	defer client.CloseIdleConnections()
	for _, header := range []string{"Authorization", "Cookie", "Proxy-Authorization", peerauth.DomainHeader} {
		r, _ := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader([]byte(message.raw)))
		r.Header.Set("Content-Type", s3aMediaType)
		r.Header.Set(peerauth.ControlHeader, hex.EncodeToString(o.binding[:]))
		r.Header.Set(header, "forbidden")
		response, err := client.Do(r)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusNoContent {
				t.Fatal("incoming public header", header)
			}
		}
	}
}

func TestS3ATransportBlockedRPCExpiryWithdrawalAndCancel(t *testing.T) {
	for _, cut := range []string{"expiry", "withdrawal", "cancel"} {
		t.Run(cut, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			n := s3aTestCluster(t, func(id uint32, c *s3aConfig) {
				if id == 2 {
					c.hooks = &s3aHooks{beforeResponse: func(ctx context.Context) {
						once.Do(func() { close(entered) })
						select {
						case <-ctx.Done():
						case <-release:
						}
					}}
				}
			})
			n.startAll()
			ctx, cancel := context.WithCancel(s3aTestContext(t))
			defer cancel()
			before, err := n.nodes[2].Floors()
			if err != nil {
				t.Fatal(err)
			}
			m, err := s2cSignPrepare(n.f.trust, n.f.keys[1], 1, 1, n.f.genesis.state.prefix, s2cBallot{Counter: 1, Member: 1})
			if err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() { result <- n.nodes[1].send(ctx, 2, []byte(m.raw)) }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("RPC did not reach durable response cut")
			}
			retained, err := n.nodes[2].Floors()
			if err != nil || retained.P.Index <= before.P.Index || retained.B != before.B {
				t.Fatal("durable promise missing before blocked ACK", retained, err)
			}
			switch cut {
			case "expiry":
				n.clock.Store(n.manifest.ExpiresAt.Add(-peerauth.ClockMargin).UnixNano())
			case "withdrawal":
				next := n.manifest
				next.Version++
				next.Profile.TrustDigest[0]++
				if n.nodes[2].Refresh(n.sign(next)) == nil {
					t.Fatal("withdrawal not fenced")
				}
			case "cancel":
				cancel()
			}
			close(release)
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("ineligible transport response escaped")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("blocked RPC did not cancel")
			}
			_ = n.nodes[2].Close()
			if cut == "cancel" {
				owner, err := resumeS3AOwner(n.configs[2], retained)
				if err != nil {
					t.Fatal("cancellation undid durable promise", err)
				}
				_ = owner.Close()
			}
		})
	}
}

func TestS3ATransportRealCertificateRejectionsAndForeignReplay(t *testing.T) {
	n := s3aTestCluster(t, nil)
	n.startAll()
	ctx := s3aTestContext(t)
	o := n.nodes[1]
	before, err := n.nodes[2].Floors()
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"unknown-SAN", "wrong-SPKI", "wrong-root", "TLS-1.2"} {
		t.Run(kind, func(t *testing.T) {
			certificate := o.identity.certificate
			roots := o.identity.roots
			version := uint16(tls.VersionTLS13)
			switch kind {
			case "unknown-SAN":
				certificate = n.certificate("spiffe://s3a.test/foreign")
			case "wrong-SPKI":
				certificate = n.certificate(o.identity.self.Identity)
			case "wrong-root":
				roots = x509.NewCertPool()
			case "TLS-1.2":
				version = tls.VersionTLS12
			}
			client := &http.Client{Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: version, MaxVersion: version, RootCAs: roots, Certificates: []tls.Certificate{certificate}}}}
			defer client.CloseIdleConnections()
			r, _ := http.NewRequestWithContext(ctx, http.MethodPost, n.configs[2].Membership.Self.Origin+s3aMessagePath, strings.NewReader("untrusted"))
			r.Header.Set("Content-Type", s3aMediaType)
			r.Header.Set(peerauth.ControlHeader, hex.EncodeToString(o.binding[:]))
			if response, err := client.Do(r); err == nil {
				_ = response.Body.Close()
				t.Fatal("unqualified certificate reached HTTP", kind)
			}
		})
	}
	response, err := http.Post(strings.Replace(n.configs[2].Membership.Self.Origin, "https:", "http:", 1)+s3aMessagePath, s3aMediaType, strings.NewReader("plaintext"))
	if err == nil {
		_ = response.Body.Close()
		if response.StatusCode < 400 {
			t.Fatal("plaintext listener admitted")
		}
	}
	origins := append([]s2cOriginDescriptor(nil), n.f.origins...)
	origins[0].Incarnation[0]++
	foreign, err := newS2CTrust(s2cBootstrap{n.f.genesis, n.f.members, origins, n.f.trust.bounds})
	if err != nil {
		t.Fatal(err)
	}
	message, err := s2cSignPrepare(foreign, n.f.keys[1], 1, 1, n.f.genesis.state.prefix, s2cBallot{Counter: 1, Member: 1})
	if err != nil {
		t.Fatal(err)
	}
	if o.send(ctx, 2, []byte(message.raw)) == nil {
		t.Fatal("foreign protocol replay")
	}
	after, err := n.nodes[2].Floors()
	if err != nil || after != before {
		t.Fatal("rejected TLS/scope mutated kernel", err)
	}
}

func TestS3ATransportResponseKeepsHandshakeDeadlineAcrossRefresh(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	n := s3aTestCluster(t, func(id uint32, c *s3aConfig) {
		if id == 2 {
			c.hooks = &s3aHooks{beforeResponse: func(ctx context.Context) {
				once.Do(func() { close(entered) })
				select {
				case <-release:
				case <-ctx.Done():
				}
			}}
		}
	})
	n.manifest.ExpiresAt = time.Unix(0, n.clock.Load()).Add(20 * time.Second)
	for id, c := range n.configs {
		c.Manifest = n.sign(n.manifest)
		n.configs[id] = c
	}
	n.start(1)
	n.start(2)
	originalExpiry := n.manifest.ExpiresAt.Add(-peerauth.ClockMargin)
	type result struct {
		admission *peerauth.Admission
		err       error
	}
	done := make(chan result, 1)
	go func() {
		body := binary.BigEndian.AppendUint64(nil, 1)
		body = binary.BigEndian.AppendUint64(body, 1)
		_, _, a, err := n.nodes[1].requestAdmitted(t.Context(), 2, s3aRangePath, body, 1024)
		done <- result{a, err}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("response not reached")
	}
	n.clock.Add(int64(time.Second))
	refresh := n.manifest
	refresh.Version++
	refresh.ExpiresAt = refresh.ExpiresAt.Add(time.Minute)
	for _, o := range n.nodes {
		if err := o.Refresh(n.sign(refresh)); err != nil {
			t.Fatal(err)
		}
	}
	close(release)
	r := <-done
	if r.err != nil || r.admission == nil || !r.admission.ExpiresAt().Equal(originalExpiry) {
		t.Fatal("response minted a renewed admission", r.admission, r.err)
	}
	n.clock.Store(originalExpiry.UnixNano())
	if r.admission.Check(t.Context()) == nil {
		t.Fatal("original exact expiry survived refresh")
	}
}

func TestS3ATransportLostACKRegeneratesExactDurablePromise(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	promises := make(chan []byte, 16)
	n := s3aTestCluster(t, func(id uint32, c *s3aConfig) {
		if id == 2 {
			c.hooks = &s3aHooks{
				beforeSend: func(_ context.Context, to uint32, raw []byte) error {
					if to == 1 {
						select {
						case promises <- bytes.Clone(raw):
						default:
						}
					}
					return nil
				},
				beforeResponse: func(ctx context.Context) {
					once.Do(func() {
						close(entered)
						select {
						case <-release:
						case <-ctx.Done():
						}
					})
				},
			}
		}
	})
	n.start(1)
	n.start(2)
	m, err := s2cSignPrepare(n.f.trust, n.f.keys[1], 1, 1, n.f.genesis.state.prefix, s2cBallot{Counter: 1, Member: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- n.nodes[1].send(ctx, 2, []byte(m.raw)) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("durable ACK not reached")
	}
	before, err := n.nodes[2].Floors()
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("lost ACK reported success")
	}
	close(release)
	if err := n.nodes[1].send(t.Context(), 2, []byte(m.raw)); err != nil {
		t.Fatal(err)
	}
	after, err := n.nodes[2].Floors()
	if err != nil || before != after {
		t.Fatal("delivery retry appended durable state", err)
	}
	var first []byte
	for i := 0; i < 2; i++ {
		select {
		case raw := <-promises:
			decoded, err := s2cDecodeMessage(n.f.trust, raw)
			if err != nil || decoded.kind != s2cPromise {
				t.Fatal("not a signed promise", err)
			}
			if i == 0 {
				first = raw
			} else if !bytes.Equal(first, raw) {
				t.Fatal("retry changed retained promise bytes")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("promise not regenerated")
		}
	}
}

func TestS3ATransportIncomingContextRetainsConfiguredRPCLimit(t *testing.T) {
	remaining := make(chan time.Duration, 1)
	n := s3aTestCluster(t, func(id uint32, c *s3aConfig) {
		if id == 2 {
			c.hooks = &s3aHooks{beforeResponse: func(ctx context.Context) {
				deadline, ok := ctx.Deadline()
				if !ok {
					remaining <- time.Hour
					return
				}
				remaining <- time.Until(deadline)
			}}
		}
	})
	n.start(1)
	n.start(2)
	m, err := s2cSignPrepare(n.f.trust, n.f.keys[1], 1, 1, n.f.genesis.state.prefix, s2cBallot{Counter: 1, Member: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := n.nodes[1].send(t.Context(), 2, []byte(m.raw)); err != nil {
		t.Fatal(err)
	}
	if got := <-remaining; got <= 0 || got > n.configs[2].Limits.RPCTimeout {
		t.Fatal("workload wrapper extended configured RPC lifetime", got)
	}
}
