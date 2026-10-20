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
	started := time.Now()
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
	original, err := t.store.admitOrigin(origin, started)
	if err != nil {
		return nil, err
	}
	member := original.member
	deadline, err := original.deadline(request.Context())
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithDeadline(request.Context(), deadline)
	stop, stopped := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var admissionMu sync.Mutex
	var interruptionTimer *time.Timer
	activeAdmission := original
	interruptionDeadline := deadline
	closing := false
	cleanup := func() {
		once.Do(func() {
			close(stop)
			cancel()
			admissionMu.Lock()
			closing = true
			if interruptionTimer != nil {
				interruptionTimer.Stop()
			}
			admissionMu.Unlock()
			<-stopped
		})
	}
	check := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		admissionMu.Lock()
		current := activeAdmission
		admissionMu.Unlock()
		// Never call Store while holding the admission/timer publication lock.
		if current.Check(ctx) != nil {
			return ErrMembership
		}
		return nil
	}
	// Publish only an immutable intersection. Each later stage may shorten the
	// local interruption timer, while the watcher checks all native/UTC limits.
	activate := func(admission *Admission) error {
		nextDeadline, err := admission.deadline(ctx)
		if err != nil {
			return err
		}
		admissionMu.Lock()
		if closing {
			admissionMu.Unlock()
			return ErrMembership
		}
		activeAdmission = admission
		if nextDeadline.Before(interruptionDeadline) {
			interruptionDeadline = nextDeadline
			if interruptionTimer != nil {
				interruptionTimer.Stop()
			}
			interruptionTimer = time.AfterFunc(time.Until(interruptionDeadline), cancel)
		}
		admissionMu.Unlock()
		return check()
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
				if err := admission.restrict(original); err != nil {
					return err
				}
				return activate(admission)
			}}}
	if t.store.controlBinding != nil {
		transport.MaxResponseHeaderBytes = 64 << 10
	}
	if t.store.timeBounds != nil || t.store.options.Time != nil {
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
	admissionMu.Lock()
	admission := activeAdmission
	admissionMu.Unlock()
	if admission == nil || admission == original {
		_ = response.Body.Close()
		cleanup()
		return nil, ErrMembership
	}
	if t.store.controlBinding == nil {
		// Revalidate the data profile without renewing the original capability.
		prior := admission
		admission, err = t.store.Admit(response.TLS, origin)
		if err == nil {
			err = admission.restrict(prior)
		}
		if err == nil {
			err = activate(admission)
		}
	}
	if err != nil || admission == nil || admission.member != member || check() != nil {
		_ = response.Body.Close()
		cleanup()
		return nil, ErrMembership
	}
	response.Body = &workloadBody{ReadCloser: response.Body, admission: admission, check: check, cleanup: cleanup}
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
