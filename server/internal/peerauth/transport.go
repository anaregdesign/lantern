package peerauth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"io"
	"net/http"
	"sync"
	"time"
)

// NewHTTPClient constructs the private peer client. Each bounded peer RPC uses
// a fresh TLS 1.3 handshake with an origin-specific membership check. Streams
// may last up to MaxRPCLifetime; no data RPC uses this client. Avoiding pooled
// peer connections keeps certificate/identity changes from surviving a request.
func NewHTTPClient(store *Store, certificate tls.Certificate, roots *x509.CertPool) *http.Client {
	return &http.Client{Transport: &workloadTransport{store: store, certificate: certificate, roots: roots},
		CheckRedirect: func(*http.Request, []*http.Request) error { return ErrMembership }}
}

type workloadTransport struct {
	store       *Store
	certificate tls.Certificate
	roots       *x509.CertPool
}

func (t *workloadTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || request.URL == nil || request.URL.User != nil || request.URL.Opaque != "" ||
		request.URL.RawQuery != "" || request.URL.ForceQuery || request.URL.Fragment != "" ||
		request.Host != "" && request.Host != request.URL.Host || t.roots == nil ||
		len(request.Header.Values("Authorization")) != 0 || len(request.Header.Values("Cookie")) != 0 || len(request.Header.Values("Proxy-Authorization")) != 0 {
		return nil, ErrMembership
	}
	origin := request.URL.Scheme + "://" + request.URL.Host
	if !validOrigin(origin) {
		return nil, ErrMembership
	}
	member, expiry, err := t.store.MemberAtOrigin(origin)
	if err != nil || member.Identity == t.store.options.Self.Identity {
		return nil, ErrMembership
	}
	now := t.store.options.Now()
	remaining := min(expiry.Sub(now), MaxRPCLifetime)
	if remaining <= 0 {
		return nil, ErrMembership
	}
	ctx, cancel := context.WithTimeout(request.Context(), remaining)
	stop, stopped := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var timerMu sync.Mutex
	var certificateTimer *time.Timer
	var handshakeAdmission *Admission
	cleanup := func() {
		once.Do(func() {
			close(stop)
			cancel()
			timerMu.Lock()
			if certificateTimer != nil {
				certificateTimer.Stop()
			}
			timerMu.Unlock()
			<-stopped
		})
	}
	check := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, _, err := t.store.MemberAtOrigin(origin)
		if err != nil || current != member {
			return ErrMembership
		}
		return nil
	}
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(membershipCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if check() != nil {
					cancel()
					return
				}
			}
		}
	}()
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: t.roots.Clone(),
			Certificates: []tls.Certificate{t.certificate}, NextProtos: []string{"http/1.1"},
			VerifyConnection: func(state tls.ConnectionState) error {
				admission, err := t.store.Admit(&state, origin)
				if err != nil || admission.member != member {
					return ErrMembership
				}
				// Bound writes and blocked response headers at the certificate
				// expiry immediately, before sending any authenticated request.
				timerMu.Lock()
				handshakeAdmission = admission
				if certificateTimer != nil {
					certificateTimer.Stop()
				}
				if ctx.Err() == nil {
					certificateTimer = time.AfterFunc(admission.ExpiresAt().Sub(t.store.options.Now()), cancel)
				}
				timerMu.Unlock()
				return check()
			}}}
	if t.store.controlBinding != nil {
		transport.MaxResponseHeaderBytes = 64 << 10
	}
	if t.store.timeBounds != nil {
		transport.TLSClientConfig.Time = t.store.options.Now
	}
	request = request.Clone(ctx)
	request.Header = request.Header.Clone()
	header, digest := t.store.wireProfile()
	foreignHeader := ControlHeader
	if header == ControlHeader {
		foreignHeader = DomainHeader
	}
	if len(request.Header.Values(foreignHeader)) != 0 {
		cleanup()
		return nil, ErrMembership
	}
	request.Header.Set(header, hex.EncodeToString(digest[:]))
	if err := check(); err != nil {
		cleanup()
		return nil, err
	}
	response, err := transport.RoundTrip(request)
	if err != nil {
		cleanup()
		return nil, err
	}
	timerMu.Lock()
	admission := handshakeAdmission
	timerMu.Unlock()
	if t.store.controlBinding == nil {
		// Preserve the legacy graph profile's response admission behavior.
		admission, err = t.store.Admit(response.TLS, origin)
	}
	if err != nil || admission == nil || admission.member != member || admission.Check(ctx) != nil || check() != nil {
		_ = response.Body.Close()
		cleanup()
		return nil, ErrMembership
	}
	response.Body = &workloadBody{ReadCloser: response.Body, admission: admission, check: func() error {
		if err := check(); err != nil {
			return err
		}
		return admission.Check(ctx)
	}, cleanup: cleanup}
	return response, nil
}

type workloadBody struct {
	io.ReadCloser
	admission *Admission
	check     func() error
	cleanup   func()
}

// ResponseAdmission preserves the original verified workload/deadline while a
// bounded caller processes already-read response bytes. Membership refresh must
// not silently mint a longer admission between reading and kernel application.
func ResponseAdmission(response *http.Response) (*Admission, bool) {
	if response == nil {
		return nil, false
	}
	body, ok := response.Body.(*workloadBody)
	if !ok || body.admission == nil {
		return nil, false
	}
	return body.admission, true
}

func (b *workloadBody) Read(data []byte) (int, error) {
	if err := b.check(); err != nil {
		_ = b.Close()
		return 0, err
	}
	n, err := b.ReadCloser.Read(data)
	if checkErr := b.check(); checkErr != nil {
		_ = b.Close()
		return 0, checkErr
	}
	if err != nil {
		b.cleanup()
	}
	return n, err
}

func (b *workloadBody) Close() error {
	b.cleanup()
	return b.ReadCloser.Close()
}
