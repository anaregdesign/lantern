package membershippublisher

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func publisherFixture(t *testing.T) (string, string, Config, time.Time) {
	t.Helper()
	config, state, c, now, _ := publisherTrustFixture(t, time.Hour)
	return config, state, c, now
}

func publisherTrustFixture(t *testing.T, lifetime time.Duration) (string, string, Config, time.Time, func(time.Time, time.Time)) {
	t.Helper()
	dir, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	write := func(name string, raw []byte) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if e := os.WriteFile(path, raw, 0600); e != nil {
			t.Fatal(e)
		}
		return path
	}
	now := time.Now().UTC().Truncate(time.Second)
	var members []peerauth.Member
	var voters []peerauth.ControlVoter
	var certs []string
	rootPublic, rootKey, _ := ed25519.GenerateKey(rand.Reader)
	rootTemplate := &x509.Certificate{SerialNumber: big.NewInt(100), NotBefore: now.Add(-365 * 24 * time.Hour), NotAfter: now.Add(10 * 365 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	rootDER, e := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, rootPublic, rootKey)
	if e != nil {
		t.Fatal(e)
	}
	root, e := x509.ParseCertificate(rootDER)
	if e != nil {
		t.Fatal(e)
	}
	rootPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER})
	var leaves []*x509.Certificate
	var leafKeys []ed25519.PrivateKey
	for i := 1; i <= 3; i++ {
		pub, key, _ := ed25519.GenerateKey(rand.Reader)
		uri, _ := url.Parse("spiffe://renew.test/node" + string(rune('0'+i)))
		template := &x509.Certificate{SerialNumber: big.NewInt(int64(i)), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(lifetime), URIs: []*url.URL{uri}, KeyUsage: x509.KeyUsageDigitalSignature}
		der, e := x509.CreateCertificate(rand.Reader, template, root, pub, rootKey)
		if e != nil {
			t.Fatal(e)
		}
		cert, _ := x509.ParseCertificate(der)
		member := peerauth.Member{ID: [16]byte{byte(i)}, Identity: uri.String(), SPKI: sha256.Sum256(cert.RawSubjectPublicKeyInfo), Origin: "https://127.0.0.1:1700" + string(rune('0'+i))}
		members = append(members, member)
		vote, _, _ := ed25519.GenerateKey(rand.Reader)
		voters = append(voters, peerauth.ControlVoter{Workload: member, Voter: uint32(i), Key: [32]byte(vote), Proposer: true})
		leaves = append(leaves, template)
		leafKeys = append(leafKeys, key)
		certs = append(certs, write("cert"+string(rune('0'+i))+".pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})))
	}
	rootPath := write("roots.pem", rootPEM)
	trust := sha256.Sum256(rootPEM)
	c := Config{Version: 1, End: now.Add(15 * time.Minute), CleanupBy: now.Add(16 * time.Minute), Interval: 5 * time.Second, Lifetime: 30 * time.Second}
	for _, kind := range []string{"control", "data"} {
		_, key, _ := ed25519.GenerateKey(rand.Reader)
		der, _ := x509.MarshalPKCS8PrivateKey(key)
		keyPath := write(kind+".key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
		var raw []byte
		if kind == "control" {
			m := peerauth.ControlManifest{Version: 1, Profile: peerauth.ControlProfile{Version: 1, Lineage: peerauth.ControlLineage{Deployment: [16]byte{1}, Instance: [16]byte{2}}, NamespaceFormat: "namespaced-v1", ProtocolScope: [32]byte{3}, TrustDigest: trust, Voters: voters}, IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(-30 * time.Second)}
			raw, e = peerauth.SignControlManifest(m, key)
		} else {
			m := peerauth.Manifest{Version: 1, Domain: peerauth.Domain{Deployment: [16]byte{1}, NamespaceFormat: "namespaced-v1", AuthMode: "off", TrustDigest: trust}, Members: members, IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(-30 * time.Second)}
			raw, e = peerauth.SignManifest(m, key)
		}
		if e != nil {
			t.Fatal(e)
		}
		c.Streams = append(c.Streams, Stream{Kind: kind, KeyFile: keyPath, RootsFile: rootPath, Initial: raw, Certificates: certs, Targets: []string{write(kind+"-a", raw), write(kind+"-b", raw)}})
	}
	raw, _ := json.Marshal(c)
	renew := func(start, end time.Time) {
		t.Helper()
		for i, leaf := range leaves {
			next := *leaf
			next.NotBefore = start
			next.NotAfter = end
			der, err := x509.CreateCertificate(rand.Reader, &next, root, leafKeys[i].Public(), rootKey)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(certs[i], pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return write("config.json", raw), filepath.Join(dir, "state.json"), c, now, renew
}

func writePublisherConfig(t *testing.T, path string, c Config) string {
	t.Helper()
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func readPublisherJournal(t *testing.T, path string) journal {
	t.Helper()
	raw, err := readPrivate(path, maxStateBytes)
	if err != nil {
		t.Fatal(err)
	}
	var j journal
	if decode(raw, &j) != nil {
		t.Fatal("invalid journal")
	}
	return j
}

func closePublisher(t *testing.T, p *Publisher) {
	t.Helper()
	if err := p.Close(); err != nil {
		t.Error(err)
	}
}
