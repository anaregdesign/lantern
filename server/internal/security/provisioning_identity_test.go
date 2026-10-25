package security

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"
)

func TestProvisioningIdentityCertificateAndIndependentKeys(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 123456789, time.UTC)
	cleanup := now.Add(time.Hour)
	ca, err := newProvisioningCA(now, cleanup)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { clear(ca.key) })
	if ca.digest != sha256.Sum256(ca.rootsPEM) || !ca.certificate.NotAfter.Equal(now.AddDate(10, 0, 0).Truncate(time.Second)) {
		t.Fatal("private root does not pin its exact encoding and cleanup capacity")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca.rootsPEM) {
		t.Fatal("generated root is not PEM")
	}
	var previous *x509.Certificate
	for range 2 {
		identity, err := ca.issue([]string{"https://node-1:16380", "https://127.0.0.1:16381"}, "spiffe://lantern.internal/node-1", now, cleanup)
		if err != nil {
			t.Fatal(err)
		}
		pair, err := tls.X509KeyPair(identity.certificatePEM, identity.tlsKeyPEM)
		if err != nil || len(pair.Certificate) != 1 {
			t.Fatal("generated TLS key/certificate pair is invalid", err)
		}
		leaf, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			t.Fatal(err)
		}
		if leaf.IsCA || len(leaf.URIs) != 1 || leaf.URIs[0].String() != identity.identity ||
			sha256.Sum256(leaf.RawSubjectPublicKeyInfo) != identity.spki {
			t.Fatal("generated workload has wrong URI or SPKI")
		}
		for _, host := range []string{"node-1", "127.0.0.1"} {
			if err := leaf.VerifyHostname(host); err != nil {
				t.Fatal("control/data hostname omitted", err)
			}
		}
		if leaf.VerifyHostname("other-node") == nil {
			t.Fatal("certificate admitted an unconfigured hostname")
		}
		for _, usage := range []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth} {
			if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: cleanup, KeyUsages: []x509.ExtKeyUsage{usage}}); err != nil {
				t.Fatal("workload chain does not cover cleanup", err)
			}
		}
		key := pair.PrivateKey.(ed25519.PrivateKey)
		if bytes.Equal(key, ca.key) || previous != nil && (bytes.Equal(previous.RawSubjectPublicKeyInfo, leaf.RawSubjectPublicKeyInfo) || previous.SerialNumber.Cmp(leaf.SerialNumber) == 0) {
			t.Fatal("CA or workload private identity was reused")
		}
		block, trailing := pem.Decode(identity.tlsKeyPEM)
		if block == nil || len(trailing) != 0 || block.Type != "PRIVATE KEY" {
			t.Fatal("workload key encoding is not one PKCS#8 PEM")
		}
		if _, err := x509.ParsePKCS8PrivateKey(block.Bytes); err != nil {
			t.Fatal(err)
		}
		clear(key)
		clear(identity.tlsKeyPEM)
		previous = leaf
	}
	otherCA, err := newProvisioningCA(now, cleanup)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(otherCA.key)
	if ca.digest == otherCA.digest || bytes.Equal(ca.key, otherCA.key) {
		t.Fatal("independent setups reused private CA identity")
	}
}

func TestProvisioningIdentityRejectsMalformedInputs(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	cleanup := now.Add(time.Hour)
	ca, err := newProvisioningCA(now, cleanup)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(ca.key)
	for _, input := range []struct {
		name     string
		origins  []string
		identity string
	}{
		{"no endpoint", nil, "spiffe://lantern.internal/node-1"},
		{"too many endpoints", []string{"https://a:1", "https://b:2", "https://c:3"}, "spiffe://lantern.internal/node-1"},
		{"plaintext", []string{"http://node-1:16380"}, "spiffe://lantern.internal/node-1"},
		{"endpoint path", []string{"https://node-1:16380/path"}, "spiffe://lantern.internal/node-1"},
		{"noncanonical port", []string{"https://node-1:016380"}, "spiffe://lantern.internal/node-1"},
		{"endpoint credentials", []string{"https://secret@node-1:16380"}, "spiffe://lantern.internal/node-1"},
		{"wildcard", []string{"https://*.lantern.internal:16380"}, "spiffe://lantern.internal/node-1"},
		{"unspecified", []string{"https://0.0.0.0:16380"}, "spiffe://lantern.internal/node-1"},
		{"zoned IP", []string{"https://[fe80::1%25en0]:16380"}, "spiffe://lantern.internal/node-1"},
		{"wrong URI", []string{"https://node-1:16380"}, "https://lantern.internal/node-1"},
		{"URI alias", []string{"https://node-1:16380"}, "spiffe://lantern.internal/a/../node-1"},
		{"URI escaping", []string{"https://node-1:16380"}, "spiffe://lantern.internal/node%2f1"},
		{"URI query", []string{"https://node-1:16380"}, "spiffe://lantern.internal/node-1?secret=value"},
	} {
		t.Run(input.name, func(t *testing.T) {
			if _, err := ca.issue(input.origins, input.identity, now, cleanup); err == nil {
				t.Fatal("invalid workload input produced a certificate")
			}
		})
	}
	for _, times := range [][2]time.Time{{{}, cleanup}, {now, {}}, {now, now}, {now, now.Add(-time.Second)}, {now, time.Date(2080, 1, 1, 0, 0, 0, 0, time.UTC)}} {
		if _, err := newProvisioningCA(times[0], times[1]); err == nil {
			t.Fatal("invalid explicit certificate period accepted")
		}
	}
	if _, err := ca.issue([]string{"https://node-1:16380"}, "spiffe://lantern.internal/node-1", now, ca.certificate.NotAfter); err == nil {
		t.Fatal("leaf exceeded root cleanup capacity")
	}
	late := time.Date(2075, 1, 1, 0, 0, 0, 0, time.UTC)
	lateCA, err := newProvisioningCA(late, late.Add(time.Hour))
	if err != nil || !lateCA.certificate.NotAfter.Equal(time.Date(2079, 12, 31, 23, 59, 59, 0, time.UTC)) {
		t.Fatal("root lifetime did not respect admitted UTC limit", err)
	}
	clear(lateCA.key)
	identity, err := ca.issue([]string{"https://node-1:16380", "https://node-1:16381"}, "spiffe://lantern.internal/node-1", now, cleanup)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(identity.certificatePEM)
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil || len(leaf.DNSNames) != 1 {
		t.Fatal("same control/data host did not produce one SAN", err)
	}
	clear(identity.tlsKeyPEM)
}
