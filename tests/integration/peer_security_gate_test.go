package integration_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/replication"
	"github.com/anaregdesign/lantern/server/service"
)

// Independent operator wire fixture: root tests do not import Server internals
// or rely on their signing/serialization helpers to validate the same contract.
type peerOperatorDomain struct {
	Deployment         [16]byte `json:"deployment"`
	NamespaceFormat    string   `json:"namespace_format"`
	AuthMode           string   `json:"auth_mode"`
	SecurityGeneration [16]byte `json:"security_generation"`
	WriterPublicKey    [32]byte `json:"writer_public_key"`
	TrustDigest        [32]byte `json:"trust_digest"`
}

type peerOperatorMember struct {
	ID       [16]byte `json:"id"`
	Identity string   `json:"identity"`
	SPKI     [32]byte `json:"spki"`
	Origin   string   `json:"origin"`
}

type peerOperatorManifest struct {
	Version   uint64               `json:"version"`
	Domain    peerOperatorDomain   `json:"domain"`
	IssuedAt  time.Time            `json:"issued_at"`
	ExpiresAt time.Time            `json:"expires_at"`
	Members   []peerOperatorMember `json:"members"`
}

type peerSecurityFixture struct {
	servers  [2]*httptest.Server
	runtimes [2]*provider.PeerIdentityRuntime
	primary  [2]*service.LanternService
	configs  [2]provider.PeerIdentityConfig
	manifest peerOperatorManifest
	key      ed25519.PrivateKey
	roots    *x509.CertPool
	certs    [2]tls.Certificate
}

type peerSecurityOptions struct {
	Generation [16]byte
	WriterKey  [32]byte
	Mount      func(*peerSecurityFixture, int, *service.ServingRuntime, *http.ServeMux)
}

func peerSecurityPEM(t *testing.T, directory, name, kind string, raw []byte) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: raw}), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func peerSecuritySign(t *testing.T, manifest peerOperatorManifest, key ed25519.PrivateKey) []byte {
	t.Helper()
	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(struct {
		Payload   json.RawMessage `json:"payload"`
		Signature []byte          `json:"signature"`
	}{payload, ed25519.Sign(key, append([]byte("lantern.peer.membership.v1\x00"), payload...))})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func newPeerSecurityFixture(t *testing.T, options ...peerSecurityOptions) *peerSecurityFixture {
	t.Helper()
	f := &peerSecurityFixture{}
	dir := t.TempDir()
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f.key = private
	publicDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	operatorPath := peerSecurityPEM(t, dir, "operator.pub", "PUBLIC KEY", publicDER)
	caPub, caKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Lantern peer test CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, caPub, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err = x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	caPath := peerSecurityPEM(t, dir, "ca.pem", "CERTIFICATE", caDER)
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	f.roots = x509.NewCertPool()
	f.roots.AddCert(ca)
	f.manifest = peerOperatorManifest{Version: 1, Domain: peerOperatorDomain{Deployment: [16]byte{1}, NamespaceFormat: "namespaced-v1", AuthMode: "off", TrustDigest: sha256.Sum256(caPEM)}, IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
	if len(options) != 0 {
		f.manifest.Domain.AuthMode = "oidc"
		f.manifest.Domain.SecurityGeneration = options[0].Generation
		f.manifest.Domain.WriterPublicKey = options[0].WriterKey
	}
	for i := range f.servers {
		server := httptest.NewUnstartedServer(nil)
		f.servers[i] = server
		identity := "spiffe://lantern.test/node-" + string(rune('a'+i))
		uri, _ := url.Parse(identity)
		certPub, certKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		leafTemplate := &x509.Certificate{SerialNumber: big.NewInt(int64(i + 2)),
			NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), URIs: []*url.URL{uri},
			IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature,
			ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
		leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, ca, certPub, caKey)
		if err != nil {
			t.Fatal(err)
		}
		leaf, err := x509.ParseCertificate(leafDER)
		if err != nil {
			t.Fatal(err)
		}
		keyDER, err := x509.MarshalPKCS8PrivateKey(certKey)
		if err != nil {
			t.Fatal(err)
		}
		certPath := peerSecurityPEM(t, dir, "cert-"+string(rune('a'+i))+".pem", "CERTIFICATE", leafDER)
		keyPath := peerSecurityPEM(t, dir, "key-"+string(rune('a'+i))+".pem", "PRIVATE KEY", keyDER)
		f.certs[i], err = tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			t.Fatal(err)
		}
		origin := "https://" + server.Listener.Addr().String()
		f.manifest.Members = append(f.manifest.Members, peerOperatorMember{ID: [16]byte{byte(i + 2)}, Identity: identity, SPKI: sha256.Sum256(leaf.RawSubjectPublicKeyInfo), Origin: origin})
		f.configs[i] = provider.PeerIdentityConfig{AuthMode: "off", Deployment: [16]byte{1}, CAFile: caPath, OperatorKeyFile: operatorPath,
			ManifestFile: filepath.Join(dir, "membership-"+string(rune('a'+i))+".json"), StateFile: filepath.Join(dir, "floor-"+string(rune('a'+i))),
			StateMode: "fresh", CertFile: certPath, KeyFile: keyPath, SelfIdentity: identity}
		f.configs[i].AuthMode = f.manifest.Domain.AuthMode
		f.configs[i].SecurityGeneration = f.manifest.Domain.SecurityGeneration
		f.configs[i].WriterPublicKey = f.manifest.Domain.WriterPublicKey
	}
	raw := peerSecuritySign(t, f.manifest, f.key)
	for i := range f.servers {
		if err := os.WriteFile(f.configs[i].ManifestFile, raw, 0600); err != nil {
			t.Fatal(err)
		}
		runtime, cleanup, err := provider.NewPeerIdentityRuntime(f.configs[i])
		if err != nil {
			t.Fatal(err)
		}
		f.runtimes[i] = runtime
		t.Cleanup(cleanup)
		graph := provider.NewGraphCache(provider.CacheConfig{TTL: time.Hour}, provider.SearchConfig{})
		data, err := service.NewGraphOnlyServingRuntime(graph, mutationlog.New(mutationlog.Options{Capacity: 16}), hlc.New(hlc.NodeID{byte(i + 3)}, hlc.Options{}), "namespaced-v1")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = data.Close() })
		primary := data.NewLanternService(nil).WithDataNamespace()
		f.primary[i] = primary
		peer, err := data.NewLanternReplicationService(primary)
		if err != nil {
			t.Fatal(err)
		}
		if err := data.CertifyInstallation(primary, peer); err != nil {
			t.Fatal(err)
		}
		mux := http.NewServeMux()
		mux.Handle(graphv1connect.NewLanternReplicationServiceHandler(service.NewLanternReplicationServiceConnectHandler(peer)))
		if len(options) != 0 && options[0].Mount != nil {
			options[0].Mount(f, i, data, mux)
		}
		server := f.servers[i]
		server.Config.Handler = runtime.ProtectPeerHTTPHandler(mux)
		server.TLS = runtime.TLSConfig()
		server.EnableHTTP2 = true
		server.StartTLS()
		t.Cleanup(server.Close)
	}
	return f
}

func TestPeerSecurityPolicyLeaseRealConnectFreshCutAndRevocation(t *testing.T) {
	dir := t.TempDir()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	publicPath := peerSecurityPEM(t, dir, "writer.pub", "PUBLIC KEY", publicDER)
	privatePath := peerSecurityPEM(t, dir, "writer.key", "PRIVATE KEY", privateDER)
	var publicBinding [32]byte
	copy(publicBinding[:], public)
	var wall atomic.Int64
	wall.Store(time.Now().UnixNano())
	clock := func() time.Time { return time.Unix(0, wall.Load()) }
	var policies [2]*provider.SecurityPeerRuntime
	f := newPeerSecurityFixture(t, peerSecurityOptions{Generation: [16]byte{8}, WriterKey: publicBinding,
		Mount: func(f *peerSecurityFixture, i int, data *service.ServingRuntime, mux *http.ServeMux) {
			config := provider.SecurityConfig{Mode: "oidc", StoreMode: "fresh", StorePath: filepath.Join(dir, "policy-"+string(rune('a'+i))+".wal"),
				Generation: [16]byte{8}, WriterPublicKeyFile: publicPath, NodeRole: "replica", WriterEndpoint: f.manifest.Members[0].Origin,
				BrowserOrigin: "https://admin.example", ClockQualified: true, Clock: clock}
			config.Bootstrap.Revision = 1
			config.Bootstrap.Issuer.URL = "https://idp.example"
			config.Bootstrap.Issuer.Enabled = true
			config.Bootstrap.Issuer.ClientID = "admin"
			config.Bootstrap.Issuer.APIAudience = "api"
			callback := sha256.Sum256([]byte(config.Bootstrap.Issuer.URL))
			config.Bootstrap.Issuer.RedirectURI = config.BrowserOrigin + "/auth/callback/" + hex.EncodeToString(callback[:])
			config.Bootstrap.Issuer.Algorithms = []string{"EdDSA"}
			config.Bootstrap.AdminSubjects = []string{"admin"}
			if i == 0 {
				config.NodeRole, config.WriterKeyFile = "writer", privatePath
			}
			runtime, cleanup, err := provider.NewSecurityRuntime(config, data)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cleanup)
			policies[i], err = provider.NewSecurityPeerRuntime(runtime, f.runtimes[i])
			if err != nil {
				t.Fatal(err)
			}
			mux.Handle(policies[i].PrivateHTTPHandler())
		}})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if policies[0].Ready(ctx) || policies[1].Ready(ctx) {
		t.Fatal("fresh process skipped authority barrier")
	}
	if err := policies[1].Renew(ctx); err == nil {
		t.Fatal("writer restart hold-down skipped")
	}
	wall.Add(int64(35 * time.Second))
	if err := policies[1].Renew(ctx); err != nil {
		t.Fatal("signed checkpoint/lease not installed", err)
	}
	if !policies[0].Ready(ctx) || !policies[1].Ready(ctx) {
		t.Fatal("matching leased cut not ready")
	}
	if err := policies[1].Renew(ctx); err != nil {
		t.Fatal("unchanged-cut lease not renewed", err)
	}
	privateClient := graphv1connect.NewLanternSecurityPeerServiceClient(f.runtimes[1].HTTPClient(), f.servers[0].URL)
	for _, receiver := range [][]byte{make([]byte, 15), {9, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}} {
		_, err := privateClient.RenewPolicyLease(ctx, connect.NewRequest(&pb.RenewPolicyLeaseRequest{Receiver: receiver, BootNonce: make([]byte, 16), Challenge: make([]byte, 32)}))
		if connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatal("forged receiver acquired lease", err)
		}
	}
	// Stopping renewals must expire existing authority even with a valid local
	// signed policy, then a fresh challenge may restore it without reinstalling.
	wall.Add(int64(29 * time.Second))
	if policies[1].Ready(ctx) {
		t.Fatal("transport outage retained expired serving lease")
	}
	if err := policies[1].Renew(ctx); err != nil || !policies[1].Ready(ctx) {
		t.Fatal("fresh renewal failed", err)
	}
	f.manifest.Version++
	f.manifest.Members = f.manifest.Members[:1]
	if err := os.WriteFile(f.configs[1].ManifestFile, peerSecuritySign(t, f.manifest, f.key), 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.runtimes[1].ReloadMembership(); err != nil {
		t.Fatal(err)
	}
	if policies[1].Ready(ctx) || policies[1].Renew(ctx) == nil {
		t.Fatal("removed replica retained policy serving authority")
	}
}

func TestPeerSecurityRealConnectAdmissionRemovalAndRestart(t *testing.T) {
	f := newPeerSecurityFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client := graphv1connect.NewLanternReplicationServiceClient(f.runtimes[1].HTTPClient(), f.servers[0].URL)
	request := func() *connect.Request[pb.PeerStatusRequest] {
		return connect.NewRequest(&pb.PeerStatusRequest{NamespaceFormat: "namespaced-v1"})
	}
	if response, err := client.PeerStatus(ctx, request()); err != nil || response.Msg.GetNamespaceFormat() != "namespaced-v1" {
		t.Fatal("approved mTLS peer failed", err)
	}
	credential := request()
	credential.Header().Set("Authorization", "Bearer "+testToken)
	if _, err := client.PeerStatus(ctx, credential); err == nil {
		t.Fatal("public credential entered workload transport")
	}
	plainClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: f.roots, Certificates: []tls.Certificate{f.certs[1]}}}}
	plain := graphv1connect.NewLanternReplicationServiceClient(plainClient, f.servers[0].URL)
	if _, err := plain.PeerStatus(ctx, request()); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal("missing domain admitted", err)
	}
	wrongDomain := request()
	wrongDomain.Header().Set("Lantern-Peer-Domain", hex.EncodeToString(make([]byte, 32)))
	if _, err := plain.PeerStatus(ctx, wrongDomain); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal("mixed domain admitted", err)
	}
	if _, err := f.primary[0].PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "data:visible"}}}); err != nil {
		t.Fatal(err)
	}
	stream, err := client.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{NamespaceFormat: "namespaced-v1", AcceptReceiptEnvelopes: true}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	if !stream.Receive() {
		t.Fatal("approved stream did not deliver mutation", stream.Err())
	}
	f.manifest.Version++
	f.manifest.Members = f.manifest.Members[:1]
	raw := peerSecuritySign(t, f.manifest, f.key)
	if err := os.WriteFile(f.configs[0].ManifestFile, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.runtimes[0].ReloadMembership(); err != nil {
		t.Fatal(err)
	}
	if stream.Receive() || stream.Err() == nil {
		t.Fatal("removed workload retained active stream")
	}
	if _, err := client.PeerStatus(ctx, request()); err == nil {
		t.Fatal("removed workload opened another RPC")
	}
	if err := os.WriteFile(f.configs[1].ManifestFile, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.runtimes[1].ReloadMembership(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runtimes[1].ApprovedOrigins(); err == nil {
		t.Fatal("locally removed replica retained outbound authority")
	}
}

func TestPeerSecurityRefusesRollbackAcrossProcessReconstruction(t *testing.T) {
	f := newPeerSecurityFixture(t)
	// Use a third independent state path so live fixture ownership is preserved.
	config := f.configs[0]
	config.StateFile = filepath.Join(t.TempDir(), "restart-floor")
	runtime, cleanup, err := provider.NewPeerIdentityRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	f.manifest.Version++
	newer := peerSecuritySign(t, f.manifest, f.key)
	if err := os.WriteFile(config.ManifestFile, newer, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ReloadMembership(); err != nil {
		t.Fatal(err)
	}
	cleanup()
	f.manifest.Version--
	if err := os.WriteFile(config.ManifestFile, peerSecuritySign(t, f.manifest, f.key), 0600); err != nil {
		t.Fatal(err)
	}
	config.StateMode = "resume"
	if runtime, cleanup, err := provider.NewPeerIdentityRuntime(config); err == nil || runtime != nil || cleanup != nil {
		t.Fatal("operator file rolled persisted floor backward", err)
	}
	if err := os.WriteFile(config.ManifestFile, newer, 0600); err != nil {
		t.Fatal(err)
	}
	runtime, cleanup, err = provider.NewPeerIdentityRuntime(config)
	if err != nil {
		t.Fatal("current signed checkpoint restart failed", err)
	}
	defer cleanup()
	if _, err := runtime.ApprovedOrigins(); err != nil {
		t.Fatal(err)
	}
}

func TestPeerSecurityIdleSubscribeFlushesHeadersWithoutAnEvent(t *testing.T) {
	f := newPeerSecurityFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	client := graphv1connect.NewLanternReplicationServiceClient(f.runtimes[1].HTTPClient(), f.servers[0].URL)
	// Bound only the opening handshake. An empty log must not need a Mutation
	// to finish Subscribe, nor should opening fabricate a protobuf event.
	opening := time.AfterFunc(500*time.Millisecond, cancel)
	stream, err := client.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{NamespaceFormat: "namespaced-v1", AcceptReceiptEnvelopes: true}))
	opening.Stop()
	if err != nil {
		t.Fatal("empty private subscription did not open promptly", err)
	}
	defer func() { _ = stream.Close() }()
	first := make(chan bool, 1)
	go func() { first <- stream.Receive() }()
	select {
	case <-first:
		t.Fatal("idle subscription emitted a synthetic frame", stream.Err())
	case <-time.After(30 * time.Millisecond):
	}
	if _, err := f.primary[0].PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "data:first", Value: &pb.Vertex_String_{String_: "committed"}}}}); err != nil {
		t.Fatal(err)
	}
	select {
	case received := <-first:
		if !received {
			t.Fatal("first real mutation missing", stream.Err())
		}
		mutation := stream.Msg().GetMutation()
		if mutation == nil || mutation.Seq != 1 || len(mutation.GetOp().GetReplicatedPutVertices().GetEntries()) != 1 || mutation.GetOp().GetReplicatedPutVertices().Entries[0].GetLive().GetKey() != "data:first" {
			t.Fatal("mutation/cursor changed", stream.Msg())
		}
	case <-ctx.Done():
		t.Fatal("real mutation was not delivered", ctx.Err())
	}
	cancel()
	if stream.Receive() || connect.CodeOf(stream.Err()) != connect.CodeCanceled {
		t.Fatal("cancel did not release the idle stream", stream.Err())
	}
	rejected, err := client.Subscribe(t.Context(), connect.NewRequest(&pb.SubscribeRequest{NamespaceFormat: "namespaced-v1", FromSeqPerOrigin: map[string]uint64{"malformed": 1}, AcceptReceiptEnvelopes: true}))
	if err == nil {
		defer func() { _ = rejected.Close() }()
		if rejected.Receive() || connect.CodeOf(rejected.Err()) != connect.CodeInvalidArgument {
			t.Fatal("malformed cursor admitted", rejected.Err())
		}
	} else if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal(err)
	}
}

// Origin authorization is covered by the OIDC Head-managed real-wire cases.
// Here the same immutable accepted effects cross the operator mTLS peer plane,
// including an actual Subscribe gap repaired by private Snapshot.
func TestPeerSecurityEdgeOnlyHistorySubscribeSnapshotAndTombstoneRealConnect(t *testing.T) {
	var graphs [2]*graphcache.GraphCache[string, *pb.Vertex]
	var snapshots atomic.Int32
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var writer [32]byte
	copy(writer[:], public)
	f := newPeerSecurityFixture(t, peerSecurityOptions{Generation: [16]byte{6}, WriterKey: writer, Mount: func(f *peerSecurityFixture, index int, runtime *service.ServingRuntime, mux *http.ServeMux) {
		graphs[index] = runtime.GraphCache()
		graphs[index].RetainDanglingEdgeHistory()
		f.primary[index].WithTombstoneTTL(time.Hour)
		if index == 0 {
			peer, err := runtime.NewLanternReplicationService(f.primary[index])
			if err != nil {
				t.Fatal(err)
			}
			_, handler := graphv1connect.NewLanternReplicationServiceHandler(service.NewLanternReplicationServiceConnectHandler(peer))
			mux.Handle("/graph.v1.LanternReplicationService/Snapshot", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { snapshots.Add(1); handler.ServeHTTP(w, r) }))
		}
	}})
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	origin := hlc.NodeID{9}
	apply := func(seq uint64, op *pb.MutationOp) {
		t.Helper()
		ts := &pb.HLCTimestamp{WallNs: time.Now().UnixNano(), NodeId: origin[:]}
		if err := f.primary[0].ApplyMutation(ctx, &pb.Mutation{NamespaceFormat: "namespaced-v1", Seq: seq, Origin: origin[:], Hlc: ts, Op: op}); err != nil {
			t.Fatal(err)
		}
	}
	apply(1, &pb.MutationOp{NoEndpointCreation: true, Op: &pb.MutationOp_PutEdge{PutEdge: &pb.PutEdgeRequest{Edge: &pb.Edge{Tail: "data:tail", Head: "data:head", Weight: 2}}}})
	apply(2, &pb.MutationOp{NoEndpointCreation: true, Op: &pb.MutationOp_AddEdge{AddEdge: &pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "data:tail", Head: "data:head", Weight: 3}}}})
	transport, err := provider.NewWorkloadPeerTransport(f.runtimes[1])
	if err != nil {
		t.Fatal(err)
	}
	start := func() (context.CancelFunc, <-chan error) {
		pctx, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		pump := replication.NewPump(replication.Config{NodeID: hlc.NodeID{4}, NamespaceFormat: "namespaced-v1", Peers: []string{f.servers[0].URL}, PeerTransport: transport, BackoffMin: 10 * time.Millisecond, BackoffMax: 20 * time.Millisecond}, f.primary[1], graphs[1])
		go func() { done <- pump.Run(pctx) }()
		return stop, done
	}
	wait := func(message string, condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !condition() && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if !condition() {
			t.Fatal(message)
		}
	}
	stop, done := start()
	wait("private Subscribe did not apply sources", func() bool { return f.primary[1].LocalSeq(origin) == 2 })
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	assertHiddenHistory := func() {
		t.Helper()
		if _, ok := graphs[1].GetVertex("data:head"); ok {
			t.Fatal("peer fabricated/revived head")
		}
		if _, ok := graphs[1].GetWeight("data:tail", "data:head"); ok {
			t.Fatal("dangling Edge exposed")
		}
		rows := graphs[1].SnapshotReplication().Graph.Edges
		if len(rows) != 1 || len(rows[0].Contributions) != 2 {
			t.Fatal("private accepted history lost", rows)
		}
	}
	assertHiddenHistory()
	for _, graph := range graphs {
		gctx, gcancel := context.WithCancel(ctx)
		ended := make(chan struct{})
		graph.SetGCHooks(nil, func(time.Duration) { gcancel() })
		go func() { defer close(ended); graph.Watch(gctx, time.Millisecond) }()
		select {
		case <-ended:
		case <-ctx.Done():
			gcancel()
			t.Fatal(ctx.Err())
		}
	}
	assertHiddenHistory()
	// Evict the original relay log window. The next pump must use Snapshot,
	// whose graph contains pending sources but neither missing endpoint.
	for i := range 20 {
		if _, err := f.primary[0].PutVertex(ctx, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: fmt.Sprintf("data:noise:%02d", i)}}); err != nil {
			t.Fatal(err)
		}
	}
	stop, done = start()
	wait("private Snapshot did not repair the gap", func() bool { return snapshots.Load() > 0 && f.primary[1].LocalSeq(hlc.NodeID{3}) == 20 })
	assertHiddenHistory()
	for _, key := range []string{"data:tail", "data:head"} {
		if _, err := f.primary[0].PutVertex(ctx, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: key}}); err != nil {
			t.Fatal(err)
		}
	}
	wait("explicit endpoints did not expose original converged sources", func() bool { weight, ok := graphs[1].GetWeight("data:tail", "data:head"); return ok && weight == 5 })
	if _, err := f.primary[0].DeleteVertex(ctx, &pb.DeleteVertexRequest{Key: "data:head"}); err != nil {
		t.Fatal(err)
	}
	wait("Vertex tombstone did not replicate", func() bool { return f.primary[1].LocalSeq(hlc.NodeID{3}) == 23 })
	apply(3, &pb.MutationOp{NoEndpointCreation: true, Op: &pb.MutationOp_AddEdge{AddEdge: &pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "data:tail", Head: "data:head", Weight: 1}}}})
	wait("post-tombstone Edge-only Add did not replicate", func() bool { return f.primary[1].LocalSeq(origin) == 3 })
	if _, ok := graphs[1].GetVertex("data:head"); ok {
		t.Fatal("accepted Edge revived tombstoned head")
	}
	if _, ok := graphs[1].GetWeight("data:tail", "data:head"); ok {
		t.Fatal("post-tombstone dangling Edge exposed")
	}
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
