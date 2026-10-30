// Command ntpfixture serves a private benchmark-only NTP source. It never queries
// another clock: UTC is seeded once, then advances only with monotonic elapsed time.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	fixtureProtocol      = "lantern_bench_ntp_fixture_v1"
	clockKind            = "seed_utc_plus_monotonic"
	ntpEpochOffset       = int64(2_208_988_800)
	precisionLog2Seconds = -20
	rootDispersion       = uint32(66) // Unsigned 16.16 seconds: about 1.007 ms.
)

var errClock = errors.New("fixture monotonic clock failed")

type fixture struct {
	seed         time.Time
	origin       time.Time
	since        func(time.Time) time.Duration
	pid          int
	startNonce   string
	clockMu      sync.Mutex
	lastElapsed  time.Duration
	failed       bool
	clockFailed  chan struct{}
	started      chan struct{}
	ready        atomic.Bool
	requests     atomic.Uint64
	responses    atomic.Uint64
	invalid      atomic.Uint64
	clockFailure atomic.Uint64
}

type fixtureStatus struct {
	Schema       int    `json:"schema"`
	Protocol     string `json:"protocol"`
	PID          int    `json:"pid"`
	StartNonce   string `json:"start_nonce"`
	ClockKind    string `json:"clock_kind"`
	SeedUTC      string `json:"seed_utc"`
	ElapsedNS    uint64 `json:"elapsed_ns"`
	Requests     uint64 `json:"requests"`
	Responses    uint64 `json:"responses"`
	Invalid      uint64 `json:"invalid"`
	ClockFailure uint64 `json:"clock_failure"`
}

func newFixture(seed time.Time, since func(time.Time) time.Duration, pid int, nonce [32]byte) *fixture {
	return &fixture{seed: seed.UTC(), origin: seed, since: since, pid: pid,
		startNonce: hex.EncodeToString(nonce[:]), clockFailed: make(chan struct{}), started: make(chan struct{})}
}

// Counters saturate instead of wrapping. They count UDP datagrams, not HTTP reads.
func increment(counter *atomic.Uint64) {
	for {
		previous := counter.Load()
		if previous == math.MaxUint64 || counter.CompareAndSwap(previous, previous+1) {
			return
		}
	}
}

func (f *fixture) sample() (time.Time, uint64, error) {
	f.clockMu.Lock()
	defer f.clockMu.Unlock()
	if f.failed {
		return time.Time{}, uint64(f.lastElapsed), errClock
	}
	elapsed := f.since(f.origin)
	stamp := f.seed.Add(elapsed)
	if elapsed < 0 || elapsed < f.lastElapsed || stamp.Before(f.seed) {
		f.failed = true
		increment(&f.clockFailure)
		close(f.clockFailed)
		return time.Time{}, uint64(f.lastElapsed), errClock
	}
	f.lastElapsed = elapsed
	return stamp, uint64(elapsed), nil
}

func timestamp(destination []byte, stamp time.Time) {
	// The 32-bit seconds field deliberately follows the NTP era rollover.
	binary.BigEndian.PutUint32(destination[:4], uint32(stamp.Unix()+ntpEpochOffset))
	binary.BigEndian.PutUint32(destination[4:8], uint32((uint64(stamp.Nanosecond())<<32)/1_000_000_000))
}

func (f *fixture) reply(request []byte) ([48]byte, bool, error) {
	increment(&f.requests)
	var response [48]byte
	if len(request) != len(response) || request[0] != 4<<3|3 || bytes.Equal(request[40:48], make([]byte, 8)) {
		increment(&f.invalid)
		return response, false, nil
	}
	received, _, err := f.sample()
	if err != nil {
		return response, false, err
	}
	response[0], response[1], response[2] = 4<<3|4, 1, 4
	response[3] = byte(256 + precisionLog2Seconds)
	// Root delay is zero; no upstream/reference network is involved.
	binary.BigEndian.PutUint32(response[8:12], rootDispersion)
	copy(response[12:16], "LOCL")
	timestamp(response[16:24], f.seed)
	copy(response[24:32], request[40:48])
	timestamp(response[32:40], received)
	transmitted, _, err := f.sample()
	if err != nil {
		return response, false, err
	}
	timestamp(response[40:48], transmitted)
	return response, true, nil
}

func (f *fixture) status() fixtureStatus {
	_, elapsed, _ := f.sample()
	// Outcomes are counted after their request. Read them first so a request
	// arriving during this observation cannot make an outcome exceed Requests.
	// These are ordered observations, not one atomic counter snapshot.
	responses, invalid := f.responses.Load(), f.invalid.Load()
	requests := f.requests.Load()
	return fixtureStatus{Schema: 1, Protocol: fixtureProtocol, PID: f.pid,
		StartNonce: f.startNonce, ClockKind: clockKind, SeedUTC: f.seed.Format(time.RFC3339Nano),
		ElapsedNS: elapsed, Requests: requests, Responses: responses,
		Invalid: invalid, ClockFailure: f.clockFailure.Load()}
}

func (f *fixture) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(f.status())
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		_, _, err := f.sample()
		if !f.ready.Load() || err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, "unhealthy\n")
			return
		}
		_, _ = io.WriteString(w, "ok\n")
	})
	return mux
}

func (f *fixture) serveUDP(ctx context.Context, connection net.PacketConn) error {
	// One extra byte detects oversized datagrams; no per-datagram goroutine or
	// unbounded allocation is needed. Longer truncated packets remain invalid.
	var packet [49]byte
	for {
		n, peer, err := connection.ReadFrom(packet[:])
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		response, valid, err := f.reply(packet[:n])
		if err != nil {
			return err
		}
		if !valid {
			continue
		}
		written, err := connection.WriteTo(response[:], peer)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		if written != len(response) {
			return io.ErrShortWrite
		}
		increment(&f.responses)
	}
}

func (f *fixture) serve(ctx context.Context, packet net.PacketConn, listener net.Listener) error {
	server := &http.Server{Handler: f.handler(), ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 4096}
	results := make(chan error, 2)
	f.ready.Store(true)
	close(f.started)
	go func() { results <- f.serveUDP(ctx, packet) }()
	go func() { results <- server.Serve(listener) }()
	var result error
	remaining := 2
	select {
	case <-ctx.Done():
	case <-f.clockFailed:
		result = errClock
	case result = <-results:
		remaining--
	}
	f.ready.Store(false)
	_ = packet.Close()
	shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	if err := server.Shutdown(shutdown); err != nil {
		result = errors.Join(result, err)
		_ = server.Close()
	}
	cancel()
	for range remaining {
		err := <-results
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			result = errors.Join(result, err)
		}
	}
	return result
}

func listenAddress(value string) bool {
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return false
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 0 || number > 65535 || strconv.Itoa(number) != port {
		return false
	}
	if host == "" {
		return true
	}
	_, err = netip.ParseAddr(host)
	return err == nil
}

func run(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("ntpfixture", flag.ContinueOnError)
	udpAddress := flags.String("listen", ":123", "UDP listen address (literal IP or wildcard)")
	httpAddress := flags.String("status", ":8080", "HTTP status listen address (literal IP or wildcard)")
	relayConfig := flags.String("relay-config", "", "read-only fixed TCP relay configuration; exclusive of NTP flags")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	relayMode, exclusive := false, flags.NArg() == 0
	flags.Visit(func(option *flag.Flag) {
		if option.Name == "relay-config" {
			relayMode = true
		} else {
			exclusive = false
		}
	})
	if relayMode {
		if *relayConfig == "" || !exclusive {
			return errors.New("relay configuration cannot be combined with NTP flags or positional arguments")
		}
		return runTCPRelay(ctx, *relayConfig)
	}
	if flags.NArg() != 0 || !listenAddress(*udpAddress) || !listenAddress(*httpAddress) {
		return errors.New("literal listen addresses and no positional arguments required")
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	// This is the only UTC seed. time.Since uses the retained monotonic origin.
	f := newFixture(time.Now(), time.Since, os.Getpid(), nonce)
	packet, err := net.ListenPacket("udp", *udpAddress)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", *httpAddress)
	if err != nil {
		return errors.Join(err, packet.Close())
	}
	return f.serve(ctx, packet, listener)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "ntpfixture:", err)
		os.Exit(1)
	}
}
