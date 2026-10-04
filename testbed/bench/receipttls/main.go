// Command receipttls verifies the live public HTTPS/HTTP2 identity before and
// after a native receipt run. authfixture owns trust/material generation.
package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
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
		fatal(errors.New("expected verify (generate native trust with authfixture -compose)"))
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
		time.Until(ca.NotAfter) < 5*time.Minute {
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
		// The live handshake proves key possession. The host proof never reads
		// the workload-owned private key after native volume provisioning.
		rawLeaf, err := os.ReadFile(filepath.Join(dir, service, "server.pem"))
		if err != nil {
			return tlsProof{}, err
		}
		leafPEM, rest := pem.Decode(rawLeaf)
		if leafPEM == nil || leafPEM.Type != "CERTIFICATE" || len(rest) != 0 {
			return tlsProof{}, errors.New("invalid public benchmark leaf")
		}
		leaf, err := x509.ParseCertificate(leafPEM.Bytes)
		if err != nil {
			return tlsProof{}, err
		}
		if _, err := leaf.Verify(x509.VerifyOptions{
			Roots: roots, DNSName: service,
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
