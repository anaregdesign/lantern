package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

func testFixture(elapsed *atomic.Int64) *fixture {
	seed := time.Date(2026, 10, 30, 4, 0, 0, 123_456_789, time.UTC)
	return newFixture(seed, func(time.Time) time.Duration { return time.Duration(elapsed.Load()) }, 17, [32]byte{1})
}

func clientPacket() []byte {
	request := make([]byte, 48)
	request[0] = 4<<3 | 3
	copy(request[40:48], "nonce001")
	return request
}

func TestFixturePacketContract(t *testing.T) {
	var elapsed atomic.Int64
	f := testFixture(&elapsed)
	request := clientPacket()
	before := slices.Clone(request)
	elapsed.Store(int64(1250 * time.Millisecond))
	response, valid, err := f.reply(request)
	if err != nil || !valid || !bytes.Equal(request, before) {
		t.Fatalf("valid request or input ownership: valid=%v err=%v", valid, err)
	}
	if response[0] != 4<<3|4 || response[1] != 1 || response[2] != 4 || int8(response[3]) != precisionLog2Seconds {
		t.Fatalf("unexpected NTP header %x", response[:4])
	}
	if binary.BigEndian.Uint32(response[4:8]) != 0 || binary.BigEndian.Uint32(response[8:12]) != rootDispersion || string(response[12:16]) != "LOCL" {
		t.Fatal("root delay, dispersion or local reference differs")
	}
	if !bytes.Equal(response[24:32], request[40:48]) {
		t.Fatal("origin timestamp did not echo the exact request nonce")
	}
	var reference, current [8]byte
	timestamp(reference[:], f.seed)
	timestamp(current[:], f.seed.Add(1250*time.Millisecond))
	if !bytes.Equal(response[16:24], reference[:]) || !bytes.Equal(response[32:40], current[:]) || !bytes.Equal(response[40:48], current[:]) {
		t.Fatal("reference/receive/transmit timestamps do not use the seeded monotonic clock")
	}
	for _, tc := range []struct {
		name   string
		packet []byte
	}{
		{"short", request[:47]},
		{"oversized", append(slices.Clone(request), 0)},
		{"v3", append([]byte{3<<3 | 3}, request[1:]...)},
		{"server_mode", append([]byte{4<<3 | 4}, request[1:]...)},
		{"leap_alarm", append([]byte{3<<6 | 4<<3 | 3}, request[1:]...)},
		{"zero_nonce", append(slices.Clone(request[:40]), make([]byte, 8)...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, valid, err := f.reply(tc.packet); valid || err != nil {
				t.Fatalf("invalid client request was not rejected: valid=%v err=%v", valid, err)
			}
		})
	}
	status := f.status()
	if status.Requests != 7 || status.Invalid != 6 || status.Responses != 0 || status.ClockFailure != 0 {
		t.Fatalf("datagram counters differ: %+v", status)
	}
}

func TestFixtureMonotonicUTC(t *testing.T) {
	t.Run("seed_and_elapsed", func(t *testing.T) {
		var elapsed atomic.Int64
		f := testFixture(&elapsed)
		for _, value := range []time.Duration{0, time.Second, time.Second, 3 * time.Hour} {
			elapsed.Store(int64(value))
			stamp, observed, err := f.sample()
			if err != nil || observed != uint64(value) || !stamp.Equal(f.seed.Add(value)) || stamp.Location() != time.UTC {
				t.Fatalf("monotonic UTC sample differs: %v %v %v", stamp, observed, err)
			}
		}
		if f.seed.Format(time.RFC3339Nano) != "2026-10-30T04:00:00.123456789Z" {
			t.Fatal("UTC seed changed after elapsed samples")
		}
	})
	t.Run("regression_is_sticky", func(t *testing.T) {
		var elapsed atomic.Int64
		f := testFixture(&elapsed)
		elapsed.Store(int64(time.Second))
		if _, _, err := f.sample(); err != nil {
			t.Fatal(err)
		}
		elapsed.Store(int64(time.Second / 2))
		if _, valid, err := f.reply(clientPacket()); valid || !errors.Is(err, errClock) {
			t.Fatalf("regression did not fail closed: %v %v", valid, err)
		}
		elapsed.Store(int64(2 * time.Second))
		if _, valid, err := f.reply(clientPacket()); valid || !errors.Is(err, errClock) {
			t.Fatal("clock recovered without a new fixture process")
		}
		if f.clockFailure.Load() != 1 || f.status().ElapsedNS != uint64(time.Second) {
			t.Fatal("sticky clock failure lost the last valid elapsed observation")
		}
	})
	t.Run("negative_elapsed", func(t *testing.T) {
		var elapsed atomic.Int64
		elapsed.Store(-1)
		f := testFixture(&elapsed)
		if _, _, err := f.sample(); !errors.Is(err, errClock) || f.clockFailure.Load() != 1 {
			t.Fatal("negative monotonic elapsed was accepted")
		}
	})
	t.Run("ntp_era", func(t *testing.T) {
		stamp := time.Date(2050, 1, 1, 0, 0, 0, 500_000_000, time.UTC)
		var encoded [8]byte
		timestamp(encoded[:], stamp)
		if binary.BigEndian.Uint32(encoded[:4]) != uint32(stamp.Unix()+ntpEpochOffset) || binary.BigEndian.Uint32(encoded[4:]) != 1<<31 {
			t.Fatal("NTP era rollover or fractional timestamp differs")
		}
	})
}

func TestFixtureStatusAndHealth(t *testing.T) {
	t.Run("concurrent_counter_observations", func(t *testing.T) {
		var elapsed atomic.Int64
		f := testFixture(&elapsed)
		stop, done, started := make(chan struct{}), make(chan struct{}), make(chan struct{})
		go func() {
			defer close(done)
			// Match the UDP worker's causal order: every outcome follows its
			// request, and each datagram has at most one outcome.
			increment(&f.requests)
			increment(&f.responses)
			close(started)
			for index := 0; ; index++ {
				select {
				case <-stop:
					return
				default:
				}
				increment(&f.requests)
				if index%2 == 0 {
					increment(&f.responses)
				} else {
					increment(&f.invalid)
				}
			}
		}()
		t.Cleanup(func() {
			close(stop)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Error("counter producer was not joined")
			}
		})
		<-started
		var previous fixtureStatus
		for index := range 20_000 {
			if index%64 == 0 {
				runtime.Gosched()
			}
			status := f.status()
			if status.Responses > status.Requests || status.Invalid > status.Requests || status.Responses+status.Invalid > status.Requests {
				t.Fatalf("status exposes an outcome without its request: %+v", status)
			}
			if status.Requests < previous.Requests || status.Responses < previous.Responses || status.Invalid < previous.Invalid {
				t.Fatal("counter observations moved backward")
			}
			previous = status
		}
		if previous.Requests <= 1 {
			t.Fatal("counter producer did not overlap status observations")
		}
	})
	var elapsed atomic.Int64
	f := testFixture(&elapsed)
	handler := f.handler()
	request := func(method, path string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, path, nil))
		return response
	}
	if request(http.MethodGet, "/healthz").Code != http.StatusServiceUnavailable {
		t.Fatal("unstarted fixture reported healthy")
	}
	f.ready.Store(true)
	if response := request(http.MethodGet, "/healthz"); response.Code != http.StatusOK || response.Body.String() != "ok\n" {
		t.Fatal("ready fixture is not healthy")
	}
	response := request(http.MethodGet, "/status")
	var fields map[string]json.RawMessage
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &fields) != nil {
		t.Fatal("status is not bounded JSON")
	}
	wanted := []string{"clock_failure", "clock_kind", "elapsed_ns", "invalid", "pid", "protocol", "requests", "responses", "schema", "seed_utc", "start_nonce"}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	if !reflect.DeepEqual(keys, wanted) || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status schema/cache policy changed: %v", keys)
	}
	var status fixtureStatus
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Schema != 1 || status.Protocol != fixtureProtocol || status.PID != 17 || len(status.StartNonce) != 64 || status.ClockKind != clockKind || status.SeedUTC != f.seed.Format(time.RFC3339Nano) {
		t.Fatalf("process/clock identity differs: %+v", status)
	}
	for _, path := range []string{"/status", "/healthz"} {
		if request(http.MethodPost, path).Code != http.StatusMethodNotAllowed {
			t.Fatal("mutating HTTP method accepted")
		}
	}
	elapsed.Store(-1)
	if request(http.MethodGet, "/healthz").Code != http.StatusServiceUnavailable {
		t.Fatal("failed clock reported healthy")
	}
	var failed fixtureStatus
	if err := json.Unmarshal(request(http.MethodGet, "/status").Body.Bytes(), &failed); err != nil || failed.ClockFailure != 1 {
		t.Fatal("clock failure not visible in status")
	}
}

func TestFixtureServeCancellation(t *testing.T) {
	for _, clockFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "clock_failure"}[clockFailure], func(t *testing.T) {
			var elapsed atomic.Int64
			f := testFixture(&elapsed)
			packet, err := net.ListenPacket("udp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				_ = packet.Close()
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() { defer close(done); done <- f.serve(ctx, packet, listener) }()
			t.Cleanup(func() {
				cancel()
				_ = packet.Close()
				_ = listener.Close()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("fixture workers were not joined")
				}
			})
			select {
			case <-f.started:
			case <-time.After(time.Second):
				t.Fatal("fixture did not start")
			}
			connection, err := net.Dial("udp", packet.LocalAddr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = connection.Close() }()
			if err := connection.SetDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			request := clientPacket()
			if _, err := connection.Write(request); err != nil {
				t.Fatal(err)
			}
			var reply [49]byte
			n, err := connection.Read(reply[:])
			if err != nil || n != 48 || !bytes.Equal(reply[24:32], request[40:48]) {
				t.Fatalf("UDP wire reply differs: n=%d err=%v", n, err)
			}
			client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
			defer client.CloseIdleConnections()
			response, err := client.Get("http://" + listener.Addr().String() + "/status")
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(io.LimitReader(response.Body, 4097))
			_ = response.Body.Close()
			if err != nil || response.StatusCode != http.StatusOK || len(body) > 4096 {
				t.Fatal("HTTP status wire contract differs")
			}
			if clockFailure {
				elapsed.Store(-1)
				_, _, _ = f.sample()
			} else {
				cancel()
			}
			select {
			case err := <-done:
				if clockFailure != errors.Is(err, errClock) || !clockFailure && err != nil {
					t.Fatalf("fixture shutdown result differs: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("cancel/clock failure did not close and join both listeners")
			}
			if f.ready.Load() || f.requests.Load() != 1 || f.responses.Load() != 1 {
				t.Fatal("shutdown lost state or final successful response count")
			}
			if _, _, err := packet.ReadFrom(reply[:]); !errors.Is(err, net.ErrClosed) {
				t.Fatal("UDP socket remains open")
			}
			if _, err := listener.Accept(); !errors.Is(err, net.ErrClosed) {
				t.Fatal("HTTP listener remains open")
			}
		})
	}
}

func TestFixtureCounterSaturation(t *testing.T) {
	var counter atomic.Uint64
	counter.Store(math.MaxUint64 - 1)
	increment(&counter)
	increment(&counter)
	if counter.Load() != math.MaxUint64 {
		t.Fatal("counter wrapped")
	}
}

func TestFixtureListenAddress(t *testing.T) {
	for _, value := range []string{":123", ":8080", "127.0.0.1:0", "[::1]:123"} {
		if !listenAddress(value) {
			t.Fatalf("literal address refused: %s", value)
		}
	}
	for _, value := range []string{"fixture.invalid:123", "127.0.0.1:0123", "127.0.0.1:-1", "127.0.0.1:65536", "123", ""} {
		if listenAddress(value) {
			t.Fatalf("hostname/malformed address accepted: %s", value)
		}
	}
	if err := run(t.Context(), []string{"-listen", "fixture.invalid:123"}); err == nil {
		t.Fatal("run accepted a DNS listen lookup")
	}
}
