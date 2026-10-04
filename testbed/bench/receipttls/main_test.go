package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func serial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, err
	}
	return n.Add(n, big.NewInt(1)), nil
}

func certificatePEM(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func generate(dir string) error {
	stat, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("stat private TLS directory: %w", err)
	}
	if !stat.IsDir() || stat.Mode().Perm() != 0o700 {
		return errors.New("private TLS directory must exist with mode 0700")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		return errors.New("private TLS directory must be empty")
	}
	now := time.Now()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	caSerial, err := serial()
	if err != nil {
		return err
	}
	caTemplate := &x509.Certificate{
		SerialNumber: caSerial, Subject: pkix.Name{CommonName: "Lantern receipt benchmark CA"},
		NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true, IsCA: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	caPEM := certificatePEM(caDER)
	if err := writeMountedFile(filepath.Join(dir, "ca.pem"), caPEM); err != nil {
		return err
	}
	for _, service := range replicas {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return err
		}
		number, err := serial()
		if err != nil {
			return err
		}
		leaf := &x509.Certificate{
			SerialNumber: number, Subject: pkix.Name{CommonName: service},
			NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(24 * time.Hour),
			KeyUsage:              x509.KeyUsageDigitalSignature,
			ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			BasicConstraintsValid: true,
			DNSNames:              []string{"localhost", service},
			IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		}
		der, err := x509.CreateCertificate(rand.Reader, leaf, caTemplate, &key.PublicKey, caKey)
		if err != nil {
			return err
		}
		keyDER, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			return err
		}
		replicaDir := filepath.Join(dir, service)
		if err := os.Mkdir(replicaDir, 0o755); err != nil {
			return err
		}
		if err := os.Chmod(replicaDir, 0o755); err != nil {
			return err
		}
		// The outer directory is host-private. Only this replica's leaf
		// key is mounted inside its non-root container, read-only.
		keyFile := filepath.Join(replicaDir, "server.key")
		if err := writeMountedFile(keyFile,
			pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})); err != nil {
			return err
		}
		if err := writeMountedFile(filepath.Join(replicaDir, "server.pem"), certificatePEM(der)); err != nil {
			return err
		}
		if err := writeMountedFile(filepath.Join(replicaDir, "ca.pem"), caPEM); err != nil {
			return err
		}
	}
	return nil
}

func writeMountedFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	return os.Chmod(path, 0o644)
}
