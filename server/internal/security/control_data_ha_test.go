package security

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/keyspace"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

// The data workload plane has its own original keys/membership and listener.
// Its fixed origins point at byte-transparent fault proxies, not RPC mocks.
func exportCurrentDataHA(t *testing.T, dir string, hosts []string, driver string, profile CurrentProfile) {
	t.Helper()
	write := func(name string, raw []byte) {
		t.Helper()
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.Write(raw)
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			t.Fatal(err, closeErr)
		}
	}
	key := func() (ed25519.PublicKey, ed25519.PrivateKey) {
		t.Helper()
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return pub, priv
	}
	caPub, caKey := key()
	now := time.Now().UTC()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "mixed HA data fixture"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, caPub, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	write("data-ca.pem", caPEM)
	operator, signer := key()
	opDER, err := x509.MarshalPKIXPublicKey(operator)
	if err != nil {
		t.Fatal(err)
	}
	write("data-operator.pub", pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: opDER}))
	var deployment [16]byte
	if _, err := rand.Read(deployment[:]); err != nil {
		t.Fatal(err)
	}
	manifest := peerauth.Manifest{Version: 1, IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(-time.Second).Add(peerauth.MaxMembershipLifetime), Domain: peerauth.Domain{Deployment: deployment, NamespaceFormat: keyspace.Version, AuthMode: "oidc", SecurityGeneration: profile.Generation, CurrentProfile: profile.Binding(), TrustDigest: sha256.Sum256(caPEM)}}
	for i, host := range hosts {
		pub, priv := key()
		identity := fmt.Sprintf("spiffe://lantern.fixture/data-%d", i+1)
		uri, err := url.Parse(identity)
		if err != nil {
			t.Fatal(err)
		}
		leaf := &x509.Certificate{SerialNumber: big.NewInt(int64(i + 2)), NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, URIs: []*url.URL{uri}, IPAddresses: []net.IP{net.ParseIP(driver), net.ParseIP(host)}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
		cert, err := x509.CreateCertificate(rand.Reader, leaf, ca, pub, caKey)
		if err != nil {
			t.Fatal(err)
		}
		privateDER, err := x509.MarshalPKCS8PrivateKey(priv)
		if err != nil {
			t.Fatal(err)
		}
		write(fmt.Sprintf("node-%d/data.pem", i+1), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert}))
		write(fmt.Sprintf("node-%d/data.key", i+1), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}))
		parsed, err := x509.ParseCertificate(cert)
		if err != nil {
			t.Fatal(err)
		}
		var member [16]byte
		member[15] = byte(i + 1)
		manifest.Members = append(manifest.Members, peerauth.Member{ID: member, Identity: identity, SPKI: sha256.Sum256(parsed.RawSubjectPublicKeyInfo), Origin: fmt.Sprintf("https://%s:%d", driver, 17381+i)})
	}
	raw, err := peerauth.SignManifest(manifest, signer)
	if err != nil {
		t.Fatal(err)
	}
	write("data-membership.json", raw)
	metadata, err := json.Marshal(map[string]any{"deployment": fmt.Sprintf("%x", deployment), "expires_at": manifest.ExpiresAt})
	if err != nil {
		t.Fatal(err)
	}
	write("data-metadata.json", metadata)
}
