package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"sync"
	"time"
)

// receiptHADataRelays retain advertised signed data endpoints while replacing
// unsigned child backend generations. The authority plane never traverses them.
type receiptHADataRelays struct {
	mu               sync.Mutex
	control          *receiptHAControl
	roots            *x509.CertPool
	certs            []tls.Certificate
	servers          []*http.Server
	targets          []*receiptHADataGeneration
	frontConnections map[net.Conn]struct{}
	workers          sync.WaitGroup
	once             sync.Once
}

type receiptHADataGeneration struct {
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex
	closed      bool
	active      int
	connections map[*receiptHADataConnection]struct{}
	proxies     []*httputil.ReverseProxy
	transports  []*http.Transport
	workers     sync.WaitGroup
	dials       sync.WaitGroup
}

type receiptHADataConnection struct {
	net.Conn
	generation *receiptHADataGeneration
}

func (c *receiptHADataConnection) Close() error {
	err := c.Conn.Close()
	c.generation.mu.Lock()
	delete(c.generation.connections, c)
	c.generation.mu.Unlock()
	return err
}

func receiptHARelays(f fixture, control *receiptHAControl, listeners []net.Listener) (_ *receiptHADataRelays, err error) {
	if len(f.Nodes) != 4 || len(listeners) != 4 {
		return nil, errors.New("four retained HA relays required")
	}
	raw, err := os.ReadFile(f.CAFile)
	if err != nil {
		return nil, err
	}
	r := &receiptHADataRelays{control: control, roots: x509.NewCertPool(), certs: make([]tls.Certificate, 4), targets: make([]*receiptHADataGeneration, 4), frontConnections: map[net.Conn]struct{}{}}
	defer func() {
		if err != nil {
			r.close()
		}
	}()
	if !r.roots.AppendCertsFromPEM(raw) {
		return nil, errors.New("invalid relay CA")
	}
	control.identities = map[string]int{}
	control.fingerprints = map[string][32]byte{}
	for i, node := range f.Nodes {
		r.certs[i], err = receiptHACert(node)
		if err != nil {
			return nil, err
		}
		leaf := r.certs[i].Leaf
		material, err := fixturePeerMaterial(node)
		if err != nil {
			return nil, err
		}
		identity := material.SelfIdentity
		if len(leaf.URIs) != 1 || leaf.URIs[0].String() != identity {
			return nil, errors.New("relay workload identity mismatch")
		}
		if _, duplicate := control.identities[identity]; duplicate {
			return nil, errors.New("duplicate relay workload")
		}
		control.identities[identity] = i
		control.fingerprints[identity] = receiptHASPKI(leaf)
		origin, e := url.Parse(node.PeerOrigin)
		if e != nil || origin.Scheme != "https" || origin.Hostname() != "localhost" || origin.Port() != listenerPort(listeners[i]) {
			return nil, errors.New("relay origin does not name retained listener")
		}
	}
	for recipient, listener := range listeners {
		handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			caller, ok := control.caller(req.TLS)
			if !ok || caller == recipient || req.Method != http.MethodPost || req.URL.RawQuery != "" || !control.allowed(caller, recipient, req.URL.Path) {
				http.Error(w, "owned data partition", http.StatusServiceUnavailable)
				return
			}
			r.mu.Lock()
			g := r.targets[recipient]
			if g != nil {
				g.mu.Lock()
				if g.closed || g.active >= 32 {
					g.mu.Unlock()
					g = nil
				} else {
					g.active++
					g.workers.Add(1)
					g.mu.Unlock()
				}
			}
			r.mu.Unlock()
			if g == nil {
				http.Error(w, "owned relay unavailable", http.StatusServiceUnavailable)
				return
			}
			defer func() { g.mu.Lock(); g.active--; g.mu.Unlock(); g.workers.Done() }()
			ctx, cancel := context.WithCancel(req.Context())
			stop := context.AfterFunc(g.ctx, cancel)
			defer func() { stop(); cancel() }()
			g.proxies[caller].ServeHTTP(w, req.WithContext(ctx))
		})
		server := &http.Server{Handler: handler, ErrorLog: log.New(io.Discard, "", 0), ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 16 << 10, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{r.certs[recipient]}, ClientCAs: r.roots, ClientAuth: tls.RequireAndVerifyClientCert, SessionTicketsDisabled: true, VerifyConnection: func(state tls.ConnectionState) error {
			if _, ok := control.caller(&state); !ok {
				return errors.New("unapproved relay workload")
			}
			return nil
		}}, ConnState: func(conn net.Conn, state http.ConnState) {
			r.mu.Lock()
			defer r.mu.Unlock()
			if state == http.StateNew {
				r.frontConnections[conn] = struct{}{}
			}
			if state == http.StateClosed || state == http.StateHijacked {
				delete(r.frontConnections, conn)
			}
		}}
		r.servers = append(r.servers, server)
		r.workers.Add(1)
		go func() { defer r.workers.Done(); _ = server.ServeTLS(listener, "", "") }()
	}
	return r, nil
}

func listenerPort(listener net.Listener) string {
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	return port
}

func (r *receiptHADataRelays) resume(f fixture) error {
	if len(f.Nodes) != 4 {
		return errors.New("four original relay recipients required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, target := range r.targets {
		if target != nil {
			return errors.New("relay generation still active")
		}
	}
	for recipient, node := range f.Nodes {
		address := node.Environment["LANTERN_PEER_LISTEN_ADDR"]
		if node.StartupFile != "" {
			var err error
			address, err = fixtureDataListen(node)
			if err != nil {
				return err
			}
		}
		if _, err := fixtureReservedPort(address, true); err != nil {
			return err
		}
		ctx, cancel := context.WithCancel(context.Background())
		g := &receiptHADataGeneration{ctx: ctx, cancel: cancel, connections: map[*receiptHADataConnection]struct{}{}}
		target, _ := url.Parse("https://" + address)
		for caller := 0; caller < 4; caller++ {
			transport := &http.Transport{ForceAttemptHTTP2: true, MaxConnsPerHost: 16, MaxIdleConnsPerHost: 16, IdleConnTimeout: 10 * time.Second, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: r.roots, Certificates: []tls.Certificate{r.certs[caller]}, VerifyConnection: func(state tls.ConnectionState) error {
				index, ok := r.control.caller(&state)
				if !ok || index != recipient {
					return errors.New("wrong relay recipient")
				}
				return nil
			}}, DialContext: g.dial}
			proxy := httputil.NewSingleHostReverseProxy(target)
			proxy.Transport = transport
			proxy.FlushInterval = -1
			proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
				http.Error(w, "owned relay unavailable", http.StatusServiceUnavailable)
			}
			g.transports = append(g.transports, transport)
			g.proxies = append(g.proxies, proxy)
		}
		r.targets[recipient] = g
	}
	return nil
}

func (g *receiptHADataGeneration) dial(ctx context.Context, network, address string) (net.Conn, error) {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil, errors.New("relay generation retired")
	}
	g.dials.Add(1)
	g.mu.Unlock()
	defer g.dials.Done()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(g.ctx, cancel)
	defer func() { stop(); cancel() }()
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	c := &receiptHADataConnection{Conn: conn, generation: g}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		_ = conn.Close()
		return nil, errors.New("relay generation retired")
	}
	g.connections[c] = struct{}{}
	return c, nil
}

func (g *receiptHADataGeneration) close() {
	g.cancel()
	g.mu.Lock()
	g.closed = true
	connections := make([]*receiptHADataConnection, 0, len(g.connections))
	for conn := range g.connections {
		connections = append(connections, conn)
	}
	g.mu.Unlock()
	for _, conn := range connections {
		_ = conn.Close()
	}
	g.workers.Wait()
	g.dials.Wait()
	for _, transport := range g.transports {
		transport.CloseIdleConnections()
	}
}

func (r *receiptHADataRelays) pause() {
	r.mu.Lock()
	targets := r.targets
	r.targets = make([]*receiptHADataGeneration, 4)
	connections := make([]net.Conn, 0, len(r.frontConnections))
	for conn := range r.frontConnections {
		connections = append(connections, conn)
	}
	r.mu.Unlock()
	for _, conn := range connections {
		_ = conn.Close()
	}
	for i := len(targets) - 1; i >= 0; i-- {
		if targets[i] != nil {
			targets[i].close()
		}
	}
}

func (r *receiptHADataRelays) close() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		for i := len(r.servers) - 1; i >= 0; i-- {
			_ = r.servers[i].Close()
		}
		r.workers.Wait()
		r.pause()
	})
}
