package provider

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/service"
)

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
	privatePath, publicPath := filepath.Join(dir, "writer.key"), filepath.Join(dir, "writer.pub")
	if err = os.WriteFile(privatePath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(publicPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}), 0644); err != nil {
		t.Fatal(err)
	}
	return privatePath, publicPath
}
func securityRuntimeFixture(t *testing.T) (SecurityConfig, *service.ServingRuntime, *securityTestClock) {
	t.Helper()
	dir := t.TempDir()
	privatePath, publicPath := securityKeyFixture(t, dir)
	clock := &securityTestClock{now: time.Now()}
	issuer := security.Issuer{URL: "https://idp.example", Enabled: true, ClientID: "admin", APIAudience: "api", RedirectURI: "https://admin.example" + oidc.CallbackPath("https://idp.example"), Algorithms: []string{"EdDSA"}}
	config := SecurityConfig{Mode: "oidc", Profile: "legacy-v1", StoreMode: "fresh", StorePath: filepath.Join(dir, "security.wal"), Generation: [16]byte{1}, WriterKeyFile: privatePath, WriterPublicKeyFile: publicPath, NodeRole: "writer", WriterEndpoint: "https://writer-peer.example", BrowserOrigin: "https://admin.example", Bootstrap: security.Bootstrap{Revision: 1, Issuer: issuer, AdminSubjects: []string{"admin", "other"}}, MaxJournalBytes: security.DefaultSystemJournalMax, ClockQualified: true, Clock: clock.Now}
	graph := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	log := mutationlog.New(mutationlog.Options{Capacity: 16})
	hlcClock := hlc.New(hlc.NodeID{7}, hlc.Options{})
	data, err := service.NewGraphOnlyServingRuntime(graph, log, hlcClock, "namespaced-v1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = data.Close() })
	return config, data, clock
}
