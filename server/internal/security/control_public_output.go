package security

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
)

const (
	currentOutputHeaderBytes = 32 << 10
	currentOutputPoolBytes   = uint64(2 << 30)
	currentOutputConnections = 64
	currentOutputRequests    = 8
)

// CurrentOutputLimits are the actual public listener's message/stream limits.
// A zero/unbounded setting cannot certify a finite current output owner.
// Encoded messages are bounded before compression, as well as on the wire.
type CurrentOutputLimits struct {
	ReadBytes, SendBytes int
	ConcurrentStreams    uint32
}

// CurrentOutput owns request-lifetime encoding pools and immutable Write units
// on the actual accepted sockets. An HTTP/2 request has its own recipient and
// sequence; a connection is not a long-lived output permit. No unit is queued
// here, and all producer/encoder calls join the existing authority owner Close.
type CurrentOutput struct {
	owner                           *CurrentAuthority
	server                          *http.Server
	limits                          CurrentOutputLimits
	requestCharge, connectionCharge uint64
	wireBytes                       int
	mu                              sync.Mutex
	connections                     map[net.Conn]*currentOutputConnection
	bytes                           uint64
	active                          int
	closed                          bool
}

type currentOutputConnection struct {
	owner    *CurrentOutput
	conn     net.Conn
	active   int
	sequence uint64
	closed   bool
}
type currentOutputConnectionKey struct{}
type currentOutputRequestKey struct{}
type currentOutputRequest struct {
	owner        *CurrentOutput
	connection   *currentOutputConnection
	sequence     uint64
	method, path string
	mu           sync.Mutex
	grant        currentOutputGrant
	started      bool
	writer       *currentOutputWriter
}

// ConfigurePublicOutput runs once, before Serve, on the real public server.
// Budget includes encoder originals/copies, compression buffers, error metadata,
// request buffers and finite HTTP/2 stream/header/flow-control storage. Credits
// are reservations, not eager allocations of the configured maximum message.
func (o *CurrentAuthority) ConfigurePublicOutput(server *http.Server, limits CurrentOutputLimits) (*CurrentOutput, error) {
	if o == nil || o.origin == nil || server == nil || limits.ReadBytes <= 0 || limits.SendBytes <= 0 || limits.ConcurrentStreams == 0 || limits.ReadBytes > 64<<20 || limits.SendBytes > 64<<20 || limits.ConcurrentStreams > 4096 {
		return nil, ErrS1Contract
	}
	// Complete the fixed linked-schema certificate before any socket can enter.
	_ = currentDecodeSchemas()
	p := &CurrentOutput{owner: o, server: server, limits: limits, connections: make(map[net.Conn]*currentOutputConnection)}
	p.requestCharge = uint64(4*limits.ReadBytes+10*limits.SendBytes+1<<20) + currentDecodeCharge(limits.ReadBytes)
	p.connectionCharge = uint64(limits.ConcurrentStreams+1)*(2*currentOutputHeaderBytes+16<<10) + 4<<20
	p.wireBytes = limits.SendBytes + limits.SendBytes/65535*5 + 1<<20
	if p.requestCharge > currentOutputPoolBytes/2 || p.connectionCharge > currentOutputPoolBytes/4 {
		return nil, ErrControlReserve
	}
	k := o.origin.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	if o.origin.closed || o.origin.publicOutputs != nil || server.ConnContext != nil || server.ConnState != nil {
		return nil, ErrAuthorityUnavailable
	}
	o.origin.publicOutputs = p
	server.ConnContext, server.ConnState = p.connContext, p.connState
	server.MaxHeaderBytes = currentOutputHeaderBytes
	server.HTTP2 = &http.HTTP2Config{MaxConcurrentStreams: int(limits.ConcurrentStreams), MaxDecoderHeaderTableSize: 4096, MaxEncoderHeaderTableSize: 4096, MaxReadFrameSize: 16 << 10, MaxReceiveBufferPerConnection: 1 << 20, MaxReceiveBufferPerStream: 64 << 10}
	return p, nil
}

func (p *CurrentOutput) connContext(ctx context.Context, c net.Conn) context.Context {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.owner.origin.network.closed.Load() || len(p.connections) >= currentOutputConnections || p.bytes+p.connectionCharge > currentOutputPoolBytes {
		_ = c.Close()
		return ctx
	}
	owned := &currentOutputConnection{owner: p, conn: c}
	p.connections[c] = owned
	p.bytes += p.connectionCharge
	return context.WithValue(ctx, currentOutputConnectionKey{}, owned)
}

func (p *CurrentOutput) connState(c net.Conn, state http.ConnState) {
	if state != http.StateClosed && state != http.StateHijacked {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if owned := p.connections[c]; owned != nil {
		owned.closed = true
		if owned.active == 0 {
			delete(p.connections, c)
			p.bytes -= p.connectionCharge
		}
	}
}

func (p *CurrentOutput) reserve(c *currentOutputConnection, r *http.Request) (*currentOutputRequest, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c == nil || c.owner != p || p.connections[c.conn] != c || p.closed || c.closed || c.sequence == ^uint64(0) || p.active >= currentOutputRequests || uint32(c.active) >= p.limits.ConcurrentStreams || p.bytes+p.requestCharge > currentOutputPoolBytes {
		return nil, ErrControlReserve
	}
	c.sequence++
	c.active++
	p.active++
	p.bytes += p.requestCharge
	return &currentOutputRequest{owner: p, connection: c, sequence: c.sequence, method: r.Method, path: r.URL.Path}, nil
}

func (p *CurrentOutput) release(r *currentOutputRequest) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c := r.connection
	c.active--
	p.active--
	p.bytes -= p.requestCharge
	if c.closed && c.active == 0 {
		delete(p.connections, c.conn)
		p.bytes -= p.connectionCharge
	}
}

func (p *CurrentOutput) stop() error {
	p.mu.Lock()
	p.closed = true
	connections := make([]net.Conn, 0, len(p.connections))
	for c := range p.connections {
		connections = append(connections, c)
	}
	p.mu.Unlock()
	closes := []func() error{p.server.Close}
	for _, c := range connections {
		closes = append(closes, c.Close)
	}
	return closeAuthorityOutputs(closes...)
}

func (p *CurrentOutput) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !p.owner.origin.network.enterCall() {
			panic(http.ErrAbortHandler)
		}
		defer p.owner.origin.network.calls.Done()
		c, _ := r.Context().Value(currentOutputConnectionKey{}).(*currentOutputConnection)
		owned, err := p.reserve(c, r)
		if err != nil {
			panic(http.ErrAbortHandler)
		}
		defer p.release(owned)
		ctx, cancel := context.WithCancel(r.Context())
		stop := context.AfterFunc(p.owner.origin.network.ctx, cancel)
		defer func() { stop(); cancel() }()
		ctx = context.WithValue(ctx, currentOutputRequestKey{}, owned)
		writer := &currentOutputWriter{request: owned, destination: w, ctx: ctx, headers: make(http.Header), status: http.StatusOK, hooks: p.owner.origin.outputHooks.Load()}
		owned.writer = writer
		next.ServeHTTP(writer, r.WithContext(ctx))
		if err := writer.finish(); err != nil {
			panic(http.ErrAbortHandler)
		}
	})
}

// A fresh Connect handler confines its internal sync.Pool/compressors to one
// admitted request. Only the typed service is shared. This wrapper cannot be
// entered without prior connection/global credit from the outer public owner.
func (p *CurrentOutput) Handler(create func() http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		owned, _ := r.Context().Value(currentOutputRequestKey{}).(*currentOutputRequest)
		if owned == nil || owned.owner != p || create == nil {
			panic(http.ErrAbortHandler)
		}
		create().ServeHTTP(w, r)
	})
}

func currentRequest(ctx context.Context, owner *CurrentAuthority) (*currentOutputRequest, error) {
	r, _ := ctx.Value(currentOutputRequestKey{}).(*currentOutputRequest)
	if r == nil || r.owner.owner != owner || r.sequence == 0 {
		return nil, ErrAuthorityUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return r, nil
}

var errCurrentOutput = errors.New("current output unavailable")
