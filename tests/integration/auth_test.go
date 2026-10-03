package integration_test

import (
	"bytes"
	"context"
	"errors"
	"math"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/replication"
	"github.com/anaregdesign/lantern/server/service"
)

const testToken = "integration-s3cret"

// The reserved system image is private even before the full public-key mapper
// is installed. A same-named client vertex is a distinct data record.
func TestAuth_ReservedSystemImageRemainsPrivateOverConnect(t *testing.T) {
	cache := provider.NewGraphCache(provider.CacheConfig{TTL: time.Minute}, provider.SearchConfig{Enabled: true, Positions: true})
	metadata, err := cache.EnableSystemMetadata("sys:security:revision", 1024)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := metadata.Prepare([32]byte{}, 1, []byte("hidden systemword"))
	if err != nil {
		t.Fatal(err)
	}
	stage.Commit()
	svc := service.NewLanternService(cache).WithDataNamespace().WithSearchLimits(service.SearchLimits{Enabled: true, PositionsEnabled: true}).
		WithCapacityLimits(service.CapacityLimits{MaxVertices: 1})
	srv := newConnectTestServer(t, svc, nil)
	l := newConnectClientFor(t, srv.url)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := l.GetVertex(ctx, metadata.Key()); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("system key lookup: %v", err)
	}
	if count, err := l.CountVerticesByPrefix(ctx, ""); err != nil || count != 0 {
		t.Fatalf("system image counted as data: %d %v", count, err)
	}
	if _, err := l.PutVertex(ctx, metadata.Key(), "public clientword", time.Minute); err != nil {
		t.Fatal("system reserve consumed the data capacity", err)
	}
	value, err := l.GetVertex(ctx, metadata.Key())
	if err != nil || value.GetString_() != "public clientword" {
		t.Fatalf("same-named client record: %v %v", value, err)
	}
	raw := graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)
	before, err := raw.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: "clientword"}))
	if err != nil || len(before.Msg.GetHits()) != 1 {
		t.Fatal("client search failed", err)
	}
	stage, err = metadata.Prepare(metadata.Snapshot().Digest, 2, []byte("changed systemword systemword"))
	if err != nil {
		t.Fatal(err)
	}
	stage.Commit()
	after, err := raw.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: "clientword"}))
	if err != nil || len(after.Msg.GetHits()) != 1 || after.Msg.GetHits()[0].GetScore() != before.Msg.GetHits()[0].GetScore() {
		t.Fatal("system-only change affected public ranking", err)
	}
	hidden, err := raw.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{
		Query: "systemword", Options: &pb.SearchOptions{MatchMode: pb.MatchMode_MATCH_MODE_ALL},
	}))
	if err != nil || len(hidden.Msg.GetHits()) != 0 {
		t.Fatal("system content entered business search", err)
	}
	var dump bytes.Buffer
	stats, err := l.Backup(ctx, &dump, client.WithBackupFormat(client.FormatNDJSON))
	if err != nil || stats.Vertices != 1 || bytes.Contains(dump.Bytes(), []byte("systemword")) {
		t.Fatalf("public export leaked reserved image: %+v %v", stats, err)
	}
	if deleted, err := l.DeleteVerticesByPrefix(ctx, "sys:"); err != nil || deleted != 1 {
		t.Fatalf("data prefix delete: %d %v", deleted, err)
	}
	if len(metadata.Snapshot().Value) == 0 || metadata.Snapshot().Revision != 2 {
		t.Fatal("ordinary data deletion removed system image")
	}
}

func TestAuth_DataNamespacePublicConnectBoundary(t *testing.T) {
	cache := provider.NewGraphCache(provider.CacheConfig{TTL: time.Hour}, provider.SearchConfig{})
	svc := service.NewLanternService(cache).WithDataNamespace()
	srv := newConnectTestServer(t, svc, nil)
	raw := graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	keys := []string{"users:user1", "sys:users:user1", "data:users:user1", "日本語:ユーザ"}
	vertices := make([]*pb.Vertex, len(keys))
	for i, key := range keys {
		vertices[i] = &pb.Vertex{Key: key, Value: &pb.Vertex_String_{String_: "sys:literal-value"}}
	}
	if _, err := raw.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: vertices})); err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if value, live := cache.GetVertex("data:" + key); !live || value.GetKey() != "data:"+key || value.GetString_() != "sys:literal-value" {
			t.Fatalf("physical data identity/value: %q", key)
		}
	}
	physicalKeys := make(map[string]bool, len(keys))
	for _, key := range keys {
		physicalKeys["data:"+key] = true
	}
	for _, vertex := range cache.SnapshotVertices() {
		if !physicalKeys[vertex.Key] {
			t.Fatalf("unexpected physical identity: %q", vertex.Key)
		}
	}
	got, err := raw.GetVertices(ctx, connect.NewRequest(&pb.GetVerticesRequest{Keys: append(append([]string(nil), keys...), "sys:missing")}))
	if err != nil || len(got.Msg.GetVertices()) != len(keys) || !reflect.DeepEqual(got.Msg.GetMissing(), []string{"sys:missing"}) {
		t.Fatalf("logical plural read: %v %v", got, err)
	}
	for _, value := range got.Msg.GetVertices() {
		if !strings.HasPrefix(value.GetString_(), "sys:") {
			t.Fatal("ordinary string value was namespace-converted")
		}
	}
	if _, err := raw.PutEdge(ctx, connect.NewRequest(&pb.PutEdgeRequest{Edge: &pb.Edge{Tail: keys[0], Head: keys[1], Weight: 2}})); err != nil {
		t.Fatal(err)
	}
	edge, err := raw.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{Tail: keys[0], Head: keys[1]}))
	if err != nil || edge.Msg.GetEdge().GetTail() != keys[0] || edge.Msg.GetEdge().GetHead() != keys[1] {
		t.Fatalf("edge endpoint decoding: %v", err)
	}
	first, err := raw.ScanVertexKeys(ctx, connect.NewRequest(&pb.ScanVertexKeysRequest{Limit: 1}))
	if err != nil || len(first.Msg.GetKeys()) != 1 || len(first.Msg.GetNextCursor()) == 0 {
		t.Fatalf("empty prefix first page: %v", err)
	}
	second, err := raw.ScanVertexKeys(ctx, connect.NewRequest(&pb.ScanVertexKeysRequest{Limit: 10, Cursor: first.Msg.GetNextCursor()}))
	if err != nil || len(second.Msg.GetKeys()) != len(keys)-1 {
		t.Fatalf("logical cursor continuation: %v", err)
	}
	if _, err := raw.ScanVertexKeys(ctx, connect.NewRequest(&pb.ScanVertexKeysRequest{Prefix: "sys:", Cursor: first.Msg.GetNextCursor()})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("cursor scope substitution accepted: %v", err)
	}
	if _, err := raw.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "safe"}, {Key: ""}}})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid mixed batch: %v", err)
	}
	if _, live := cache.GetVertex("data:safe"); live {
		t.Fatal("invalid mixed batch partially committed")
	}
	if _, err := raw.DeleteVerticesByPrefix(ctx, connect.NewRequest(&pb.DeleteVerticesByPrefixRequest{})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("empty destructive prefix guard: %v", err)
	}
	count, err := raw.CountVerticesByPrefix(ctx, connect.NewRequest(&pb.CountVerticesByPrefixRequest{Prefix: "sys:"}))
	if err != nil || count.Msg.GetCount() != 1 {
		t.Fatalf("literal sys prefix count: %v", err)
	}
}

func TestAuth_DataNamespaceReceiptReplayOverConnect(t *testing.T) {
	config := durableReceiptWireConfig(filepath.Join(t.TempDir(), "receipts.wal"), hlc.NodeID{0x49})
	config.NamespaceFormat = "namespaced-v1"
	runtime, err := service.CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	primary := runtime.NewLanternService(nil).WithDataNamespace().WithTombstoneTTL(2 * time.Hour)
	rep, err := runtime.NewLanternReplicationService(primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CertifyInstallation(primary, rep); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CertifyReceiptBackup(primary, rep); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ActivatePublicReceipts(primary, rep); err != nil {
		t.Fatal(err)
	}
	server := newConnectTestServer(t, primary, rep, provider.NewAuthInterceptor(provider.AuthConfig{Tokens: []string{testToken}}))
	sdk := newConnectClientFor(t, server.url, client.WithAuthToken(testToken))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	capability, err := sdk.GetReceiptCapability(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id, err := client.NewReceiptOperationID(capability.Continuity.Epoch, time.Now(), [24]byte{0x51})
	if err != nil {
		t.Fatal(err)
	}
	receiptContext := client.ReceiptContext{Mutation: client.ReceiptMutationPutVertex, Continuity: capability.Continuity,
		GroupID: client.ReceiptGroupID{0x52}, OperationIDs: []client.ReceiptOperationID{id}}
	for range 2 {
		result, err := sdk.PutVertexWithReceipt(ctx, "sys:client", "logical intent", time.Hour, receiptContext)
		if err != nil || result.Outcome != client.PutOutcomeAppliedAndLive {
			t.Fatalf("namespaced receipt Put/replay: %v", err)
		}
	}
	if got, live := runtime.GraphCache().GetVertex("data:sys:client"); !live || got.GetString_() != "logical intent" || primary.LocalSeq(config.NodeID) != 1 {
		t.Fatal("receipt replay duplicated or misclassified the graph effect")
	}
	status, err := sdk.GetReceiptStatus(ctx, id)
	if err != nil || status.State != client.ReceiptConfirmed || status.Receipt == nil {
		t.Fatalf("namespaced receipt status: %v", err)
	}
	if _, err := sdk.PutVertexWithReceipt(ctx, "data:sys:client", "logical intent", time.Hour, receiptContext); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("distinct logical identity reused receipt intent: %v", err)
	}
	if _, live := runtime.GraphCache().GetVertex("data:data:sys:client"); live {
		t.Fatal("conflicting receipt created a second data identity")
	}
}

func TestAuth_ModePreflightAndAnonymousConnect(t *testing.T) {
	for _, setting := range os.Environ() {
		name, _, _ := strings.Cut(setting, "=")
		if strings.HasPrefix(name, "LANTERN_AUTH_") || strings.HasPrefix(name, "LANTERN_OIDC_") || strings.HasPrefix(name, "LANTERN_SECURITY_") {
			t.Setenv(name, "")
			if err := os.Unsetenv(name); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "unset", true: "explicit off"}[explicit], func(t *testing.T) {
			if explicit {
				t.Setenv("LANTERN_AUTH_MODE", "off")
			}
			cfg, err := provider.NewConfig()
			if err != nil || cfg.Auth.Enabled() {
				t.Fatalf("anonymous config: %v", err)
			}
			cache := provider.NewGraphCache(cfg.Cache, cfg.Search)
			srv := newConnectTestServer(t, service.NewLanternService(cache), nil)
			l := newConnectClientFor(t, srv.url)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := l.PutVertex(ctx, "anonymous:1", "value", time.Minute); err != nil {
				t.Fatal(err)
			}
			if _, err := l.GetVertex(ctx, "anonymous:1"); err != nil {
				t.Fatal(err)
			}
			if _, err := l.GetVertex(ctx, "missing"); connect.CodeOf(err) != connect.CodeNotFound {
				t.Fatalf("missing contract: %v", err)
			}
		})
	}
	t.Run("OIDC settings never fall through to OFF", func(t *testing.T) {
		t.Setenv("LANTERN_OIDC_ADMIN_ISSUER", "https://idp.example")
		if cfg, err := provider.NewConfig(); err == nil || cfg != nil {
			t.Fatal("partial OIDC configuration produced an anonymous serving config")
		}
		t.Setenv("LANTERN_AUTH_MODE", "oidc")
		if cfg, err := provider.NewConfig(); !errors.Is(err, provider.ErrOIDCRuntimeUnavailable) || cfg != nil {
			t.Fatalf("staged OIDC runtime: %v", err)
		}
	})
}

// newAuthedServer stands up an in-process Lantern (data plane + replication
// service) with the #850 bearer-token interceptor armed, mirroring how the
// production listener mounts it.
func newAuthedServer(t *testing.T) (*connectTestServer, *graphcache.GraphCache[string, *pb.Vertex], *service.LanternService) {
	t.Helper()
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	log := mutationlog.New(mutationlog.Options{Capacity: 1024, SubscriberBuffer: 1024})
	var nid hlc.NodeID
	copy(nid[:], "auth-node-000000")
	clock := hlc.New(nid, hlc.Options{})
	svc := service.NewLanternService(cache).WithReplication(log, clock, nil)
	rep := service.NewLanternReplicationService(log, cache, clock).WithOriginStates(svc)
	auth := provider.NewAuthInterceptor(provider.AuthConfig{Tokens: []string{"stale-rotated-out", testToken}})
	srv := newConnectTestServer(t, svc, rep, auth)
	return srv, cache, svc
}

// TestAuth_SDKRoundTrip drives the #850 contract through the SDK: a
// tokenless client is rejected with Unauthenticated on unary AND the
// replication streams; WithAuthToken fixes both; rotation admits any
// configured token; failover inherits the option.
func TestAuth_SDKRoundTrip(t *testing.T) {
	srv, _, _ := newAuthedServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.Run("tokenless unary rejected", func(t *testing.T) {
		l := newConnectClientFor(t, srv.url)
		_, err := l.PutVertex(ctx, "k", "v", time.Minute)
		if !errors.Is(err, client.ErrUnauthenticated) && connectCode(err) != connect.CodeUnauthenticated {
			t.Fatalf("tokenless put: got %v, want Unauthenticated", err)
		}
	})

	t.Run("tokenless replication stream rejected", func(t *testing.T) {
		// The replication service must NOT ride the health exemption: a
		// tokenless Subscribe fails at first receive.
		raw := graphv1connect.NewLanternReplicationServiceClient(h2cClient(), srv.url)
		stream, err := raw.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{}))
		if err == nil {
			if stream.Receive() {
				t.Fatal("tokenless Subscribe delivered a message")
			}
			err = stream.Err()
			_ = stream.Close()
		}
		if connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("tokenless Subscribe: got %v (err=%v), want Unauthenticated", connect.CodeOf(err), err)
		}
	})

	t.Run("token accepted end to end", func(t *testing.T) {
		l := newConnectClientFor(t, srv.url, client.WithAuthToken(testToken))
		if _, err := l.PutVertex(ctx, "authed", "v", time.Minute); err != nil {
			t.Fatalf("authed put: %v", err)
		}
		if _, err := l.GetVertex(ctx, "authed"); err != nil {
			t.Fatalf("authed get: %v", err)
		}
		var fullSeen bool
		for event, err := range l.Subscribe(ctx, nil) {
			if err != nil {
				t.Fatalf("authed full Subscribe: %v", err)
			}
			fullSeen = event != nil
			break
		}
		if !fullSeen {
			t.Fatal("authed full Subscribe returned no Mutation")
		}
		var checkpointSeen bool
		for event, err := range l.BootstrapIdentity(ctx) {
			if err != nil {
				t.Fatalf("authed identity Subscribe: %v", err)
			}
			_, checkpointSeen = event.(*client.IdentityCheckpoint)
			break
		}
		if !checkpointSeen {
			t.Fatal("authed identity Subscribe returned no checkpoint")
		}
	})

	t.Run("tokenless identity stream rejected", func(t *testing.T) {
		l := newConnectClientFor(t, srv.url)
		var streamErr error
		for _, err := range l.BootstrapIdentity(ctx) {
			streamErr = err
			break
		}
		if !errors.Is(streamErr, client.ErrUnauthenticated) || connect.CodeOf(streamErr) != connect.CodeUnauthenticated {
			t.Fatalf("tokenless identity Subscribe = %v", streamErr)
		}
	})

	t.Run("rotation: stale-but-configured token accepted", func(t *testing.T) {
		l := newConnectClientFor(t, srv.url, client.WithAuthToken("stale-rotated-out"))
		if _, err := l.PutVertex(ctx, "rotated", "v", time.Minute); err != nil {
			t.Fatalf("rotation token put: %v", err)
		}
	})

	t.Run("failover inherits the token", func(t *testing.T) {
		lf, err := client.NewLanternFailover([]string{srv.url},
			client.WithHTTPClient(h2cClient()), client.WithAuthToken(testToken))
		if err != nil {
			t.Fatalf("NewLanternFailover: %v", err)
		}
		t.Cleanup(func() { _ = lf.Close() })
		if _, err := lf.PutVertex(ctx, "via-failover", "v", time.Minute); err != nil {
			t.Fatalf("failover put: %v", err)
		}
	})
}

func TestAuth_RawSingularPutEdgeRejectsNonFiniteSourceOverH2C(t *testing.T) {
	nodeID := hlc.NodeID{0x7a}
	now := time.Now()
	runtime, err := service.CreateDurableReceiptWALServingRuntime(service.DurableReceiptWALRuntimeConfig{
		Path: filepath.Join(t.TempDir(), "receipts.wal"),
		Receipt: mutationreceipt.Config{
			Epoch: mutationreceipt.Epoch{0x7a}, Retention: time.Hour,
			MaxEntries: 32, MaxBytes: 1 << 20, ClockHighWater: now,
		},
		Log:        mutationlog.Options{Capacity: 16, SubscriberBuffer: 16},
		DefaultTTL: time.Hour,
		ConfigureGraph: func(graph *graphcache.GraphCache[string, *pb.Vertex]) error {
			provider.ConfigureGraphCache(graph, provider.CacheConfig{TTL: time.Hour}, provider.SearchConfig{})
			return nil
		},
		NodeID:        nodeID,
		Now:           now,
		BaselineCodec: backup.ReceiptBaselineCodec{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("close durable runtime: %v", err)
		}
	})
	primary := runtime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
	replicationService, err := runtime.NewLanternReplicationService(primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CertifyInstallation(primary, replicationService); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CertifyReceiptBackup(primary, replicationService); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ActivatePublicReceipts(primary, replicationService); err != nil {
		t.Fatal(err)
	}
	srv := newConnectTestServer(
		t, primary, replicationService,
		provider.NewAuthInterceptor(provider.AuthConfig{Tokens: []string{testToken}}),
		provider.NewValidationInterceptor(defaultIntegrationValidationLimits()).ConnectInterceptor(),
	)
	raw := graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)
	ctx := t.Context()
	authorizedPutEdge := func(edge *pb.Edge) *connect.Request[pb.PutEdgeRequest] {
		req := connect.NewRequest(&pb.PutEdgeRequest{Edge: edge})
		req.Header().Set("Authorization", "Bearer "+testToken)
		return req
	}
	authorizedPutEdges := func(edges []*pb.Edge) *connect.Request[pb.PutEdgesRequest] {
		req := connect.NewRequest(&pb.PutEdgesRequest{Edges: edges})
		req.Header().Set("Authorization", "Bearer "+testToken)
		return req
	}
	if _, err := raw.PutEdge(ctx, connect.NewRequest(&pb.PutEdgeRequest{
		Edge: &pb.Edge{Tail: "singular", Head: "edge", Weight: 1},
	})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("tokenless raw PutEdge = %v, want Unauthenticated", err)
	}

	beforeReceipts := runtime.ReceiptStats()
	beforeOrigins := primary.OriginStates()
	beforeLog, beforeCapacity, beforeEvicted := runtime.MutationLogStats()
	assertUnchanged := func(t *testing.T) {
		t.Helper()
		length, capacity, evicted := runtime.MutationLogStats()
		if runtime.ReceiptStats() != beforeReceipts ||
			!reflect.DeepEqual(primary.OriginStates(), beforeOrigins) ||
			primary.LocalSeq(nodeID) != 0 ||
			length != beforeLog || capacity != beforeCapacity || evicted != beforeEvicted ||
			runtime.GraphCache().VertexCount() != 0 || runtime.GraphCache().EdgeCount() != 0 {
			t.Fatalf("rejected source changed receipts/origin/log/graph: receipt=%+v origin=%+v log=%d/%d/%d vertices=%d edges=%d",
				runtime.ReceiptStats(), primary.OriginStates(), length, capacity, evicted,
				runtime.GraphCache().VertexCount(), runtime.GraphCache().EdgeCount())
		}
	}
	for _, weight := range []struct {
		name  string
		value float32
	}{
		{"NaN", float32(math.NaN())},
		{"positive infinity", float32(math.Inf(1))},
		{"negative infinity", float32(math.Inf(-1))},
	} {
		t.Run("singular/"+weight.name, func(t *testing.T) {
			// SDK PutEdge conveniences call PutEdges; the generated client
			// exercises the singular RPC and its own interceptor path.
			_, err := raw.PutEdge(ctx, authorizedPutEdge(&pb.Edge{
				Tail: "singular", Head: "edge", Weight: weight.value,
			}))
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("raw PutEdge = %v, want InvalidArgument", err)
			}
			assertUnchanged(t)
		})
		t.Run("plural/"+weight.name, func(t *testing.T) {
			_, err := raw.PutEdges(ctx, authorizedPutEdges([]*pb.Edge{
				{Tail: "prefix", Head: "edge", Weight: 1},
				{Tail: "plural", Head: "edge", Weight: weight.value},
			}))
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("raw PutEdges = %v, want InvalidArgument", err)
			}
			assertUnchanged(t)
		})
	}
	put, err := raw.PutEdge(ctx, authorizedPutEdge(&pb.Edge{
		Tail: "singular", Head: "edge", Weight: 2.5,
	}))
	if err != nil || put.Msg.GetOutcome() != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
		t.Fatalf("finite raw PutEdge = %+v, %v", put, err)
	}
	plural, err := raw.PutEdges(ctx, authorizedPutEdges([]*pb.Edge{
		{Tail: "plural", Head: "edge", Weight: math.MaxFloat32},
	}))
	if err != nil || len(plural.Msg.GetOutcomes()) != 1 ||
		plural.Msg.GetOutcomes()[0] != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
		t.Fatalf("finite raw PutEdges = %+v, %v", plural, err)
	}
	if got, live := runtime.GraphCache().GetWeight("singular", "edge"); !live || got != 2.5 {
		t.Fatalf("finite singular edge = %v, live=%t", got, live)
	}
	if got, live := runtime.GraphCache().GetWeight("plural", "edge"); !live || got != math.MaxFloat32 {
		t.Fatalf("finite plural edge = %v, live=%t", got, live)
	}
	length, _, _ := runtime.MutationLogStats()
	if primary.LocalSeq(nodeID) != 2 || length != beforeLog+2 || runtime.ReceiptStats() != beforeReceipts {
		t.Fatalf("finite writes did not publish exactly twice: origin=%d log=%d receipts=%+v",
			primary.LocalSeq(nodeID), length, runtime.ReceiptStats())
	}
}

// TestAuth_PumpReplicatesAgainstAuthedPeer pins the peer-credential path:
// a pump replicates through verified TLS to an auth-enabled peer.
func TestAuth_PumpReplicatesAgainstAuthedPeer(t *testing.T) {
	srcSrv, _, srcSvc := newAuthedServer(t)
	peer := newAuthenticatedReplicationPeer(t, srcSrv, testToken)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Local (destination) node — plain, no auth needed on its own surface.
	dstCache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	dstLog := mutationlog.New(mutationlog.Options{Capacity: 1024, SubscriberBuffer: 1024})
	var dstID hlc.NodeID
	copy(dstID[:], "auth-node-dst000")
	dstClock := hlc.New(dstID, hlc.Options{})
	dstSvc := service.NewLanternService(dstCache).WithReplication(dstLog, dstClock, nil)

	p := replication.NewPump(replication.Config{
		NodeID:        dstID,
		Peers:         []string{peer.url},
		BackoffMin:    20 * time.Millisecond,
		BackoffMax:    200 * time.Millisecond,
		PeerTransport: peer.transport,
	}, dstSvc, dstCache)
	pumpCtx, pumpCancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { _ = p.Run(pumpCtx); close(done) }()
	t.Cleanup(func() {
		pumpCancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	})

	// Write on the source THROUGH its authed surface; the pump must carry
	// it to the destination.
	if _, err := srcSvc.PutVertex(ctx, &pb.PutVertexRequest{Vertex: &pb.Vertex{
		Key: "replicated", Value: &pb.Vertex_Nil{Nil: true},
	}}); err != nil {
		t.Fatalf("source put: %v", err)
	}
	if !waitForVertex(t, dstCache, "replicated", 5*time.Second) {
		t.Fatal("authed pump did not replicate the vertex")
	}
}

func TestAuth_DNSPeerAuthenticatesResolvedIPOverTLS(t *testing.T) {
	srcSrv, _, srcSvc := newAuthedServer(t)
	peer := newAuthenticatedReplicationPeer(t, srcSrv, testToken)
	addr := strings.TrimPrefix(peer.url, "https://")
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	transport, err := replication.NewAuthenticatedPeerTransport(
		peer.ca, nil, testToken, nil, "example.com", port,
	)
	if err != nil {
		t.Fatal(err)
	}
	var dstID hlc.NodeID
	copy(dstID[:], "auth-node-dns000")
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	log := mutationlog.New(mutationlog.Options{Capacity: 1024, SubscriberBuffer: 1024})
	clock := hlc.New(dstID, hlc.Options{})
	svc := service.NewLanternService(cache).WithReplication(log, clock, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pump := replication.NewPump(replication.Config{
		NodeID: dstID, Peers: []string{addr},
		BackoffMin: 20 * time.Millisecond, BackoffMax: 200 * time.Millisecond,
		PeerTransport: transport,
	}, svc, cache)
	done := make(chan error, 1)
	go func() { done <- pump.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("stop DNS pump: %v", err)
		}
	}()
	if _, err := srcSvc.PutVertex(ctx, &pb.PutVertexRequest{Vertex: &pb.Vertex{
		Key: "dns-peer", Value: &pb.Vertex_Nil{Nil: true},
	}}); err != nil {
		t.Fatal(err)
	}
	if !waitForVertex(t, cache, "dns-peer", 4*time.Second) {
		t.Fatal("DNS-discovered IP did not authenticate against the configured certificate DNS identity")
	}
}

func TestAuth_DNSPeerRejectsMismatchedIdentityOverTLS(t *testing.T) {
	srcSrv, _, srcSvc := newAuthedServer(t)
	peer := newAuthenticatedReplicationPeer(t, srcSrv, testToken)
	addr := strings.TrimPrefix(peer.url, "https://")
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	const wrongIdentity = "unrelated.invalid"
	transport, err := replication.NewAuthenticatedPeerTransport(
		peer.ca, nil, testToken, nil, wrongIdentity, port,
	)
	if err != nil {
		t.Fatal(err)
	}
	var dstID hlc.NodeID
	copy(dstID[:], "auth-node-bad-dns")
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	log := mutationlog.New(mutationlog.Options{Capacity: 1024, SubscriberBuffer: 1024})
	svc := service.NewLanternService(cache).WithReplication(log, hlc.New(dstID, hlc.Options{}), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := srcSvc.PutVertex(ctx, &pb.PutVertexRequest{Vertex: &pb.Vertex{
		Key: "must-not-replicate", Value: &pb.Vertex_Nil{Nil: true},
	}}); err != nil {
		t.Fatal(err)
	}
	pump := replication.NewPump(replication.Config{
		NodeID: dstID, Peers: []string{addr},
		BackoffMin: 20 * time.Millisecond, BackoffMax: 200 * time.Millisecond,
		PeerTransport: transport,
	}, svc, cache)
	done := make(chan error, 1)
	go func() { done <- pump.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("stop mismatched DNS pump: %v", err)
		}
	}()
	for ctx.Err() == nil {
		peers := pump.Snapshot()
		if len(peers) == 1 && strings.Contains(peers[0].LastError, "certificate") &&
			strings.Contains(peers[0].LastError, wrongIdentity) {
			if _, copied := cache.GetVertex("must-not-replicate"); copied {
				t.Fatal("untrusted DNS identity received a bearer and replicated")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("pump never reported a certificate mismatch: %+v", pump.Snapshot())
}

// connectCode unwraps the connect error code from an SDK error chain.
func connectCode(err error) connect.Code {
	var ce *connect.Error
	if errors.As(err, &ce) {
		return ce.Code()
	}
	return 0
}

// This fixture composes the new boundary explicitly while production OIDC
// remains guarded until data, browser and peer admission are installed.
