package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anaregdesign/lantern/core/privatefile"
)

// receiptHAControl isolates data replication without isolating policy leases.
// Node zero is a separate policy writer; nodes one through three are A/B/C.
// This fixture is local conformance infrastructure, never a production proxy.
type receiptHAControl struct {
	connected    atomic.Bool
	identities   map[string]int
	fingerprints map[string][32]byte
}

func (c *receiptHAControl) allowed(caller, recipient int, path string) bool {
	if strings.HasPrefix(path, "/graph.v1.LanternSecurityPeerService/") {
		return true
	}
	if !strings.HasPrefix(path, "/graph.v1.LanternReplicationService/") {
		return false
	}
	if caller == 0 || recipient == 0 {
		return false
	}
	return c.connected.Load() || caller != 3 && recipient != 3
}
func (c *receiptHAControl) caller(state *tls.ConnectionState) (int, bool) {
	if state == nil || len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 || len(state.PeerCertificates[0].URIs) != 1 {
		return 0, false
	}
	leaf := state.PeerCertificates[0]
	identity := leaf.URIs[0].String()
	index, ok := c.identities[identity]
	return index, ok && c.fingerprints[identity] == sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
}
func receiptHACert(node fixtureNode) (tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(node.Environment["LANTERN_PEER_CERT_FILE"], node.Environment["LANTERN_PEER_KEY_FILE"])
	if err != nil {
		return cert, err
	}
	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	return cert, err
}

// receiptHARelays preserve each admitted caller's workload certificate on the
// upstream hop. No browser/machine token, TLS bypass or origin rewrite is used.
func receiptHARelays(result fixture, control *receiptHAControl) (_ fixture, cleanup func(), err error) {
	if len(result.Nodes) != 4 {
		return result, nil, errors.New("four native nodes required")
	}
	raw, err := os.ReadFile(result.CAFile)
	if err != nil {
		return result, nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(raw) {
		return result, nil, errors.New("invalid relay CA")
	}
	certs := make([]tls.Certificate, 4)
	control.identities = map[string]int{}
	control.fingerprints = map[string][32]byte{}
	for i, node := range result.Nodes {
		certs[i], err = receiptHACert(node)
		if err != nil {
			return result, nil, err
		}
		id := node.Environment["LANTERN_PEER_WORKLOAD_ID"]
		control.identities[id] = i
		control.fingerprints[id] = sha256.Sum256(certs[i].Leaf.RawSubjectPublicKeyInfo)
	}
	var servers []*http.Server
	var listeners []net.Listener
	var transports []*http.Transport
	var once sync.Once
	cleanup = func() {
		once.Do(func() {
			for _, server := range servers {
				_ = server.Close()
			}
			for _, listener := range listeners {
				_ = listener.Close()
			}
			for _, transport := range transports {
				transport.CloseIdleConnections()
			}
		})
	}
	defer func() {
		if err != nil {
			cleanup()
		}
	}()
	reservedPorts := map[string]bool{}
	for _, node := range result.Nodes {
		for _, rawOrigin := range []string{node.PublicOrigin, node.PeerOrigin} {
			origin, parseErr := url.Parse(rawOrigin)
			if parseErr != nil || origin.Port() == "" {
				return result, cleanup, errors.New("invalid owned relay origin")
			}
			reservedPorts[origin.Port()] = true
		}
	}
	for recipient := range result.Nodes {
		node := &result.Nodes[recipient]
		origin, parseErr := url.Parse(node.PeerOrigin)
		if parseErr != nil {
			return result, cleanup, parseErr
		}
		listener, listenErr := net.Listen("tcp", origin.Host)
		if listenErr != nil {
			return result, cleanup, listenErr
		}
		listeners = append(listeners, listener)
		// Reserve a backend listener before releasing it for the production child.
		var address string
		for attempt := 0; attempt < 32; attempt++ {
			backend, listenErr := net.Listen("tcp", "127.0.0.1:0")
			if listenErr != nil {
				return result, cleanup, listenErr
			}
			candidate := backend.Addr().String()
			_, port, _ := net.SplitHostPort(candidate)
			_ = backend.Close()
			if !reservedPorts[port] {
				address = candidate
				reservedPorts[port] = true
				break
			}
		}
		if address == "" {
			return result, cleanup, errors.New("cannot reserve distinct owned backend")
		}

		node.Environment["LANTERN_PEER_LISTEN_ADDR"] = address
		target, _ := url.Parse("https://" + address)
		proxies := make([]*httputil.ReverseProxy, 4)
		for caller := range proxies {
			transport := &http.Transport{ForceAttemptHTTP2: true, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{certs[caller]}}, MaxConnsPerHost: 16, MaxIdleConnsPerHost: 16, IdleConnTimeout: 10 * time.Second}
			transports = append(transports, transport)
			proxy := httputil.NewSingleHostReverseProxy(target)
			proxy.Transport = transport
			proxy.FlushInterval = -1
			proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
				http.Error(w, "owned relay unavailable", http.StatusServiceUnavailable)
			}
			proxies[caller] = proxy
		}
		slots := make(chan struct{}, 32)
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			caller, ok := control.caller(r.TLS)
			if !ok || caller == recipient || r.Method != http.MethodPost || r.URL.RawQuery != "" || !control.allowed(caller, recipient, r.URL.Path) {
				http.Error(w, "owned data partition", http.StatusServiceUnavailable)
				return
			}
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			default:
				http.Error(w, "owned relay capacity", http.StatusServiceUnavailable)
				return
			}
			proxies[caller].ServeHTTP(w, r)
		})
		server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 16 << 10, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certs[recipient]}, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert, SessionTicketsDisabled: true, VerifyConnection: func(state tls.ConnectionState) error {
			if _, ok := control.caller(&state); !ok {
				return errors.New("unapproved relay workload")
			}
			return nil
		}}}
		servers = append(servers, server)
		go func() { _ = server.ServeTLS(listener, "", "") }()
	}
	return result, cleanup, nil
}

func receiptHAStart(node fixtureNode, binary, directory, suffix string) (*fixtureProcess, error) {
	env, err := fixtureEnvironment(node, nil)
	if err != nil {
		return nil, err
	}
	log, err := privatefile.Create(filepath.Join(directory, node.Name+suffix+"-server.log"), os.O_WRONLY)
	if err != nil {
		return nil, err
	}
	command := exec.Command(binary)
	command.Env = env
	command.Stdout = log
	command.Stderr = log
	if err := command.Start(); err != nil {
		_ = log.Close()
		return nil, err
	}
	child := &fixtureProcess{command: command, log: log, done: make(chan struct{})}
	go func() { _ = command.Wait(); close(child.done) }()
	return child, nil
}

func serveReceiptHAFixture(ctx context.Context, result fixture, binary, directory string, input io.Reader, output io.Writer, readyTimeout time.Duration) error {
	if !filepath.IsAbs(binary) || readyTimeout < time.Second || readyTimeout > 5*time.Minute {
		return errors.New("absolute Server and bounded timeout required")
	}
	control := &receiptHAControl{}
	result, closeRelays, err := receiptHARelays(result, control)
	if err != nil {
		return err
	}
	defer closeRelays()
	children := make([]*fixtureProcess, 4)
	defer func() {
		for i := len(children) - 1; i >= 0; i-- {
			if children[i] != nil {
				children[i].stop()
			}
		}
	}()
	for i, node := range result.Nodes {
		for key, value := range map[string]string{"LANTERN_PUMP_BACKOFF_MIN_MS": "50", "LANTERN_PUMP_BACKOFF_MAX_MS": "200", "LANTERN_ANTI_ENTROPY_INTERVAL_MS": "250"} {
			node.Environment[key] = value
		}
		children[i], err = receiptHAStart(node, binary, directory, "")
		if err != nil {
			return err
		}
	}
	startup, cancel := context.WithTimeout(ctx, readyTimeout)
	err = waitFixtureReady(startup, result, children)
	cancel()
	if err != nil {
		return err
	}
	if err := json.NewEncoder(output).Encode(result); err != nil {
		return err
	}
	commands := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 64), 1024)
		for scanner.Scan() {
			select {
			case commands <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
		close(commands)
	}()
	var stoppedA, relayedC bool
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case command, ok := <-commands:
			if !ok || command == "shutdown" {
				return nil
			}
			switch command {
			case "stop-origin":
				if stoppedA {
					return errors.New("origin already stopped")
				}
				children[1].stop()
				children[1] = nil
				stoppedA = true
			case "relay-b-to-c":
				if !stoppedA || relayedC {
					return errors.New("relay requires one explicit origin shutdown")
				}
				children[3].stop()
				children[3] = nil
				node := result.Nodes[3]
				node.Environment["LANTERN_PEER_MEMBERSHIP_MODE"] = "resume"
				node.Environment["LANTERN_SECURITY_STORE_MODE"] = "restart"
				node.Environment["LANTERN_RECEIPT_WAL_MODE"] = "restart"
				control.connected.Store(true)
				children[3], err = receiptHAStart(node, binary, directory, "-restart")
				if err != nil {
					return err
				}
				startup, cancel := context.WithTimeout(ctx, readyTimeout)
				err = waitFixtureReady(startup, fixture{CAFile: result.CAFile, Nodes: []fixtureNode{node}}, []*fixtureProcess{children[3]})
				cancel()
				if err != nil {
					return err
				}
				relayedC = true
			default:
				return errors.New("unknown receipt HA command")
			}
			if err := json.NewEncoder(output).Encode(map[string]string{"completed": command}); err != nil {
				return err
			}
		case <-ticker.C:
			for _, child := range children {
				if child != nil {
					select {
					case <-child.done:
						return errors.New("owned receipt HA node exited")
					default:
					}
				}
			}
		}
	}
}
