package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func testRelayConfig(routes int) relayConfig {
	config := relayConfig{Schema: 1, Protocol: relayProtocol, MaxConnections: 4096}
	disabled := false
	config.FrontendIPv6, config.BackendIPv6 = &disabled, &disabled
	for index, port := range []int{6380, 6381, 6382, 9090, 9091, 9092, 9093}[:routes] {
		backend, backendPort := index%3+2, 6380
		if index >= 3 {
			backendPort = 9090
		}
		if index == 6 {
			backend = 5
		}
		config.Routes = append(config.Routes, relayRoute{ListenPort: port, BackendIP: "172.22.0." + string(rune('0'+backend)), BackendPort: backendPort})
	}
	return config
}

func TestRelayConfiguration(t *testing.T) {
	for _, routes := range []int{3, 7} {
		config := testRelayConfig(routes)
		if err := config.validate(); err != nil {
			t.Fatal(err)
		}
	}
	for name, change := range map[string]func(*relayConfig){
		"protocol":           func(c *relayConfig) { c.Protocol = "other" },
		"schema":             func(c *relayConfig) { c.Schema = 2 },
		"limit":              func(c *relayConfig) { c.MaxConnections = 4097 },
		"ipv6_missing":       func(c *relayConfig) { c.FrontendIPv6 = nil },
		"ipv6_enabled":       func(c *relayConfig) { enabled := true; c.BackendIPv6 = &enabled },
		"incomplete":         func(c *relayConfig) { c.Routes = c.Routes[:2] },
		"duplicate_port":     func(c *relayConfig) { c.Routes[1].ListenPort = c.Routes[0].ListenPort },
		"unexpected_port":    func(c *relayConfig) { c.Routes[0].ListenPort = 8080 },
		"backend_port":       func(c *relayConfig) { c.Routes[0].BackendPort = 123 },
		"dns":                func(c *relayConfig) { c.Routes[0].BackendIP = "lantern-0" },
		"public":             func(c *relayConfig) { c.Routes[0].BackendIP = "8.8.8.8" },
		"loopback":           func(c *relayConfig) { c.Routes[0].BackendIP = "127.0.0.1" },
		"link_local":         func(c *relayConfig) { c.Routes[0].BackendIP = "169.254.1.1" },
		"ipv6":               func(c *relayConfig) { c.Routes[0].BackendIP = "fd00::1" },
		"noncanonical":       func(c *relayConfig) { c.Routes[0].BackendIP = "172.022.0.2" },
		"shared_backend":     func(c *relayConfig) { c.Routes[1].BackendIP = c.Routes[0].BackendIP },
		"metrics_other_node": func(c *relayConfig) { c.Routes[3].BackendIP = c.Routes[1].BackendIP },
	} {
		t.Run(name, func(t *testing.T) {
			config := testRelayConfig(7)
			change(&config)
			if config.validate() == nil {
				t.Fatal("invalid relay route admitted")
			}
		})
	}
	t.Run("literal_file_hash_and_strict_json", func(t *testing.T) {
		directory, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(directory, "relay.json")
		raw, err := json.Marshal(testRelayConfig(3))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o400); err != nil {
			t.Fatal(err)
		}
		config, digest, err := loadRelayConfig(path)
		if err != nil || len(config.Routes) != 3 {
			t.Fatalf("config: %v", err)
		}
		sum := sha256.Sum256(raw)
		if digest != hex.EncodeToString(sum[:]) {
			t.Fatal("config byte hash differs")
		}
		for _, changed := range [][]byte{
			bytes.Replace(raw, []byte(`"schema":1`), []byte(`"schema":1,"schema":1`), 1),
			bytes.Replace(raw, []byte(`"schema":1`), []byte(`"schema":1,"extra":true`), 1),
			bytes.Replace(raw, []byte(`"schema":1`), []byte(`"Schema":1`), 1),
			bytes.Replace(raw, []byte(`"backend_ip"`), []byte(`"Backend_IP"`), 1),
			append(append([]byte{}, raw...), []byte(` {}`)...),
			bytes.Repeat([]byte(" "), (16<<10)+1),
		} {
			if err := os.Chmod(path, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, changed, 0o400); err != nil {
				t.Fatal(err)
			}
			if _, _, err := loadRelayConfig(path); err == nil {
				t.Fatal("noncanonical config accepted")
			}
		}
		alias := filepath.Join(directory, "alias.json")
		if err := os.Symlink(path, alias); err != nil {
			t.Fatal(err)
		}
		if _, _, err := loadRelayConfig(alias); err == nil {
			t.Fatal("symlink config admitted")
		}
	})
}

func TestRelayModeSelection(t *testing.T) {
	for _, arguments := range [][]string{
		{"-relay-config", ""},
		{"-relay-config", "/nonexistent/relay.json", "-listen", ":123"},
		{"-relay-config", "/nonexistent/relay.json", "-status", ":8080"},
		{"-relay-config", "/nonexistent/relay.json", "extra"},
	} {
		if err := run(t.Context(), arguments); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
			t.Fatalf("mixed relay/NTP mode did not refuse before socket construction: %v", err)
		}
	}
}

// Loopback backends and ephemeral listeners are injected directly only here;
// the actual config decoder rejects loopback and requires the fixed route set.
func startTestRelay(t *testing.T, backendPort, limit int) (*tcpRelay, string, context.CancelFunc, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	status, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	config := relayConfig{MaxConnections: limit, Routes: []relayRoute{{BackendIP: "127.0.0.1", BackendPort: backendPort}}}
	relay := newTCPRelay(config, strings.Repeat("a", 64), 17, [32]byte{1}, [2]string{"0\n", "0\n"})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- relay.serve(ctx, []net.Listener{listener}, status) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("relay shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("relay did not close/join owned workers")
		}
	})
	return relay, listener.Addr().String(), cancel, done
}

func TestRelayOpaqueStreamAndHalfClose(t *testing.T) {
	backend, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = backend.Close() }()
	payload := []byte{0x16, 0x03, 0x03, 0x00, 0xff, 0, 1, 2, 0x80}
	backendDone := make(chan error, 1)
	go func() {
		defer close(backendDone)
		connection, err := backend.Accept()
		if err != nil {
			backendDone <- err
			return
		}
		defer func() { _ = connection.Close() }()
		_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
		raw, err := io.ReadAll(connection)
		if err != nil {
			backendDone <- err
			return
		}
		if !bytes.Equal(raw, payload) {
			backendDone <- errors.New("opaque request bytes changed")
			return
		}
		_, err = connection.Write(append([]byte("after-client-half-close:"), raw...))
		backendDone <- err
	}()
	t.Cleanup(func() {
		_ = backend.Close()
		select {
		case <-backendDone:
		case <-time.After(5 * time.Second):
			t.Error("backend worker did not join")
		}
	})
	relay, address, _, _ := startTestRelay(t, backend.Addr().(*net.TCPAddr).Port, 4096)
	client, err := net.DialTCP("tcp4", nil, mustTCPAddress(t, address))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := client.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := client.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	response, err := io.ReadAll(client)
	if err != nil || !bytes.Equal(response, append([]byte("after-client-half-close:"), payload...)) {
		t.Fatalf("half-close response bytes: %x %v", response, err)
	}
	if err := <-backendDone; err != nil {
		t.Fatal(err)
	}
	waitRelay(t, func() bool { return relay.snapshot().Completed == 1 })
	status := relay.snapshot()
	if status.Active != 0 || status.Accepted != 1 || status.CopyFailure != 0 || status.DialFailure != 0 {
		t.Fatalf("relay completion counters: %+v", status)
	}
}

func mustTCPAddress(t *testing.T, address string) *net.TCPAddr {
	t.Helper()
	value, err := net.ResolveTCPAddr("tcp4", address)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func waitRelay(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("relay state did not settle within fixed test deadline")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRelayTLSBytesAndStatus(t *testing.T) {
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) { _, _ = io.WriteString(w, "opaque TLS accepted\n") }))
	defer backend.Close()
	port := backend.Listener.Addr().(*net.TCPAddr).Port
	relay, address, _, _ := startTestRelay(t, port, 4096)
	client := backend.Client()
	client.Timeout = 3 * time.Second
	defer client.CloseIdleConnections()
	response, err := client.Get("https://" + address + "/unchanged")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1024))
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || string(raw) != "opaque TLS accepted\n" {
		t.Fatalf("TLS passthrough failed: %q %v", raw, err)
	}
	output := httptest.NewRecorder()
	relay.handler().ServeHTTP(output, httptest.NewRequest(http.MethodGet, "/status", nil))
	var fields map[string]json.RawMessage
	if json.Unmarshal(output.Body.Bytes(), &fields) != nil || len(fields) != 13 || fields["seed_utc"] != nil || fields["clock_kind"] != nil {
		t.Fatal("relay status constructed a time owner or changed schema")
	}
	var status relayStatus
	if json.Unmarshal(output.Body.Bytes(), &status) != nil || status.Protocol != relayProtocol || status.PID != 17 || status.ConfigSHA256 != strings.Repeat("a", 64) || len(status.StartNonce) != 64 {
		t.Fatal("relay process/config identity absent")
	}
}

func TestRelayAdmissionCancellationAndDialFailure(t *testing.T) {
	backend, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = backend.Close() }()
	relay, address, cancel, _ := startTestRelay(t, backend.Addr().(*net.TCPAddr).Port, 1)
	first, err := net.Dial("tcp4", address)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	peer, err := backend.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = peer.Close() }()
	second, err := net.Dial("tcp4", address)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	_ = second.SetReadDeadline(time.Now().Add(3 * time.Second))
	var value [1]byte
	if _, err := second.Read(value[:]); err == nil {
		t.Fatal("over-limit relay client remained admitted")
	}
	waitRelay(t, func() bool { return relay.snapshot().Rejected == 1 })
	cancel()
	waitRelay(t, func() bool { return relay.snapshot().Active == 0 })
	if relay.snapshot().CopyFailure != 0 {
		t.Fatal("owned cancellation counted as unexpected copy failure")
	}
	failed, failedAddress, _, _ := startTestRelay(t, 0, 1)
	connection, err := net.Dial("tcp4", failedAddress)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	waitRelay(t, func() bool { return failed.snapshot().Completed == 1 })
	if failed.snapshot().DialFailure != 1 || failed.snapshot().Active != 0 {
		t.Fatal("backend dial failure was hidden")
	}
}

func TestRelayCopyOutcomeClassification(t *testing.T) {
	for _, err := range []error{nil, io.EOF, net.ErrClosed, syscall.ECONNRESET, syscall.EPIPE, &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}} {
		if relayUnexpectedCopy(err) {
			t.Fatalf("connection lifecycle close became relay failure: %v", err)
		}
	}
	if !relayUnexpectedCopy(errors.New("other copy failure")) {
		t.Fatal("unexpected relay I/O failure hidden")
	}
}

func TestRelayForwardingReadOnlyGuard(t *testing.T) {
	for _, tc := range []struct {
		name, ipv4, ipv6  string
		readError         error
		ipv6Enabled, pass bool
	}{
		{name: "disabled", ipv4: "0\n", ipv6: "0\n", pass: true},
		{name: "ipv6_absent_disabled_networks", ipv4: "0\n", readError: os.ErrNotExist, pass: true},
		{name: "ipv4_enabled", ipv4: "1\n", ipv6: "0\n"},
		{name: "ipv4_no_newline", ipv4: "0", ipv6: "0\n"},
		{name: "ipv6_enabled", ipv4: "0\n", ipv6: "1\n"},
		{name: "ipv6_unreadable", ipv4: "0\n", readError: os.ErrPermission},
		{name: "ipv6_absent_enabled_network", ipv4: "0\n", readError: os.ErrNotExist, ipv6Enabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := testRelayConfig(3)
			config.FrontendIPv6 = &tc.ipv6Enabled
			var reads []string
			observed, err := relayForwarding(config, func(path string) ([]byte, error) {
				reads = append(reads, path)
				if path == "/proc/sys/net/ipv4/ip_forward" {
					return []byte(tc.ipv4), nil
				}
				if path != "/proc/sys/net/ipv6/conf/all/forwarding" {
					t.Fatalf("unexpected namespace read %q", path)
				}
				return []byte(tc.ipv6), tc.readError
			})
			if (err == nil) != tc.pass {
				t.Fatalf("forwarding guard result: %v", err)
			}
			if tc.pass && (observed[0] != "0\n" || observed[1] != "0\n" && observed[1] != "absent") {
				t.Fatal("actual read values not retained")
			}
			if len(reads) > 2 {
				t.Fatal("forwarding guard retried instead of refusing")
			}
		})
	}
}
