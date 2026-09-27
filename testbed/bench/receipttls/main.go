// Command receipttls creates short-lived, per-replica benchmark certificates
// and verifies the live HTTPS/HTTP2 identity before and after a receipt run.
package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"
)

var replicas = [...]string{"lantern-0", "lantern-1", "lantern-2"}

type peerProof struct {
	Service           string `json:"service"`
	Port              string `json:"port"`
	CertificateSHA256 string `json:"certificate_sha256"`
}

type tlsProof struct {
	CASHA256 string      `json:"ca_sha256"`
	Peers    []peerProof `json:"peers"`
}

func main() {
	if len(os.Args) < 2 {
		fatal(errors.New("expected generate or verify"))
	}
	command := os.Args[1]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	dir := flags.String("dir", "", "private directory for the ephemeral peer certificates")
	ports := flags.String("ports", "", "three published TLS ports, ordered by replica")
	out := flags.String("out", "", "public certificate-fingerprint report")
	baseline := flags.String("baseline", "", "pre-run report to compare with post-run identity")
	if err := flags.Parse(os.Args[2:]); err != nil {
		fatal(err)
	}
	if *dir == "" || flags.NArg() != 0 {
		fatal(errors.New("-dir is required; positional arguments are not accepted"))
	}
	switch command {
	case "generate":
		if *ports != "" || *out != "" || *baseline != "" {
			fatal(errors.New("generate accepts only -dir"))
		}
		fatal(generate(*dir))
	case "verify":
		if *ports == "" || *out == "" {
			fatal(errors.New("verify requires -ports and -out"))
		}
		var expected *tlsProof
		if *baseline != "" {
			data, err := os.ReadFile(*baseline)
			if err != nil {
				fatal(fmt.Errorf("read baseline: %w", err))
			}
			var proof tlsProof
			if err := json.Unmarshal(data, &proof); err != nil {
				fatal(fmt.Errorf("parse baseline: %w", err))
			}
			expected = &proof
		}
		proof, err := verify(*dir, strings.Split(*ports, ","), expected)
		if err != nil {
			fatal(err)
		}
		data, err := json.MarshalIndent(proof, "", "  ")
		if err != nil {
			fatal(err)
		}
		data = append(data, '\n')
		fatal(os.WriteFile(*out, data, 0o644))
	default:
		fatal(fmt.Errorf("unknown command %q", command))
	}
}

func fatal(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "receipttls: %v\n", err)
		os.Exit(1)
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
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return err
	}
	tokenPath := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenPath, []byte(hex.EncodeToString(token)), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tokenPath, 0o600); err != nil {
		return err
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
			DNSNames:              []string{"lantern", "localhost", service},
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

func verify(dir string, ports []string, baseline *tlsProof) (tlsProof, error) {
	if len(ports) != len(replicas) {
		return tlsProof{}, errors.New("exactly three published TLS ports are required")
	}
	stat, err := os.Stat(dir)
	if err != nil || !stat.IsDir() || stat.Mode().Perm() != 0o700 {
		return tlsProof{}, errors.New("private TLS directory is missing or not mode 0700")
	}
	caPEM, err := os.ReadFile(filepath.Join(dir, "ca.pem"))
	if err != nil {
		return tlsProof{}, err
	}
	block, rest := pem.Decode(caPEM)
	if block == nil || block.Type != "CERTIFICATE" || len(rest) != 0 {
		return tlsProof{}, errors.New("invalid benchmark CA PEM")
	}
	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil || !ca.IsCA || ca.CheckSignatureFrom(ca) != nil ||
		time.Until(ca.NotAfter) < time.Hour {
		return tlsProof{}, errors.New("invalid or expiring benchmark CA")
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	proof := tlsProof{CASHA256: fingerprint(ca.Raw), Peers: make([]peerProof, 0, len(replicas))}
	for i, service := range replicas {
		port, err := strconv.Atoi(ports[i])
		if err != nil || port < 1 || port > 65535 || strconv.Itoa(port) != ports[i] {
			return tlsProof{}, fmt.Errorf("%s has invalid published port %q", service, ports[i])
		}
		replicaCA, err := os.ReadFile(filepath.Join(dir, service, "ca.pem"))
		if err != nil || !bytes.Equal(replicaCA, caPEM) {
			return tlsProof{}, fmt.Errorf("%s peer CA differs from pinned benchmark CA", service)
		}
		cert, err := tls.LoadX509KeyPair(filepath.Join(dir, service, "server.pem"),
			filepath.Join(dir, service, "server.key"))
		if err != nil {
			return tlsProof{}, fmt.Errorf("%s certificate/key: %w", service, err)
		}
		leaf, err := x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			return tlsProof{}, err
		}
		if _, err := leaf.Verify(x509.VerifyOptions{
			Roots: roots, DNSName: "lantern",
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}); err != nil {
			return tlsProof{}, fmt.Errorf("%s peer DNS identity: %w", service, err)
		}
		for _, name := range []string{"localhost", "127.0.0.1", service} {
			if err := leaf.VerifyHostname(name); err != nil {
				return tlsProof{}, fmt.Errorf("%s client identity %s: %w", service, name, err)
			}
		}
		dialer := &net.Dialer{Timeout: 3 * time.Second}
		conn, err := tls.DialWithDialer(dialer, "tcp",
			net.JoinHostPort("127.0.0.1", ports[i]),
			&tls.Config{
				MinVersion: tls.VersionTLS12, RootCAs: roots,
				ServerName: "localhost", NextProtos: []string{"h2"},
			},
		)
		if err != nil {
			return tlsProof{}, fmt.Errorf("%s published TLS identity: %w", service, err)
		}
		state := conn.ConnectionState()
		if err := conn.Close(); err != nil {
			return tlsProof{}, err
		}
		if state.NegotiatedProtocol != "h2" || len(state.PeerCertificates) == 0 ||
			fingerprint(state.PeerCertificates[0].Raw) != fingerprint(leaf.Raw) {
			return tlsProof{}, fmt.Errorf("%s published certificate or HTTP/2 protocol differs from pinned leaf", service)
		}
		proof.Peers = append(proof.Peers, peerProof{
			Service: service, Port: ports[i], CertificateSHA256: fingerprint(leaf.Raw),
		})
	}
	if baseline != nil && !reflect.DeepEqual(proof, *baseline) {
		return tlsProof{}, errors.New("benchmark TLS identity changed since preflight")
	}
	return proof, nil
}

func fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}
