package security

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

// Provisioning and explicit offline certificate maintenance use this private
// CA. Its key never belongs in the serving runtime or membership publisher.
type provisioningCA struct {
	certificate *x509.Certificate
	key         ed25519.PrivateKey
	rootsPEM    []byte
	digest      [32]byte
}

type provisioningIdentity struct {
	certificatePEM []byte
	tlsKeyPEM      []byte
	spki           [32]byte
	identity       string
}

// These timestamps describe the explicit setup ceremony, not trusted runtime
// time. The extra day leaves certificate capacity for orderly publisher and
// server shutdown after the declared operator window.
func provisioningCertificatePeriod(now, cleanupBy time.Time) (time.Time, time.Time, error) {
	if now.IsZero() || cleanupBy.IsZero() || !cleanupBy.After(now) || now.Year() < 2020 || cleanupBy.Year() >= 2080 {
		return time.Time{}, time.Time{}, errS3AConfig
	}
	start := now.UTC().Add(-time.Minute).Truncate(time.Second)
	end := cleanupBy.UTC().Add(24 * time.Hour).Truncate(time.Second)
	if end.Year() >= 2080 || !end.After(cleanupBy) {
		return time.Time{}, time.Time{}, errS3AConfig
	}
	return start, end, nil
}

func provisioningSerial() (*big.Int, error) {
	var raw [20]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, errS3AConfig
	}
	// Positive, nonzero and at most 159 bits, as required for X.509 serials.
	raw[0] = raw[0]&0x7f | 0x01
	return new(big.Int).SetBytes(raw[:]), nil
}

func newProvisioningCA(now, cleanupBy time.Time) (*provisioningCA, error) {
	start, leafEnd, err := provisioningCertificatePeriod(now, cleanupBy)
	if err != nil {
		return nil, err
	}
	end := now.UTC().AddDate(10, 0, 0).Truncate(time.Second)
	limit := time.Date(2079, 12, 31, 23, 59, 59, 0, time.UTC)
	if end.After(limit) {
		end = limit
	}
	if leafEnd.After(end) {
		return nil, errS3AConfig
	}
	serial, err := provisioningSerial()
	if err != nil {
		return nil, err
	}
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, errS3AConfig
	}
	transferred := false
	defer func() {
		if !transferred {
			clear(key)
		}
	}()
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "Lantern private workload root"},
		NotBefore: start, NotAfter: end, IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign, MaxPathLenZero: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, key)
	if err != nil {
		return nil, errS3AConfig
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, errS3AConfig
	}
	roots := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	transferred = true
	return &provisioningCA{certificate: certificate, key: key, rootsPEM: roots, digest: sha256.Sum256(roots)}, nil
}

func provisioningWorkloadURI(raw string) (*url.URL, error) {
	if len(raw) == 0 || len(raw) > 512 || strings.ContainsAny(raw, "\\%?#") {
		return nil, errS3AConfig
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "spiffe" || u.Host == "" || u.User != nil || u.Port() != "" ||
		u.Opaque != "" || u.RawPath != "" || u.Path == "" || strings.HasSuffix(u.Path, "/") || path.Clean(u.Path) != u.Path || u.String() != raw {
		return nil, errS3AConfig
	}
	for _, ch := range u.Host {
		if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || strings.ContainsRune(".-_", ch)) {
			return nil, errS3AConfig
		}
	}
	for _, ch := range u.Path {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || strings.ContainsRune("/._-", ch)) {
			return nil, errS3AConfig
		}
	}
	return u, nil
}

func provisioningOriginHost(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.Path != "" ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.String() != raw {
		return "", errS3AConfig
	}
	host, port, err := net.SplitHostPort(u.Host)
	n, portErr := strconv.Atoi(port)
	if err != nil || host == "" || strings.ContainsAny(host, "%\\") || portErr != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return "", errS3AConfig
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() {
			return "", errS3AConfig
		}
		return ip.String(), nil
	}
	for _, ch := range host {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || strings.ContainsRune(".-_", ch)) {
			return "", errS3AConfig
		}
	}
	if strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || strings.Contains(host, "..") {
		return "", errS3AConfig
	}
	return host, nil
}

func (ca *provisioningCA) issue(origins []string, identity string, now, cleanupBy time.Time) (*provisioningIdentity, error) {
	start, end, err := provisioningCertificatePeriod(now, cleanupBy)
	if err != nil || ca == nil || ca.certificate == nil || len(ca.key) != ed25519.PrivateKeySize ||
		start.Before(ca.certificate.NotBefore) || end.After(ca.certificate.NotAfter) || len(origins) < 1 || len(origins) > 2 {
		return nil, errS3AConfig
	}
	uri, err := provisioningWorkloadURI(identity)
	if err != nil {
		return nil, err
	}
	serial, err := provisioningSerial()
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber: serial, NotBefore: start, NotAfter: end,
		URIs: []*url.URL{uri}, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	seen := make(map[string]bool, len(origins))
	for _, origin := range origins {
		host, err := provisioningOriginHost(origin)
		if err != nil {
			return nil, err
		}
		if seen[host] {
			continue
		}
		seen[host] = true
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, host)
		}
	}
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, errS3AConfig
	}
	defer clear(key)
	der, err := x509.CreateCertificate(rand.Reader, template, ca.certificate, public, ca.key)
	if err != nil {
		return nil, errS3AConfig
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, errS3AConfig
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, errS3AConfig
	}
	defer clear(keyDER)
	return &provisioningIdentity{certificatePEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		tlsKeyPEM: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
		spki:      sha256.Sum256(leaf.RawSubjectPublicKeyInfo), identity: identity}, nil
}
