package main

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func serveReplica(t *testing.T, dir, service string) (*httptest.Server, string) {
	t.Helper()
	cert, err := tls.LoadX509KeyPair(filepath.Join(dir, service, "server.pem"),
		filepath.Join(dir, service, "server.key"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	return srv, port
}

func TestGenerateAndVerifyAuthenticatedReceiptTopology(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := generate(dir); err != nil {
		t.Fatal(err)
	}
	ports := make([]string, len(replicas))
	for i, service := range replicas {
		_, ports[i] = serveReplica(t, dir, service)
	}
	pre, err := verify(dir, ports, nil)
	if err != nil || len(pre.Peers) != 3 || pre.CASHA256 == "" {
		t.Fatalf("TLS preflight = (%+v, %v)", pre, err)
	}
	for i := range pre.Peers {
		if pre.Peers[i].Service != replicas[i] || pre.Peers[i].Port != ports[i] ||
			pre.Peers[i].CertificateSHA256 == "" {
			t.Fatalf("wrong replica certificate proof: %+v", pre.Peers[i])
		}
	}
	if _, err := verify(dir, ports, &pre); err != nil {
		t.Fatalf("unchanged postflight: %v", err)
	}
	changed := pre
	changed.CASHA256 = "unexpected"
	if _, err := verify(dir, ports, &changed); err == nil ||
		!strings.Contains(err.Error(), "changed since preflight") {
		t.Fatalf("changed baseline = %v", err)
	}
	t.Run("peer trust root drift", func(t *testing.T) {
		peerCA := filepath.Join(dir, replicas[0], "ca.pem")
		good, err := os.ReadFile(peerCA)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(peerCA, []byte("untrusted"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := verify(dir, ports, nil); err == nil || !strings.Contains(err.Error(), "peer CA differs") {
			t.Errorf("changed peer trust root = %v", err)
		}
		if err := os.WriteFile(peerCA, good, 0o644); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("wrong published certificate", func(t *testing.T) {
		otherDir := filepath.Join(t.TempDir(), "other")
		if err := os.Mkdir(otherDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := generate(otherDir); err != nil {
			t.Fatal(err)
		}
		_, wrongPort := serveReplica(t, otherDir, replicas[0])
		wrongPorts := append([]string(nil), ports...)
		wrongPorts[0] = wrongPort
		if _, err := verify(dir, wrongPorts, nil); err == nil ||
			!strings.Contains(err.Error(), "published TLS identity") {
			t.Errorf("untrusted published certificate = %v", err)
		}
	})
	t.Run("plaintext published port", func(t *testing.T) {
		plain := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		t.Cleanup(plain.Close)
		_, plainPort, err := net.SplitHostPort(strings.TrimPrefix(plain.URL, "http://"))
		if err != nil {
			t.Fatal(err)
		}
		plainPorts := append([]string(nil), ports...)
		plainPorts[0] = plainPort
		if _, err := verify(dir, plainPorts, nil); err == nil ||
			!strings.Contains(err.Error(), "published TLS identity") {
			t.Errorf("plaintext published port = %v", err)
		}
	})
	t.Run("invalid private directory", func(t *testing.T) {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chmod(dir, 0o700) }()
		if _, err := verify(dir, ports, nil); err == nil ||
			!strings.Contains(err.Error(), "mode 0700") {
			t.Errorf("unsafe directory = %v", err)
		}
	})
}

func TestGenerateRejectsNonemptyOrUnprotectedDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "existing"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := generate(dir); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("nonempty directory = %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "existing")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := generate(dir); err == nil || !strings.Contains(err.Error(), "0700") {
		t.Fatalf("unprotected directory = %v", err)
	}
}
