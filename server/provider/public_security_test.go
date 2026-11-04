package provider

import (
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/replication"
)

func TestGeneratedFixedTopologyPreservesStandaloneEdgeCreate(t *testing.T) {
	for _, count := range []int{1, 3} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			dir := t.TempDir()
			now := time.Date(2026, time.October, 25, 0, 0, 0, 0, time.UTC)
			issuer := security.Issuer{URL: "https://issuer.example", ConfigRevision: 1, EnvOwned: true, Enabled: true, ClientID: "admin", RedirectURI: "https://admin.example/callback", Algorithms: []string{"EdDSA"}}
			image := security.Image{Version: security.ImageVersion, BootstrapRevision: 1, Issuers: []security.Issuer{issuer},
				Roles:      []security.Role{{ID: "security_admin", Name: "Security administrator", Rules: []security.PermissionRule{{ID: "manage", Effect: security.Allow, Action: security.SecurityManage, Resource: security.GlobalResource}}}},
				Principals: []security.Principal{{Identity: security.Identity{Kind: security.OIDCPrincipal, Issuer: issuer.URL, Subject: "admin"}, State: security.Active, HumanIssuerConfigRevision: 1, Assignments: []security.RoleAssignment{{RoleID: "security_admin", EnvOwned: true}}}}}
			var nodes []security.ProvisioningNode
			for i := 1; i <= count; i++ {
				node := security.ProvisioningNode{Name: fmt.Sprintf("node-%d", i), ControlOrigin: fmt.Sprintf("https://127.0.0.1:%d", 17000+2*i), DataOrigin: fmt.Sprintf("https://127.0.0.1:%d", 17001+2*i), ControlListen: fmt.Sprintf("127.0.0.1:%d", 17000+2*i), StateDir: filepath.Join(dir, fmt.Sprint(i), "state"), RenewableDir: filepath.Join(dir, fmt.Sprint(i), "membership")}
				for _, path := range []string{node.StateDir, node.RenewableDir} {
					if err := os.MkdirAll(path, 0700); err != nil {
						t.Fatal(err)
					}
				}
				nodes = append(nodes, node)
			}
			bundle, err := security.GenerateProvisioning(security.ProvisioningOptions{Target: runtime.GOOS, Now: now, End: now.Add(time.Hour), CleanupBy: now.Add(2 * time.Hour), Image: image, Nodes: nodes})
			if err != nil {
				t.Fatal(err)
			}
			node := bundle.Nodes[0]
			files := map[string][]byte{"control.json": bundle.Operator.ControlManifest, "data.json": node.DataManifest, "operator.pem": node.DataOperatorPublicKey, "roots.pem": node.RootsPEM, "certificate.pem": node.CertificatePEM, "key.pem": node.TLSKeyPEM}
			for name, raw := range files {
				if err := os.WriteFile(filepath.Join(nodes[0].RenewableDir, name), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			provisioning, err := security.LoadCurrentProvisioningJSON(node.AuthorityJSON)
			if err != nil {
				t.Fatal(err)
			}
			path := func(name string) string { return filepath.Join(nodes[0].RenewableDir, name) }
			peer, closePeer, err := NewPeerIdentityRuntime(PeerIdentityConfig{AuthMode: "oidc", Deployment: node.DataDomain.Deployment, SecurityGeneration: node.CurrentProfile.Generation, CurrentProfile: node.CurrentProfile.Binding(), StateMode: "fresh", StateFile: filepath.Join(nodes[0].StateDir, "data-membership"), ManifestFile: path("data.json"), OperatorKeyFile: path("operator.pem"), CAFile: path("roots.pem"), CertFile: path("certificate.pem"), KeyFile: path("key.pem"), SelfIdentity: node.Identity, Now: func() time.Time { return now }, TimeBounds: func() (time.Time, time.Time, error) { return now, now, nil }})
			if err != nil {
				t.Fatal(err)
			}
			defer closePeer()
			graph := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](time.Minute)
			data, err := dataruntime.NewGraphOnlyServingRuntime(graph, mutationlog.New(mutationlog.Options{Capacity: 16}), hlc.New(hlc.NodeID{7}, hlc.Options{}), "namespaced-v1")
			if err != nil {
				t.Fatal(err)
			}
			defer data.Close()
			primary := data.NewExecutor(nil)
			if err := certifyEdgeCreateTopology(primary, "oidc", provisioning.FixedVoterCount(), peer); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"data:tail", "data:head"} {
				if _, err := primary.PutVertex(t.Context(), &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: key, Value: &pb.Vertex_String_{String_: key}}}); err != nil {
					t.Fatal(err)
				}
			}
			result, err := primary.CreateEdge(t.Context(), &pb.CreateEdgeRequest{Edge: &pb.Edge{Tail: "data:tail", Head: "data:head", Weight: 1}})
			if count == 1 {
				if err != nil || result.GetOutcome() != pb.CreateEdgeOutcome_CREATE_EDGE_OUTCOME_CREATED_AND_LIVE {
					t.Fatal("authenticated singleton disabled existing-endpoint Create", err)
				}
			} else if connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatal("fixed multi-node enabled unsupported Create", err)
			}
			if err := certifyEdgeCreateTopology(data.NewExecutor(nil), "oidc", count+1, peer); err == nil {
				t.Fatal("data topology mismatch reached public certification")
			}
		})
	}
}

func TestPublicCertificationRequiresExactInstalledServicesAndTransport(t *testing.T) {
	data := securityDataFixture(t)
	cfg := SecurityConfig{Mode: "off"}
	runtime, closeRuntime, err := NewSecurityRuntime(t.Context(), cfg, data)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRuntime()
	primary := data.NewExecutor(nil)
	peer, err := data.NewReplication(primary)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewRuntimeRestored(data, primary)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := NewRuntimeCertified(data, primary, peer, restored, NetConfig{})
	if err != nil {
		t.Fatal(err)
	}
	logging, err := NewManagedLogRuntime(t.Context(), runtime, newServingLogger(ObservabilityConfig{}, io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	operational := operationalPublicTestCertification(t, runtime, primary, NewSlowRPCInterceptor(0, slog.New(slog.NewTextHandler(io.Discard, nil))), cert)
	for _, mutate := range []func(*managedOperationalCertified){
		func(c *managedOperationalCertified) { c.rate = nil },
		func(c *managedOperationalCertified) { c.rate = NewRateLimitInterceptor(0, 0) },
		func(c *managedOperationalCertified) { c.recovery = nil },
		func(c *managedOperationalCertified) { copied := *c.recovery; c.recovery = &copied },
		func(c *managedOperationalCertified) { c.primary = data.NewExecutor(nil) },
		func(c *managedOperationalCertified) { c.request = nil },
		func(c *managedOperationalCertified) { c.validator = nil },
		func(c *managedOperationalCertified) { c.validator = NewValidationInterceptor(c.validator.limits) },
		func(c *managedOperationalCertified) { c.backup = nil },
		func(c *managedOperationalCertified) { c.capacity = nil },
		func(c *managedOperationalCertified) { c.search = managedSearchCertified{} },
		func(c *managedOperationalCertified) { c.capacity = &dataruntime.GraphPolicyOwner{} },
		func(c *managedOperationalCertified) { c.pump = nil },
		func(c *managedOperationalCertified) { c.anti = nil },
		func(c *managedOperationalCertified) {
			fingerprint := primary.SearchConfigFingerprint()
			c.pump = replication.NewPump(replication.Config{SearchConfigFingerprint: fingerprint}, nil, nil)
			c.anti = replication.NewAntiEntropy(replication.AntiEntropyConfig{Pump: c.pump, SearchConfigFingerprint: fingerprint}, nil, nil, nil)
		},
		func(c *managedOperationalCertified) {
			c.backup = backup.New(data.NewExecutor(nil), backup.Config{Interval: time.Hour}, nil, nil)
		},
	} {
		wrong := operational
		mutate(&wrong)
		if _, err := NewPublicSecurityCertified(runtime, TLSConfig{}, nil, data, primary, cert, logging, wrong); err == nil {
			t.Fatal("foreign or absent operational resource certified")
		}
	}
	if _, err := NewPublicSecurityCertified(runtime, TLSConfig{}, nil, data, primary, cert, managedLogCertified{}, operational); err == nil {
		t.Fatal("missing logger certification accepted")
	}
	foreignLogging := logging
	foreignLogging.owner = newServingLogger(ObservabilityConfig{}, io.Discard)
	if _, err := NewPublicSecurityCertified(runtime, TLSConfig{}, nil, data, primary, cert, foreignLogging, operational); err == nil {
		t.Fatal("foreign logger owner certified")
	}
	if _, err := NewPublicSecurityCertified(runtime, TLSConfig{}, nil, data, primary, runtimeCertified{}, logging, operational); err == nil {
		t.Fatal("missing data certification accepted")
	}
	foreign := dataruntime.NewExecutor(data.GraphCache())
	if _, err := NewPublicSecurityCertified(runtime, TLSConfig{}, nil, data, foreign, cert, logging, operational); err == nil {
		t.Fatal("foreign service certified")
	}
	if _, err := NewPublicSecurityCertified(runtime, TLSConfig{}, &PeerIdentityRuntime{}, data, primary, cert, logging, operational); err == nil {
		t.Fatal("unbound workload certified")
	}
	public, err := NewPublicSecurityCertified(runtime, TLSConfig{}, nil, data, primary, cert, logging, operational)
	if err != nil || public.rate != operational.rate || public.recovery != operational.recovery || public.request != operational.request || public.validator != operational.validator || public.backup != operational.backup || public.capacity != operational.capacity || public.search != operational.search {
		t.Fatal(err)
	}
	runtime.mode = "oidc"
	if _, err := NewPublicSecurityCertified(runtime, TLSConfig{}, nil, data, primary, cert, logging, operational); err == nil {
		t.Fatal("OIDC without its authority was certified")
	}

}
func TestPublicIngressRejectsSpoofedOrAmbiguousGateway(t *testing.T) {
	runtime := &SecurityRuntime{mode: "oidc", config: SecurityConfig{TrustedProxyIPs: []netip.Addr{netip.MustParseAddr("127.0.0.1")}}}
	for _, test := range []struct {
		remote      string
		tls         bool
		proto, host []string
		want        bool
	}{
		{"127.0.0.1:100", false, []string{"https"}, []string{"api.example"}, true},
		{"127.0.0.2:100", false, []string{"https"}, []string{"api.example"}, false},
		{"127.0.0.1:100", false, []string{"https", "http"}, []string{"api.example"}, false},
		{"127.0.0.1:100", false, []string{"https"}, []string{"other.example"}, false},
		{"127.0.0.2:100", true, nil, nil, true},
	} {
		req := httptest.NewRequest(http.MethodPost, "https://api.example/rpc", nil)
		req.TLS = nil
		req.RemoteAddr = test.remote
		if test.tls {
			req.TLS = &tls.ConnectionState{}
		}
		req.Header["X-Forwarded-Proto"] = test.proto
		req.Header["X-Forwarded-Host"] = test.host
		if got := runtime.protectedIngress(req); got != test.want {
			t.Fatalf("ingress %v=%v", test, got)
		}
	}
}
