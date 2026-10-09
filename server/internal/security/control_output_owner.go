package security

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"
)

const (
	authorityOutputUnitBytes   = 256 << 10
	authorityOutputHeaderBytes = 16 << 10
	authorityOutputConnections = 64
	authorityOutputActive      = 8
	// Includes original and detached encoding, envelope copies, both header
	// maps, bookkeeping and TLS/http1 buffers. Reserved before handler entry.
	authorityOutputRequestCharge    = 4*authorityOutputUnitBytes + 8*authorityOutputHeaderBytes + 128<<10
	authorityOutputConnectionCharge = 64 << 10
	authorityOutputPoolBytes        = 16 << 20
)

type authorityOutputPool struct {
	mu          sync.Mutex
	connections map[net.Conn]*authorityOutputConnection
	bytes       uint64
	active      int
	closed      bool
	server      *http.Server
}

type authorityOutputConnection struct {
	owner          *authorityOriginOwner
	conn           net.Conn
	active, closed bool // pool.mu
}
type authorityOutputContextKey struct{}

// Frozen by the trusted service adapter, never supplied as a caller's asserted
// authorization. Key lists are bounded; edge disclosure lists both endpoints.
type authorityOutputRequirement struct {
	action Action
	keys   []string
}

func (r authorityOutputRequirement) freeze() (authorityOutputRequirement, error) {
	kind, known := actionResource(r.action)
	if !known || len(r.keys) > 1024 || (kind == GlobalResource && len(r.keys) != 0) || (kind == DataResource && len(r.keys) == 0) {
		return r, ErrPermissionDenied
	}
	bytes := 0
	for _, key := range r.keys {
		bytes += len(key)
		if key == "" || bytes > 16<<10 {
			return r, ErrPermissionDenied
		}
	}
	r.keys = slices.Clone(r.keys)
	return r, nil
}
func (r authorityOutputRequirement) allows(p *S1Projection, c authorityCredential) bool {
	a, active := p.snapshot.AccessFor(c.actor)
	if !active {
		return false
	}
	kind, _ := actionResource(r.action)
	if kind == GlobalResource {
		return a.AllowsGlobal(r.action)
	}
	for _, key := range r.keys {
		if !a.Allows(r.action, key) {
			return false
		}
	}
	return true
}

// Install on the actual private http.Server, before Serve. Connection identity
// comes from net/http's accepted socket, never an address/header supplied by a
// request. This profile is HTTP/1.1 Connect protobuf without compression.
func (o *authorityOriginOwner) outputConnContext(ctx context.Context, c net.Conn) context.Context {
	p := &o.outputs
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || o.network.closed.Load() || len(p.connections) >= authorityOutputConnections || p.bytes+authorityOutputConnectionCharge > authorityOutputPoolBytes {
		_ = c.Close()
		return ctx
	}
	connection := &authorityOutputConnection{owner: o, conn: c}
	p.connections[c] = connection
	p.bytes += authorityOutputConnectionCharge
	return context.WithValue(ctx, authorityOutputContextKey{}, connection)
}
func (o *authorityOriginOwner) outputConnState(c net.Conn, state http.ConnState) {
	if state != http.StateClosed && state != http.StateHijacked {
		return
	}
	p := &o.outputs
	p.mu.Lock()
	defer p.mu.Unlock()
	if connection := p.connections[c]; connection != nil {
		connection.closed = true
		if !connection.active {
			delete(p.connections, c)
			p.bytes -= authorityOutputConnectionCharge
		}
	}
}
func (o *authorityOriginOwner) stopOutputs() {
	if o.publicOutputs != nil {
		o.publicOutputs.stop()
	}
	p := &o.outputs
	p.mu.Lock()
	p.closed = true
	server := p.server
	connections := make([]net.Conn, 0, len(p.connections))
	for c := range p.connections {
		connections = append(connections, c)
	}
	p.mu.Unlock()
	if server != nil {
		_ = server.Close()
	}
	for _, c := range connections {
		_ = c.Close()
	}
}
func (o *authorityOriginOwner) reserveOutput(c *authorityOutputConnection) bool {
	p := &o.outputs
	p.mu.Lock()
	defer p.mu.Unlock()
	if c == nil || c.owner != o || p.connections[c.conn] != c || p.closed || c.closed || c.active || p.active >= authorityOutputActive || p.bytes+authorityOutputRequestCharge > authorityOutputPoolBytes {
		return false
	}
	c.active, p.active, p.bytes = true, p.active+1, p.bytes+authorityOutputRequestCharge
	return true
}
func (o *authorityOriginOwner) releaseOutput(c *authorityOutputConnection) {
	p := &o.outputs
	p.mu.Lock()
	defer p.mu.Unlock()
	c.active, p.active, p.bytes = false, p.active-1, p.bytes-authorityOutputRequestCharge
	if c.closed {
		delete(p.connections, c.conn)
		p.bytes -= authorityOutputConnectionCharge
	}
}

func (o *authorityOriginOwner) authorizeOutput(ctx context.Context, c authorityCredential, requirement authorityOutputRequirement) (authorityCurrentTime, error) {
	k := o.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if ctx.Err() != nil || !c.matches(k.replayState.projection) || !requirement.allows(k.replayState.projection, c) {
		return authorityCurrentTime{}, ErrPermissionDenied
	}
	return o.authorizeCurrentCredentialLocked(ctx, c)
}

// The caller owns kernel.gate and fixes all non-time inputs before this final
// native sample. The public admission/output adapters share the same event.
func (o *authorityOriginOwner) authorizeCurrentCredentialLocked(ctx context.Context, c authorityCredential) (authorityCurrentTime, error) {
	if ctx.Err() != nil || !c.matches(o.network.kernel.replayState.projection) {
		return authorityCurrentTime{}, ErrPermissionDenied
	}
	start, expiry, err := o.eligibilityLocked(ctx)
	if err != nil {
		return authorityCurrentTime{}, err
	}
	start = maxAuthorityTime(start, c.claim.DerivedStart.Time())
	expiry = authorityMinTime(expiry, c.claim.AdmissionDeadline)
	// Every non-time input and all finite credits are fixed by the caller.
	// This sampling event authorizes exactly that immutable unit, once.
	now, _, err := o.network.receiver.currentLocked()
	if err != nil {
		return authorityCurrentTime{}, err
	}
	if time.Unix(0, int64(now.utc.low)).Before(start) || !time.Unix(0, int64(now.utc.high)).Before(expiry) {
		return authorityCurrentTime{}, ErrPermissionDenied
	}
	return now, nil
}
func maxAuthorityTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// Producer/requirement are selected by trusted private composition per request.
// Whole request credit covers the synchronous Connect encoder, including before
// the first Write. There is no retained application output queue or stream permit.
func (o *authorityOriginOwner) serveOutput(w http.ResponseWriter, r *http.Request, producer CurrentCredentialProducer, requirement authorityOutputRequirement, handler func() http.Handler) {
	c, _ := r.Context().Value(authorityOutputContextKey{}).(*authorityOutputConnection)
	if o == nil || r.TLS == nil || r.ProtoMajor != 1 || r.Method != http.MethodPost || !o.network.enterCall() {
		panic(http.ErrAbortHandler)
	}
	defer o.network.calls.Done()
	if !o.reserveOutput(c) {
		panic(http.ErrAbortHandler)
	}
	defer o.releaseOutput(c)
	stop := context.AfterFunc(o.network.ctx, func() { _ = c.conn.Close() })
	defer stop()
	content := r.Header.Get("Content-Type")
	if (content != "application/proto" && content != "application/connect+proto") || r.Header.Get("Content-Encoding") != "" || r.Header.Get("Connect-Content-Encoding") != "" {
		panic(http.ErrAbortHandler)
	}
	requirement, err := requirement.freeze()
	if err != nil {
		panic(http.ErrAbortHandler)
	}
	view, facts, err := o.verifyFacts(r.Context(), producer)
	if err != nil {
		panic(http.ErrAbortHandler)
	}
	credential, err := captureAuthorityUseCredential(view, facts, false)
	if err != nil {
		panic(http.ErrAbortHandler)
	}
	writer := &authorityOutputWriter{owner: o, recipient: c, credential: credential, requirement: requirement, destination: w, ctx: r.Context(), headers: make(http.Header), status: http.StatusOK, hooks: o.outputHooks.Load()}
	handler().ServeHTTP(writer, r)
	if !writer.sent {
		if _, err := writer.Write(nil); err != nil {
			panic(http.ErrAbortHandler)
		}
	}
}

// One private server, finite typed routes and no shared response-buffer pool.
// Uses the same enrolled workload certificate whose complete validity interval
// is checked at every final output event. The caller supplies its owned listener
// to ServeTLS; the origin joins the server and all output calls during Close.
func (o *authorityOriginOwner) newOutputServer(routes []authorityOutputRoute, producer func(*http.Request) (CurrentCredentialProducer, error), scope func(*http.Request) authorityOutputRequirement) (*http.Server, error) {
	if o == nil || len(routes) == 0 || len(routes) > 16 || producer == nil || scope == nil {
		return nil, errS3AConfig
	}
	registered := make(map[string]authorityOutputRoute, len(routes))
	for _, r := range routes {
		if r.path == "" || r.path[0] != '/' || len(r.path) > 512 || r.create == nil {
			return nil, errS3AConfig
		}
		if _, exists := registered[r.path]; exists {
			return nil, errS3AConfig
		}
		registered[r.path] = r
	}
	server := &http.Server{ConnContext: o.outputConnContext, ConnState: o.outputConnState, MaxHeaderBytes: authorityOutputHeaderBytes, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, NextProtos: []string{"http/1.1"}, Certificates: []tls.Certificate{o.network.identity.certificate}, SessionTicketsDisabled: true}}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route, known := registered[r.URL.Path]
		if !known {
			http.NotFound(w, r)
			return
		}
		p, err := producer(r)
		if err != nil {
			panic(http.ErrAbortHandler)
		}
		o.serveOutput(w, r, p, scope(r), route.create)
	})
	o.outputs.mu.Lock()
	defer o.outputs.mu.Unlock()
	if o.outputs.closed || o.outputs.server != nil {
		return nil, errS3AClosed
	}
	o.outputs.server = server
	return server, nil
}
