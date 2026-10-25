package provider

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
	"github.com/anaregdesign/lantern/server/service"
)

func clearSecurityEnvironment(t *testing.T) {
	t.Helper()
	for _, setting := range os.Environ() {
		name, _, _ := strings.Cut(setting, "=")
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "LANTERN_AUTH_") || strings.HasPrefix(upper, "LANTERN_OIDC_") || strings.HasPrefix(upper, "LANTERN_SECURITY_") {
			t.Setenv(name, "")
			if err := os.Unsetenv(name); err != nil {
				t.Fatal(err)
			}
		}
	}
}

type securityTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *securityTestClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *securityTestClock) advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}
func securityKeyFixture(t *testing.T, dir string) (string, string) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	privatePath, publicPath := filepath.Join(dir, "operator.key"), filepath.Join(dir, "operator.pub")
	if err = os.WriteFile(privatePath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(publicPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}), 0644); err != nil {
		t.Fatal(err)
	}
	return privatePath, publicPath
}
func securityDataFixture(t *testing.T) *service.ServingRuntime {
	t.Helper()
	graph := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	log := mutationlog.New(mutationlog.Options{Capacity: 16})
	data, err := service.NewGraphOnlyServingRuntime(graph, log, hlc.New(hlc.NodeID{7}, hlc.Options{}), "namespaced-v1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = data.Close() })
	return data
}

func securityWorkloadFixture(t *testing.T, now func() time.Time) *PeerIdentityRuntime {
	t.Helper()
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	instant := now()
	member := peerauth.Member{ID: [16]byte{3}, Identity: "spiffe://worker.test/self", SPKI: [32]byte{4}, Origin: "https://127.0.0.1:17443"}
	manifest := peerauth.Manifest{Version: 1, Domain: peerauth.Domain{Deployment: [16]byte{1}, NamespaceFormat: "namespaced-v1", AuthMode: "off", TrustDigest: [32]byte{2}}, IssuedAt: instant.Add(-time.Second * 3), ExpiresAt: instant.Add(time.Minute), Members: []peerauth.Member{member}}
	raw, err := peerauth.SignManifest(manifest, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	store, err := peerauth.CreateStore(peerauth.StoreOptions{Path: filepath.Join(dir, "membership"), Key: public, Domain: manifest.Domain, Now: now, SelfCertificates: []peerauth.CertificateWindow{{Start: instant.Add(-time.Hour), End: instant.Add(time.Hour)}}}, raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	input := filepath.Join(dir, "membership.signed")
	if err := os.WriteFile(input, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return &PeerIdentityRuntime{store: store, config: PeerIdentityConfig{Now: now, SelfIdentity: member.Identity, ManifestFile: input}}
}
