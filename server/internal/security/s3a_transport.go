package security

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

const (
	s3aMessagePath = "/lantern-private/control/v1/message"
	s3aRangePath   = "/lantern-private/control/v1/chosen-range"
	s3aRangeMagic  = "LNS3R001"
	s3aMediaType   = "application/octet-stream"
)

// Start transfers a private listener only after M/P/B construction completes.
// It never registers product routes, DI providers, or environment knobs.
func (o *s3aOwner) Start(listener net.Listener) error {
	if listener == nil {
		return errS3AConfig
	}
	o.serverMu.Lock()
	defer o.serverMu.Unlock()
	if o.check(o.ctx) != nil || o.server != nil {
		_ = listener.Close()
		return errS3AClosed
	}
	u, _ := url.Parse(o.identity.self.Origin)
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil || port != u.Port() {
		_ = listener.Close()
		return errS3AConfig
	}
	bounded := &s3aListener{Listener: listener, ctx: o.ctx, slots: make(chan struct{}, o.limits.MaxConnections)}
	o.listener = bounded
	protected := o.membership.ProtectHandler(http.HandlerFunc(o.serveHTTP))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !o.enterCall() {
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now())
			return
		}
		defer o.calls.Done()
		// The shared workload wrapper may set a later certificate/manifest
		// write deadline. Preserve this owner's shorter configured RPC bound
		// in the context, so its cancellation also suppresses response release.
		ctx, cancel := context.WithTimeout(r.Context(), o.limits.RPCTimeout)
		defer cancel()
		protected.ServeHTTP(w, r.WithContext(ctx))
	})
	o.server = &http.Server{Handler: handler,
		ReadHeaderTimeout: min(5*time.Second, o.limits.RPCTimeout), ReadTimeout: o.limits.RPCTimeout,
		WriteTimeout: o.limits.RPCTimeout, IdleTimeout: o.limits.RPCTimeout, MaxHeaderBytes: 32 << 10,
		BaseContext: func(net.Listener) context.Context { return o.ctx }}
	o.workers.Add(1)
	go func() {
		defer o.workers.Done()
		err := o.server.Serve(tls.NewListener(bounded, o.tlsConfig()))
		if err != nil && !errors.Is(err, http.ErrServerClosed) && o.ctx.Err() == nil {
			o.fail()
		}
	}()
	return nil
}

type s3aListener struct {
	net.Listener
	ctx   context.Context
	slots chan struct{}
}

func (l *s3aListener) Accept() (net.Conn, error) {
	select {
	case <-l.ctx.Done():
		return nil, net.ErrClosed
	case l.slots <- struct{}{}:
	}
	c, err := l.Listener.Accept()
	if err != nil {
		<-l.slots
		return nil, err
	}
	return &s3aConn{Conn: c, release: func() { <-l.slots }}, nil
}

type s3aConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *s3aConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

func s3aReadBody(r io.Reader, limit uint64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil || uint64(len(raw)) > limit {
		return nil, errS3AWire
	}
	return raw, nil
}

func (o *s3aOwner) frameBytes() uint64 {
	b := o.kernel.trust.bounds
	return min(b.PayloadBytes, uint64(s2cMessageOverhead)+b.ProofBytes+b.HistoricalBytes)
}

func (o *s3aOwner) serveHTTP(w http.ResponseWriter, r *http.Request) {
	admission, ok := peerauth.AdmissionFromContext(r.Context())
	fail := func(code int) {
		if o.check(r.Context()) != nil || ok && admission.Check(r.Context()) != nil {
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now())
			return
		}
		http.Error(w, "private control unavailable", code)
	}
	u, _ := url.Parse(o.identity.self.Origin)
	if !ok || o.check(r.Context()) != nil || r.Method != http.MethodPost || r.Host != u.Host ||
		r.URL.Scheme != "" || r.URL.Host != "" || r.URL.RawPath != "" || r.URL.RawQuery != "" || r.URL.ForceQuery ||
		r.Header.Get("Content-Type") != s3aMediaType || len(r.Header.Values("Content-Type")) != 1 ||
		len(r.Header.Values("Proxy-Authorization")) != 0 {
		fail(http.StatusForbidden)
		return
	}
	sender, known := o.byIdentity[admission.Member().Identity]
	if !known || o.peers[sender].Workload != admission.Member() {
		fail(http.StatusForbidden)
		return
	}
	select {
	case o.inbound[sender] <- struct{}{}:
		defer func() { <-o.inbound[sender] }()
	default:
		fail(http.StatusServiceUnavailable)
		return
	}
	check := func() error {
		if err := o.check(r.Context()); err != nil {
			return err
		}
		return admission.Check(r.Context())
	}
	var response []byte
	switch r.URL.Path {
	case s3aMessagePath:
		if r.ContentLength > int64(o.frameBytes()) {
			fail(http.StatusRequestEntityTooLarge)
			return
		}
		raw, err := s3aReadBody(r.Body, o.frameBytes())
		if err != nil || check() != nil {
			fail(http.StatusBadRequest)
			return
		}
		if err := o.receive(r.Context(), sender, raw, check); err != nil {
			fail(http.StatusServiceUnavailable)
			return
		}
	case s3aRangePath:
		raw, err := s3aReadBody(r.Body, 16)
		if err != nil || len(raw) != 16 || check() != nil {
			fail(http.StatusBadRequest)
			return
		}
		from, count := binary.BigEndian.Uint64(raw[:8]), binary.BigEndian.Uint64(raw[8:])
		if from == 0 || count == 0 || count > o.limits.RangeSlots || from > ^uint64(0)-count {
			fail(http.StatusBadRequest)
			return
		}
		parts, err := o.kernel.ExportChosen(from, count, o.limits.RangeBytes)
		if err != nil {
			if errors.Is(err, errS2CUnknown) || errors.Is(err, errS2CClosed) {
				o.fail()
			}
			fail(http.StatusServiceUnavailable)
			return
		}
		response = append([]byte(s3aRangeMagic), make([]byte, 4)...)
		binary.BigEndian.PutUint32(response[len(s3aRangeMagic):], uint32(len(parts)))
		for _, part := range parts {
			response = binary.BigEndian.AppendUint32(response, uint32(len(part)))
			response = append(response, part...)
		}
	default:
		fail(http.StatusNotFound)
		return
	}
	if o.hooks != nil && o.hooks.beforeResponse != nil {
		o.hooks.beforeResponse(r.Context())
	}
	if check() != nil {
		// Suppress even the transport ACK; this cannot undo durable work.
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now())
		return
	}
	if response == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", s3aMediaType)
	w.Header().Set("Content-Length", strconv.Itoa(len(response)))
	_, _ = w.Write(response)
}

func (o *s3aOwner) request(ctx context.Context, id uint32, path string, body []byte, limit uint64) ([]byte, int, error) {
	raw, status, _, err := o.requestAdmitted(ctx, id, path, body, limit)
	return raw, status, err
}

func (o *s3aOwner) requestAdmitted(ctx context.Context, id uint32, path string, body []byte, limit uint64) ([]byte, int, *peerauth.Admission, error) {
	if err := o.check(ctx); err != nil {
		return nil, 0, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, min(o.limits.RPCTimeout, o.identity.expires.Sub(o.now())))
	defer cancel()
	stop := context.AfterFunc(o.ctx, cancel)
	defer stop()
	peer, ok := o.peers[id]
	if !ok || id == o.kernel.config.Member {
		return nil, 0, nil, errS3AWire
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, peer.Workload.Origin+path, bytes.NewReader(body))
	if err != nil {
		return nil, 0, nil, err
	}
	request.Header.Set("Content-Type", s3aMediaType)
	response, err := o.client.Do(request)
	if err != nil {
		return nil, 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	admission, ok := peerauth.ResponseAdmission(response)
	if !ok || admission.Check(ctx) != nil {
		return nil, 0, nil, peerauth.ErrMembership
	}
	if len(response.Header.Values(peerauth.ControlHeader)) != 1 || response.Header.Get(peerauth.ControlHeader) != hex.EncodeToString(o.binding[:]) ||
		len(response.Header.Values(peerauth.DomainHeader)) != 0 || response.ContentLength > int64(limit) {
		return nil, 0, nil, errS3AWire
	}
	raw, err := s3aReadBody(response.Body, limit)
	if err != nil {
		return nil, 0, nil, err
	}
	if err := o.check(ctx); err != nil {
		return nil, 0, nil, err
	}
	if err := admission.Check(ctx); err != nil {
		return nil, 0, nil, err
	}
	return raw, response.StatusCode, admission, nil
}

func (o *s3aOwner) send(ctx context.Context, id uint32, raw []byte) error {
	_, status, err := o.request(ctx, id, s3aMessagePath, raw, 0)
	if err != nil {
		return err
	}
	if status != http.StatusNoContent {
		return errS3AWire
	}
	return nil
}
