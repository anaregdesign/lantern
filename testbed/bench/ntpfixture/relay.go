package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

const relayProtocol = "bench_local_tcp_relay_v1"

type relayRoute struct {
	ListenPort  int    `json:"listen_port"`
	BackendIP   string `json:"backend_ip"`
	BackendPort int    `json:"backend_port"`
}

type relayConfig struct {
	Schema         int          `json:"schema"`
	Protocol       string       `json:"protocol"`
	MaxConnections int          `json:"max_connections"`
	FrontendIPv6   *bool        `json:"frontend_ipv6"`
	BackendIPv6    *bool        `json:"backend_ipv6"`
	Routes         []relayRoute `json:"routes"`
}

type relayStatus struct {
	Schema         int    `json:"schema"`
	Protocol       string `json:"protocol"`
	PID            int    `json:"pid"`
	StartNonce     string `json:"start_nonce"`
	ConfigSHA256   string `json:"config_sha256"`
	IPv4Forwarding string `json:"ipv4_forwarding"`
	IPv6Forwarding string `json:"ipv6_forwarding"`
	Accepted       uint64 `json:"accepted"`
	Active         uint64 `json:"active"`
	Completed      uint64 `json:"completed"`
	Rejected       uint64 `json:"rejected"`
	DialFailure    uint64 `json:"dial_failure"`
	CopyFailure    uint64 `json:"copy_failure"`
}

// A relay owns raw TCP connections only. It never interprets HTTP/TLS bytes,
// queries DNS or constructs a clock, UDP client or upstream proxy.
type tcpRelay struct {
	config      relayConfig
	mu          sync.Mutex
	status      relayStatus
	connections map[net.Conn]struct{}
	stopping    bool
	ready       bool
	workers     sync.WaitGroup
}

func relayJSONValue(decoder *json.Decoder, depth int) error {
	if depth > 4 {
		return errors.New("relay configuration nesting exceeds schema")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	if delimiter != '{' && delimiter != '[' {
		return errors.New("invalid relay JSON delimiter")
	}
	seen := make(map[string]bool)
	for decoder.More() {
		if delimiter == '{' {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return errors.New("duplicate relay configuration key")
			}
			rootKey := depth == 0 && (name == "schema" || name == "protocol" || name == "max_connections" || name == "routes" || name == "frontend_ipv6" || name == "backend_ipv6")
			routeKey := depth == 2 && (name == "listen_port" || name == "backend_ip" || name == "backend_port")
			if !rootKey && !routeKey {
				return errors.New("unknown or case-aliased relay configuration key")
			}
			seen[name] = true
		}
		if err := relayJSONValue(decoder, depth+1); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

func loadRelayConfig(path string) (relayConfig, string, error) {
	var config relayConfig
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return config, "", errors.New("absolute relay config path required")
	}
	physical, err := filepath.EvalSymlinks(path)
	if err != nil || physical != path {
		return config, "", errors.New("physical relay config file required")
	}
	before, err := os.Lstat(path)
	if err != nil {
		return config, "", err
	}
	if !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > 16<<10 {
		return config, "", errors.New("bounded regular relay config required")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return config, "", err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return config, "", errors.New("relay config changed before reading")
	}
	raw, err := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	if err != nil {
		return config, "", err
	}
	after, err := file.Stat()
	if err != nil {
		return config, "", err
	}
	final, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, final) || !os.SameFile(before, after) || int64(len(raw)) != before.Size() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || !before.ModTime().Equal(final.ModTime()) {
		return config, "", errors.New("relay config changed while reading")
	}
	unique := json.NewDecoder(bytes.NewReader(raw))
	if err := relayJSONValue(unique, 0); err != nil {
		return config, "", err
	}
	if _, err := unique.Token(); err != io.EOF {
		return config, "", errors.New("extra relay JSON value")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, "", err
	}
	if err := config.validate(); err != nil {
		return config, "", err
	}
	sum := sha256.Sum256(raw)
	return config, hex.EncodeToString(sum[:]), nil
}

func (config relayConfig) validate() error {
	if config.Schema != 1 || config.Protocol != relayProtocol || config.MaxConnections != 4096 ||
		config.FrontendIPv6 == nil || *config.FrontendIPv6 || config.BackendIPv6 == nil || *config.BackendIPv6 ||
		len(config.Routes) != 3 && len(config.Routes) != 7 {
		return errors.New("fixed relay profile required")
	}
	ports := []int{6380, 6381, 6382, 9090, 9091, 9092, 9093}
	seen := make(map[string]bool)
	for index, route := range config.Routes {
		address, err := netip.ParseAddr(route.BackendIP)
		private := err == nil && address.Is4() && address.String() == route.BackendIP && address.IsPrivate()
		backendPort := 6380
		if index >= 3 {
			backendPort = 9090
		}
		if !private || route.ListenPort != ports[index] || route.BackendPort != backendPort {
			return errors.New("relay routes must be ordered fixed ports and literal private IPv4")
		}
		if index < 3 || index == 6 {
			if seen[route.BackendIP] {
				return errors.New("relay backend identities must be distinct")
			}
			seen[route.BackendIP] = true
		} else if route.BackendIP != config.Routes[index-3].BackendIP {
			return errors.New("relay metrics route differs from API backend")
		}
	}
	return nil
}

func relayForwarding(config relayConfig, readFile func(string) ([]byte, error)) ([2]string, error) {
	var observed [2]string
	ipv4, err := readFile("/proc/sys/net/ipv4/ip_forward")
	if err != nil || string(ipv4) != "0\n" {
		return observed, errors.New("relay requires existing IPv4 forwarding disabled")
	}
	observed[0] = string(ipv4)
	ipv6, err := readFile("/proc/sys/net/ipv6/conf/all/forwarding")
	if errors.Is(err, os.ErrNotExist) && config.FrontendIPv6 != nil && !*config.FrontendIPv6 && config.BackendIPv6 != nil && !*config.BackendIPv6 {
		observed[1] = "absent"
		return observed, nil
	}
	if err != nil || string(ipv6) != "0\n" {
		return observed, errors.New("relay requires existing IPv6 forwarding disabled")
	}
	observed[1] = string(ipv6)
	return observed, nil
}

func newTCPRelay(config relayConfig, digest string, pid int, nonce [32]byte, forwarding [2]string) *tcpRelay {
	return &tcpRelay{config: config, connections: make(map[net.Conn]struct{}), status: relayStatus{
		Schema: 1, Protocol: relayProtocol, PID: pid, StartNonce: hex.EncodeToString(nonce[:]), ConfigSHA256: digest,
		IPv4Forwarding: forwarding[0], IPv6Forwarding: forwarding[1]}}
}

func relayIncrement(value *uint64) {
	if *value < math.MaxUint64 {
		*value = *value + 1
	}
}

// A raw TCP peer may end a stream with EOF, reset or a closed write side.
// These are connection lifecycle outcomes; RPC/workload gates retain authority
// over request success. This counter records other relay I/O failures only.
func relayUnexpectedCopy(err error) bool {
	return err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) &&
		!errors.Is(err, syscall.ECONNRESET) && !errors.Is(err, syscall.EPIPE)
}

func (relay *tcpRelay) snapshot() relayStatus {
	relay.mu.Lock()
	defer relay.mu.Unlock()
	return relay.status
}

func (relay *tcpRelay) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(relay.snapshot())
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		relay.mu.Lock()
		ready := relay.ready && !relay.stopping
		relay.mu.Unlock()
		if !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "ok\n")
	})
	return mux
}

func (relay *tcpRelay) admit(connection net.Conn) bool {
	relay.mu.Lock()
	defer relay.mu.Unlock()
	if relay.stopping || relay.status.Active >= uint64(relay.config.MaxConnections) {
		relayIncrement(&relay.status.Rejected)
		return false
	}
	relayIncrement(&relay.status.Accepted)
	relay.status.Active++
	relay.connections[connection] = struct{}{}
	relay.workers.Add(1)
	return true
}

func (relay *tcpRelay) forward(ctx context.Context, client net.Conn, route relayRoute) {
	var backend net.Conn
	defer func() {
		_ = client.Close()
		if backend != nil {
			_ = backend.Close()
		}
		relay.mu.Lock()
		delete(relay.connections, client)
		delete(relay.connections, backend)
		relay.status.Active--
		relayIncrement(&relay.status.Completed)
		relay.mu.Unlock()
		relay.workers.Done()
	}()
	var err error
	backend, err = (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp4", net.JoinHostPort(route.BackendIP, strconv.Itoa(route.BackendPort)))
	if err != nil {
		if ctx.Err() == nil {
			relay.mu.Lock()
			relayIncrement(&relay.status.DialFailure)
			relay.mu.Unlock()
		}
		return
	}
	relay.mu.Lock()
	if relay.stopping {
		relay.mu.Unlock()
		return
	}
	relay.connections[backend] = struct{}{}
	relay.mu.Unlock()
	results := make(chan error, 2)
	copyDirection := func(destination, source net.Conn) {
		_, copyError := io.Copy(destination, source)
		if copyError == nil {
			if tcp, ok := destination.(*net.TCPConn); ok {
				copyError = tcp.CloseWrite()
			}
		}
		if copyError != nil {
			_ = client.Close()
			_ = backend.Close()
		}
		results <- copyError
	}
	go copyDirection(backend, client)
	go copyDirection(client, backend)
	first, second := <-results, <-results
	if ctx.Err() == nil && (relayUnexpectedCopy(first) || relayUnexpectedCopy(second)) {
		relay.mu.Lock()
		relayIncrement(&relay.status.CopyFailure)
		relay.mu.Unlock()
	}
}

func (relay *tcpRelay) accept(ctx context.Context, listener net.Listener, route relayRoute) error {
	for {
		client, err := listener.Accept()
		if err != nil {
			return err
		}
		if !relay.admit(client) {
			_ = client.Close()
			continue
		}
		go relay.forward(ctx, client, route)
	}
}

func (relay *tcpRelay) serve(ctx context.Context, listeners []net.Listener, status net.Listener) error {
	if len(listeners) != len(relay.config.Routes) {
		return errors.New("relay listener cohort differs")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	server := &http.Server{Handler: relay.handler(), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 3 * time.Second,
		WriteTimeout: 3 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 4096}
	results := make(chan error, len(listeners)+1)
	relay.mu.Lock()
	relay.ready = true
	relay.mu.Unlock()
	for index, listener := range listeners {
		go func() { results <- relay.accept(ctx, listener, relay.config.Routes[index]) }()
	}
	go func() { results <- server.Serve(status) }()
	remaining := len(listeners) + 1
	var result error
	select {
	case <-ctx.Done():
	case result = <-results:
		remaining--
	}
	cancel()
	relay.mu.Lock()
	relay.stopping, relay.ready = true, false
	connections := make([]net.Conn, 0, len(relay.connections))
	for connection := range relay.connections {
		connections = append(connections, connection)
	}
	relay.mu.Unlock()
	for _, listener := range listeners {
		_ = listener.Close()
	}
	for _, connection := range connections {
		_ = connection.Close()
	}
	shutdown, stop := context.WithTimeout(context.Background(), 3*time.Second)
	if err := server.Shutdown(shutdown); err != nil {
		result = errors.Join(result, err)
		_ = server.Close()
	}
	stop()
	for range remaining {
		if err := <-results; err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, http.ErrServerClosed) {
			result = errors.Join(result, err)
		}
	}
	relay.workers.Wait()
	return result
}

func runTCPRelay(ctx context.Context, path string) error {
	config, digest, err := loadRelayConfig(path)
	if err != nil {
		return err
	}
	if os.Getpid() != 1 {
		return errors.New("relay requires its owned container PID1")
	}
	forwarding, err := relayForwarding(config, os.ReadFile)
	if err != nil {
		return err
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	relay := newTCPRelay(config, digest, os.Getpid(), nonce, forwarding)
	listeners := make([]net.Listener, 0, len(config.Routes))
	defer func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
	}()
	for _, route := range config.Routes {
		listener, err := net.Listen("tcp4", ":"+strconv.Itoa(route.ListenPort))
		if err != nil {
			return err
		}
		listeners = append(listeners, listener)
	}
	status, err := net.Listen("tcp4", ":8080")
	if err != nil {
		return fmt.Errorf("relay status listen: %w", err)
	}
	return relay.serve(ctx, listeners, status)
}
