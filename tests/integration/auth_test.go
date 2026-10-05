package integration_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	cliservice "github.com/anaregdesign/lantern/cli/service"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/backup"
	domainmetrics "github.com/anaregdesign/lantern/server/metrics"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/replication"
	"github.com/anaregdesign/lantern/server/service"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"log/slog"
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

func TestAuth_DataNamespaceCursorErrorsAndEquivalentOrderRealConnect(t *testing.T) {
	cache := provider.NewGraphCache(provider.CacheConfig{TTL: time.Hour}, provider.SearchConfig{Enabled: true, Positions: true})
	svc := service.NewLanternService(cache).WithDataNamespace().WithSearchLimits(service.SearchLimits{Enabled: true, PositionsEnabled: true})
	srv := newConnectTestServer(t, svc, nil)
	raw := graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)
	if _, err := raw.PutVertices(t.Context(), connect.NewRequest(&pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "orders:a"}, {Key: "orders:b"}}})); err != nil {
		t.Fatal(err)
	}
	for _, keys := range []bool{false, true} {
		var cursor []byte
		if keys {
			first, err := raw.ScanVertexKeys(t.Context(), connect.NewRequest(&pb.ScanVertexKeysRequest{Prefix: "orders:", Limit: 1}))
			if err != nil {
				t.Fatal(err)
			}
			cursor = first.Msg.NextCursor
			second, err := raw.ScanVertexKeys(t.Context(), connect.NewRequest(&pb.ScanVertexKeysRequest{Prefix: "orders:", Limit: 1, Order: pb.ScanOrder_SCAN_ORDER_ASC, Cursor: cursor}))
			if err != nil || !reflect.DeepEqual(second.Msg.Keys, []string{"orders:b"}) {
				t.Fatal(second, err)
			}
		} else {
			first, err := raw.ScanVertices(t.Context(), connect.NewRequest(&pb.ScanVerticesRequest{Prefix: "orders:", Limit: 1}))
			if err != nil {
				t.Fatal(err)
			}
			cursor = first.Msg.NextCursor
			second, err := raw.ScanVertices(t.Context(), connect.NewRequest(&pb.ScanVerticesRequest{Prefix: "orders:", Limit: 1, Order: pb.ScanOrder_SCAN_ORDER_ASC, Cursor: cursor}))
			if err != nil || len(second.Msg.Vertices) != 1 || second.Msg.Vertices[0].Key != "orders:b" {
				t.Fatal(second, err)
			}
		}
		if _, err := raw.ScanVertices(t.Context(), connect.NewRequest(&pb.ScanVerticesRequest{Prefix: "orders:", Order: pb.ScanOrder_SCAN_ORDER_DESC, Cursor: cursor})); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatal("wrong scope accepted", err)
		}
	}
	_, err := raw.SearchVertices(t.Context(), connect.NewRequest(&pb.SearchVerticesRequest{Prefix: "orders:", Query: "term", Cursor: []byte{1}}))
	var ce *connect.Error
	if connect.CodeOf(err) != connect.CodeInvalidArgument || !errors.As(err, &ce) || len(ce.Details()) != 1 {
		t.Fatal("missing typed Search error", err)
	}
	detail, err := ce.Details()[0].Value()
	if err != nil || detail.(*pb.SearchErrorDetail).Reason != pb.SearchErrorReason_SEARCH_CURSOR_INVALID {
		t.Fatal(detail, err)
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
		if cfg, err := provider.NewConfig(); err == nil || cfg != nil {
			t.Fatalf("incomplete OIDC configuration: %v", err)
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

// This fixture exercises the certified production public handler composition
// with a trusted local HTTPS gateway and a deterministic authority clock.
type oidcControlWireFixture struct {
	client      graphv1connect.LanternSecurityServiceClient
	server      *httptest.Server
	provider    *httptest.Server
	private     ed25519.PrivateKey
	mu          sync.Mutex
	now         time.Time
	fetches     atomic.Int64
	codes       map[string]oidcWireCode
	graph       *graphcache.GraphCache[string, *pb.Vertex]
	data        graphv1connect.LanternServiceClient
	changes     graphv1connect.LanternChangeServiceClient
	dataRuntime *service.ServingRuntime
}

type oidcWireCode struct {
	nonce, challenge, subject string
	wrongNonce                bool
	authTime                  string
}

func newOIDCControlWireFixture(t *testing.T, traversalLimits ...service.TraversalLimits) *oidcControlWireFixture {
	return newOIDCControlWireFixtureConfigured(t, nil, traversalLimits...)
}

func newOIDCControlWireFixtureConfigured(t *testing.T, configure func(*provider.SecurityConfig), traversalLimits ...service.TraversalLimits) *oidcControlWireFixture {
	return newOIDCControlWireFixtureOptions(t, configure, false, traversalLimits...)
}

func newOIDCControlWireFixtureOptions(t *testing.T, configure func(*provider.SecurityConfig), durableReceipts bool, traversalLimits ...service.TraversalLimits) *oidcControlWireFixture {
	t.Helper()
	f := &oidcControlWireFixture{now: time.Now(), codes: make(map[string]oidcWireCode)}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f.private = private
	f.provider = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.fetches.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("metadata fetch forwarded credentials")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": f.provider.URL, "authorization_endpoint": f.provider.URL + "/authorize", "token_endpoint": f.provider.URL + "/token", "jwks_uri": f.provider.URL + "/jwks", "response_types_supported": []string{"code"}, "code_challenge_methods_supported": []string{"S256"}})
		case "/jwks":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "OKP", "kid": "key", "alg": "EdDSA", "crv": "Ed25519", "x": base64.RawURLEncoding.EncodeToString(public)}}})
		case "/token":
			if r.Method != http.MethodPost || r.ParseForm() != nil {
				http.Error(w, "invalid", http.StatusBadRequest)
				return
			}
			f.mu.Lock()
			code, known := f.codes[r.Form.Get("code")]
			delete(f.codes, r.Form.Get("code"))
			f.mu.Unlock()
			challenge := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			callback := sha256.Sum256([]byte(f.provider.URL))
			if !known || r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("client_id") != "admin" || r.Form.Get("redirect_uri") != fmt.Sprintf("https://admin.example/auth/callback/%x", callback) || base64.RawURLEncoding.EncodeToString(challenge[:]) != code.challenge {
				http.Error(w, "invalid", http.StatusBadRequest)
				return
			}
			now := f.clock()
			nonce := code.nonce
			if code.wrongNonce {
				nonce = "wrong-nonce"
			}
			accessHash := sha512.Sum512([]byte("provider-private-access-token"))
			claims := map[string]any{"iss": f.provider.URL, "sub": code.subject, "aud": "admin", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "auth_time": now.Unix(), "nonce": nonce, "at_hash": base64.RawURLEncoding.EncodeToString(accessHash[:32])}
			switch code.authTime {
			case "missing":
				delete(claims, "auth_time")
			case "stale":
				claims["auth_time"] = now.Add(-6 * time.Minute).Unix()
			case "future":
				claims["auth_time"] = now.Add(time.Second).Unix()
			case "contradictory":
				claims["iat"] = now.Add(-2 * time.Minute).Unix()
				claims["auth_time"] = now.Add(-time.Minute).Unix()
			case "null":
				claims["auth_time"] = nil
			}
			id := f.signedToken(claims, "JWT")
			_ = json.NewEncoder(w).Encode(map[string]any{"id_token": id, "access_token": "provider-private-access-token", "token_type": "Bearer"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.provider.Close)
	dir := t.TempDir()
	writerPublic, writerPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(writerPrivate)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(writerPublic)
	if err != nil {
		t.Fatal(err)
	}
	for name, block := range map[string]*pem.Block{"writer.key": {Type: "PRIVATE KEY", Bytes: privateDER}, "writer.pub": {Type: "PUBLIC KEY", Bytes: publicDER}, "provider.pem": {Type: "CERTIFICATE", Bytes: f.provider.Certificate().Raw}} {
		if err := createPrivateTestFile(filepath.Join(dir, name), pem.EncodeToMemory(block)); err != nil {
			t.Fatal(err)
		}
	}
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
	callback := sha256.Sum256([]byte(f.provider.URL))
	origins, _ := json.Marshal(map[string][]string{f.provider.URL: {"127.0.0.1/32"}})
	for name, value := range map[string]string{
		"LANTERN_AUTH_MODE": "oidc", "LANTERN_OIDC_ADMIN_ISSUER": f.provider.URL, "LANTERN_OIDC_ADMIN_SUBJECTS": `["admin","other"]`,
		"LANTERN_OIDC_CLIENT_ID": "admin", "LANTERN_OIDC_API_AUDIENCE": "api", "LANTERN_OIDC_ALGORITHMS": `["EdDSA"]`, "LANTERN_OIDC_BROWSER_ORIGIN": "https://admin.example",
		"LANTERN_OIDC_REDIRECT_URI": fmt.Sprintf("https://admin.example/auth/callback/%x", callback), "LANTERN_OIDC_ROOT_CA_FILE": filepath.Join(dir, "provider.pem"), "LANTERN_OIDC_PRIVATE_ORIGINS": string(origins),
		"LANTERN_OIDC_TRUSTED_PROXY_IPS": `["127.0.0.1"]`,
		"LANTERN_SECURITY_STORE_MODE":    "fresh", "LANTERN_SECURITY_STORE_PATH": filepath.Join(dir, "sys.wal"), "LANTERN_SECURITY_GENERATION": "01000000000000000000000000000000",
		"LANTERN_SECURITY_NODE_ROLE": "writer", "LANTERN_SECURITY_WRITER_ENDPOINT": "https://peer.example", "LANTERN_SECURITY_WRITER_KEY_FILE": filepath.Join(dir, "writer.key"), "LANTERN_SECURITY_WRITER_PUBLIC_KEY_FILE": filepath.Join(dir, "writer.pub"),
		"LANTERN_SECURITY_BOOTSTRAP_REVISION": "1", "LANTERN_SECURITY_CLOCK_QUALIFIED": "true",
	} {
		t.Setenv(name, value)
	}
	config, err := provider.LoadSecurityConfig()
	if err != nil {
		t.Fatal(err)
	}
	config.Clock = f.clock
	configureGraph := func(graph *graphcache.GraphCache[string, *pb.Vertex]) error {
		graph.EnablePrefixIndex(func(key string) string { return key })
		graph.EnableSearchIndex(func(key string, vertex *pb.Vertex) search.Document {
			return search.Fields{{ID: search.FieldKey, Text: strings.TrimPrefix(key, "data:")}, {ID: search.FieldValue, Text: vertex.GetString_()}}
		}, strings.Compare)
		return nil
	}
	var data *service.ServingRuntime
	if durableReceipts {
		durable := durableReceiptWireConfig(filepath.Join(dir, "receipts.wal"), hlc.NodeID{8})
		durable.NamespaceFormat = "namespaced-v1"
		durable.ConfigureGraph = configureGraph
		data, err = service.CreateDurableReceiptWALServingRuntime(durable)
	} else {
		graph := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
		if err := configureGraph(graph); err != nil {
			t.Fatal(err)
		}
		data, err = service.NewGraphOnlyServingRuntime(graph, mutationlog.New(mutationlog.Options{Capacity: 16}), hlc.New(hlc.NodeID{8}, hlc.Options{}), "namespaced-v1")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = data.Close() })
	f.dataRuntime = data
	f.graph = data.GraphCache()
	if configure != nil {
		configure(&config)
	}
	runtime, closeRuntime, err := provider.NewSecurityRuntime(config, data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeRuntime)
	f.advance(35 * time.Second)
	dataService := data.NewLanternService(nil).WithSearchLimits(service.SearchLimits{Enabled: true, PositionsEnabled: true})
	if durableReceipts {
		dataService.WithTombstoneTTL(time.Hour)
	}
	if len(traversalLimits) > 0 {
		dataService.WithTraversalLimits(traversalLimits[0])
	}
	peer, err := data.NewLanternReplicationService(dataService)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := provider.NewRuntimeRestored(data, dataService)
	if err != nil {
		t.Fatal(err)
	}
	limits := provider.NetConfig{MaxRecvMsgBytes: 8 << 20, MaxSendMsgBytes: 8 << 20}
	certified, err := provider.NewRuntimeCertified(data, dataService, peer, restored, limits)
	if err != nil {
		t.Fatal(err)
	}
	if durableReceipts {
		if err := data.CertifyReceiptBackup(dataService, peer); err != nil {
			t.Fatal(err)
		}
		if err := data.ActivatePublicReceipts(dataService, peer); err != nil {
			t.Fatal(err)
		}
	}
	publicCertified, err := provider.NewPublicSecurityCertified(runtime, provider.TLSConfig{}, nil, nil, data, dataService, certified)
	if err != nil {
		t.Fatal(err)
	}
	f.server = httptest.NewUnstartedServer(nil)
	listener, err := provider.NewPublicLanternListener(f.server.Listener, limits, provider.TLSConfig{}, provider.ObservabilityConfig{EnableReflection: true}, provider.CORSConfig{}, dataService, runtime,
		provider.ChangeConfig{Enabled: true, Options: service.ChangeServiceOptions{CursorKeys: []service.ChangeCursorKey{{Version: 1, Key: [32]byte{13}}}, CurrentKeyVersion: 1, Heartbeat: 100 * time.Millisecond}},
		nil, nil, nil, nil, nil, provider.NewHealthChecker(), slog.New(slog.NewTextHandler(io.Discard, nil)), publicCertified)
	if err != nil {
		t.Fatal(err)
	}
	f.server.Config.Handler = listener.Server().Handler
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	f.server.Config.Protocols = protocols
	f.server.Start()
	t.Cleanup(f.server.Close)
	f.client = graphv1connect.NewLanternSecurityServiceClient(&http.Client{Transport: authIngressRoundTripper{h2cClient().Transport}}, f.server.URL)
	f.data = graphv1connect.NewLanternServiceClient(&http.Client{Transport: authIngressRoundTripper{h2cClient().Transport}}, f.server.URL)
	f.changes = graphv1connect.NewLanternChangeServiceClient(&http.Client{Transport: authIngressRoundTripper{h2cClient().Transport}}, f.server.URL)
	return f
}
func (f *oidcControlWireFixture) clock() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.now }
func (f *oidcControlWireFixture) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}
func (f *oidcControlWireFixture) token(t *testing.T, subject string, mutate func(map[string]any)) string {
	t.Helper()
	now := f.clock()
	claims := map[string]any{"iss": f.provider.URL, "sub": subject, "aud": "api", "client_id": "client", "jti": "wire", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "auth_time": now.Unix()}
	if mutate != nil {
		mutate(claims)
	}
	return f.signedToken(claims, "at+jwt")
}
func (f *oidcControlWireFixture) signedToken(claims map[string]any, typ string) string {
	header, _ := json.Marshal(map[string]any{"alg": "EdDSA", "kid": "key", "typ": typ})
	payload, _ := json.Marshal(claims)
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	return input + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(f.private, []byte(input)))
}
func securityWireRequest[T any](token string, message *T) *connect.Request[T] {
	req := connect.NewRequest(message)
	if token != "" {
		req.Header().Set("Authorization", "Bearer "+token)
	}
	return req
}

func TestAuth_OIDCManagementRealConnect(t *testing.T) {
	f := newOIDCControlWireFixture(t)
	ctx := t.Context()
	capabilities, err := f.client.GetAuthCapabilities(ctx, connect.NewRequest(&pb.GetAuthCapabilitiesRequest{}))
	if err != nil || capabilities.Msg.Mode != pb.AuthMode_AUTH_MODE_OIDC || len(capabilities.Msg.LoginIssuers) != 1 {
		t.Fatal("capability boundary", err)
	}
	if _, err := f.client.ListRoles(ctx, connect.NewRequest(&pb.ListRolesRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("anonymous management", err)
	}
	admin := f.token(t, "admin", nil)
	principal, err := f.client.GetCurrentPrincipal(ctx, securityWireRequest(admin, &pb.GetCurrentPrincipalRequest{}))
	if err != nil || principal.Msg.Identity.Subject != "admin" || len(principal.Msg.Roles) != 1 || !principal.Msg.RecentAuthentication || principal.Msg.CsrfToken != "" {
		t.Fatal("verified principal", err)
	}
	initial := principal.Msg.Version.Revision
	readerIdentity := &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC, Issuer: f.provider.URL, Subject: "reader"}
	readerToken := f.token(t, "reader", func(c map[string]any) { c["roles"] = []string{"security_admin"} })
	if _, err := f.client.GetCurrentPrincipal(ctx, securityWireRequest(readerToken, &pb.GetCurrentPrincipalRequest{})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("unregistered token enrolled itself", err)
	}
	role := &pb.SecurityRole{Id: "reader", Name: "Tenant reader", Rules: []*pb.SecurityRule{
		{Id: "read", Effect: pb.SecurityEffect_SECURITY_EFFECT_ALLOW, Action: pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, Resource: &pb.SecurityRule_Prefix{Prefix: "tenant:"}},
		{Id: "private", Effect: pb.SecurityEffect_SECURITY_EFFECT_DENY, Action: pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, Resource: &pb.SecurityRule_Prefix{Prefix: "tenant:private:"}},
	}}
	command := &pb.ApplySecurityChangesRequest{ExpectedRevision: initial, ChangeId: bytes.Repeat([]byte{1}, 16), Changes: []*pb.SecurityChange{
		{Operation: &pb.SecurityChange_PutRole{PutRole: role}},
		{Operation: &pb.SecurityChange_PutUser{PutUser: &pb.SecurityUserStateChange{Identity: readerIdentity, State: pb.SecurityPrincipalState_SECURITY_PRINCIPAL_STATE_ACTIVE}}},
		{Operation: &pb.SecurityChange_PutAssignment{PutAssignment: &pb.SecurityRoleAssignment{Identity: readerIdentity, RoleId: "reader"}}},
	}}
	commit, err := f.client.ApplySecurityChanges(ctx, securityWireRequest(admin, command))
	if err != nil || len(commit.Msg.Applied) != 3 || commit.Msg.Version.Revision != initial+1 || commit.Msg.Enforcement != pb.SecurityEnforcementState_SECURITY_ENFORCEMENT_STATE_ENFORCED {
		t.Fatal("atomic role command", err)
	}
	retry, err := f.client.ApplySecurityChanges(ctx, securityWireRequest(admin, command))
	if err != nil || !retry.Msg.Replayed || retry.Msg.Version.Revision != commit.Msg.Version.Revision {
		t.Fatal("retained retry", err)
	}
	reader, err := f.client.GetCurrentPrincipal(ctx, securityWireRequest(readerToken, &pb.GetCurrentPrincipalRequest{}))
	if err != nil || len(reader.Msg.Roles) != 1 || reader.Msg.Roles[0].Id != "reader" {
		t.Fatal("Role-only membership", err)
	}
	if _, err := f.client.ListUsers(ctx, securityWireRequest(readerToken, &pb.ListUsersRequest{})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("reader token Role claim granted management", err)
	}
	for key, want := range map[string]bool{"tenant:public:1": true, "tenant:private:1": false, "other:1": false} {
		result, err := f.client.ExplainAccess(ctx, securityWireRequest(admin, &pb.ExplainAccessRequest{Identity: readerIdentity, Action: pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, LogicalKey: &key}))
		if err != nil || result.Msg.Allowed != want {
			t.Fatal("prefix policy", key, err)
		}
	}
	stale := &pb.ApplySecurityChangeRequest{ExpectedRevision: initial, ChangeId: bytes.Repeat([]byte{2}, 16), Change: &pb.SecurityChange{Operation: &pb.SecurityChange_DeleteRole{DeleteRole: "reader"}}}
	if _, err := f.client.ApplySecurityChange(ctx, securityWireRequest(admin, stale)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("stale CAS", err)
	}
	stale.ExpectedRevision = commit.Msg.Version.Revision
	if _, err := f.client.ApplySecurityChange(ctx, securityWireRequest(admin, stale)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("in-use Role deleted", err)
	}
	page, err := f.client.ListUsers(ctx, securityWireRequest(admin, &pb.ListUsersRequest{Limit: 1}))
	if err != nil || page.Msg.NextCursor == "" {
		t.Fatal("bounded listing", err)
	}
	if _, err := f.client.ListUsers(ctx, securityWireRequest(f.token(t, "other", nil), &pb.ListUsersRequest{Limit: 1, Cursor: page.Msg.NextCursor})); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("cursor changed Principal", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, f.server.URL+graphv1connect.LanternSecurityServiceApplySecurityChangesProcedure, strings.NewReader(`{"expectedRevision":"2","changeId":"AQEBAQEBAQEBAQEBAQEBAQ==","changes":[],"permissions":["manage"]}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Connect-Protocol-Version", "1")
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Forwarded-Host", request.URL.Host)
	request.Header.Set("Authorization", "Bearer "+admin)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatal("unknown direct-grant field accepted", response.StatusCode)
	}
	change := &pb.ApplySecurityChangeRequest{ExpectedRevision: commit.Msg.Version.Revision, ChangeId: bytes.Repeat([]byte{3}, 16), Change: &pb.SecurityChange{Operation: &pb.SecurityChange_PutUser{PutUser: &pb.SecurityUserStateChange{Identity: readerIdentity, State: pb.SecurityPrincipalState_SECURITY_PRINCIPAL_STATE_SUSPENDED}}}}
	if _, err := f.client.ApplySecurityChange(ctx, securityWireRequest(admin, change)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.GetCurrentPrincipal(ctx, securityWireRequest(readerToken, &pb.GetCurrentPrincipalRequest{})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("current suspension bypassed", err)
	}
	if _, err := f.client.ListUsers(ctx, securityWireRequest(admin, &pb.ListUsersRequest{Limit: 1, Cursor: page.Msg.NextCursor})); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("cursor survived policy change", err)
	}
}

func TestAuth_MachineRoleBearerRealConnect(t *testing.T) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	token := "lnt_m1_" + base64.RawURLEncoding.EncodeToString(secret)
	readerSecret := make([]byte, 32)
	if _, err := rand.Read(readerSecret); err != nil {
		t.Fatal(err)
	}
	readerToken := "lnt_m1_" + base64.RawURLEncoding.EncodeToString(readerSecret)
	f := newOIDCControlWireFixtureConfigured(t, func(config *provider.SecurityConfig) {
		if err := json.Unmarshal([]byte(`[{"id":"machine_reader","rules":[{"id":"read","effect":"allow","action":"vertex.read","resource":"data","prefix":"orders:"},{"id":"private","effect":"deny","action":"vertex.read","resource":"data","prefix":"orders:private:"}]},{"id":"machine_exporter","rules":[{"id":"export","effect":"allow","action":"export","resource":"data","prefix":"orders:"}]}]`), &config.Bootstrap.Roles); err != nil {
			t.Fatal(err)
		}
		now := config.Clock()
		raw, err := json.Marshal([]map[string]any{{"name": "worker", "role_ids": []string{"machine_reader", "machine_exporter"}, "credentials": []map[string]any{{"token": token, "created_at": now, "expires_at": now.Add(time.Hour)}}}, {"name": "readonly", "role_ids": []string{"machine_reader"}, "credentials": []map[string]any{{"token": readerToken, "created_at": now, "expires_at": now.Add(time.Hour)}}}})
		if err != nil {
			t.Fatal(err)
		}
		config.MachineBootstrapFile = filepath.Join(t.TempDir(), "machines.json")
		if err := createPrivateTestFile(config.MachineBootstrapFile, raw); err != nil {
			t.Fatal(err)
		}
	})
	for key, value := range map[string]string{"orders:1": "visible", "orders:private:1": "hidden"} {
		physical := "data:" + key
		if err := f.graph.PutVertex(physical, &pb.Vertex{Key: physical, Value: &pb.Vertex_String_{String_: value}}); err != nil {
			t.Fatal(err)
		}
	}
	before := f.fetches.Load()
	request := securityWireRequest(token, &pb.GetCurrentPrincipalRequest{})
	request.Header().Set("X-Lantern-Roles", "security_admin")
	principal, err := f.client.GetCurrentPrincipal(t.Context(), request)
	if err != nil || principal.Msg.Identity.Kind != pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_MACHINE || principal.Msg.Identity.MachineName != "worker" || principal.Msg.RecentAuthentication || principal.Msg.CsrfToken != "" || len(principal.Msg.Roles) != 2 {
		t.Fatal("machine credential confused with browser or token-supplied Roles", err)
	}
	visible, err := f.data.GetVertex(t.Context(), securityWireRequest(token, &pb.GetVertexRequest{Key: "orders:1"}))
	if err != nil || visible.Msg.Vertex.Key != "orders:1" || visible.Msg.Vertex.GetString_() != "visible" {
		t.Fatal("scoped machine read", err)
	}
	// #1620: data-read collections are independent from the Query action.
	keys, err := f.data.ScanVertexKeys(t.Context(), securityWireRequest(token, &pb.ScanVertexKeysRequest{Prefix: "orders:", Limit: 10}))
	if err != nil || len(keys.Msg.Keys) != 1 || keys.Msg.Keys[0] != "orders:1" {
		t.Fatal("reader without Query could not scan its cache", keys, err)
	}
	count, err := f.data.CountVerticesByPrefix(t.Context(), securityWireRequest(token, &pb.CountVerticesByPrefixRequest{Prefix: "orders:"}))
	if err != nil || count.Msg.Count != 1 {
		t.Fatal("reader without Query could not count", count, err)
	}
	if _, err := f.data.SearchVertices(t.Context(), securityWireRequest(token, &pb.SearchVerticesRequest{Query: "visible", Prefix: "orders:"})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("data reader inherited Query", err)
	}
	for _, key := range []string{"orders:private:1", "orders:private:missing", "other:1"} {
		if response, err := f.data.GetVertex(t.Context(), securityWireRequest(token, &pb.GetVertexRequest{Key: key})); response != nil || connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatal("Deny/existence boundary", key, err)
		}
	}

	exported, err := f.data.BackupSnapshot(t.Context(), securityWireRequest(token, &pb.BackupSnapshotRequest{VertexPrefix: "orders:"}))
	if err != nil {
		t.Fatal("machine export open", err)
	}
	var exportedKeys []string
	for exported.Receive() {
		if vertex := exported.Msg().GetVertex(); vertex != nil {
			exportedKeys = append(exportedKeys, vertex.Key)
		}
	}
	if err := exported.Err(); err != nil || !reflect.DeepEqual(exportedKeys, []string{"orders:1"}) {
		t.Fatal("machine export escaped scope or demanded recent auth", exportedKeys, err)
	}
	denied, err := f.data.BackupSnapshot(t.Context(), securityWireRequest(readerToken, &pb.BackupSnapshotRequest{VertexPrefix: "orders:"}))
	if err == nil {
		for denied.Receive() {
		}
		err = denied.Err()
	}
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("data reader inherited export", err)
	}
	if _, err := f.data.PutVertex(t.Context(), securityWireRequest(token, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "orders:1"}})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("reader wrote data", err)
	}
	if _, err := f.client.ListUsers(t.Context(), securityWireRequest(token, &pb.ListUsersRequest{})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("machine claimed management", err)
	}
	for _, invalid := range []string{token + "=", token[:len(token)-1], "lnt_m1_" + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32))} {
		if _, err := f.client.GetCurrentPrincipal(t.Context(), securityWireRequest(invalid, &pb.GetCurrentPrincipalRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatal("invalid machine credential admitted", err)
		}
	}
	if f.fetches.Load() != before {
		t.Fatal("machine admission contacted an IdP")
	}
	admin := f.token(t, "admin", nil)
	identity := &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_MACHINE, MachineName: "worker"}
	if _, err := f.client.ApplySecurityChange(t.Context(), securityWireRequest(admin, &pb.ApplySecurityChangeRequest{ExpectedRevision: principal.Msg.Version.Revision, ChangeId: bytes.Repeat([]byte{41}, 16), Change: &pb.SecurityChange{Operation: &pb.SecurityChange_PutUser{PutUser: &pb.SecurityUserStateChange{Identity: identity, State: pb.SecurityPrincipalState_SECURITY_PRINCIPAL_STATE_SUSPENDED}}}})); err != nil {
		t.Fatal(err)
	}
	if _, err := f.data.GetVertex(t.Context(), securityWireRequest(token, &pb.GetVertexRequest{Key: "orders:1"})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("suspended machine still read data", err)
	}
}

func TestAuth_OIDCScopedReceiptRealConnect(t *testing.T) {
	f := newOIDCControlWireFixtureOptions(t, nil, true)
	admin := f.token(t, "admin", nil)
	principal, err := f.client.GetCurrentPrincipal(t.Context(), securityWireRequest(admin, &pb.GetCurrentPrincipalRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	var rules []*pb.SecurityRule
	for i, action := range []pb.SecurityAction{pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, pb.SecurityAction_SECURITY_ACTION_VERTEX_WRITE, pb.SecurityAction_SECURITY_ACTION_VERTEX_DELETE, pb.SecurityAction_SECURITY_ACTION_RECEIPT_READ} {
		rules = append(rules, &pb.SecurityRule{Id: fmt.Sprintf("allow%d", i), Effect: pb.SecurityEffect_SECURITY_EFFECT_ALLOW, Action: action, Resource: &pb.SecurityRule_Prefix{Prefix: "orders:"}})
	}
	private := &pb.SecurityRule{Id: "private", Effect: pb.SecurityEffect_SECURITY_EFFECT_DENY, Action: pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, Resource: &pb.SecurityRule_Prefix{Prefix: "orders:private:"}}
	identity := &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC, Issuer: f.provider.URL, Subject: "receipt_writer"}
	other := &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC, Issuer: f.provider.URL, Subject: "other"}
	change, err := f.client.ApplySecurityChanges(t.Context(), securityWireRequest(admin, &pb.ApplySecurityChangesRequest{ExpectedRevision: principal.Msg.Version.Revision, ChangeId: bytes.Repeat([]byte{50}, 16), Changes: []*pb.SecurityChange{
		{Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Id: "receipt_writer", Rules: append(append([]*pb.SecurityRule(nil), rules...), private)}}},
		{Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Id: "receipt_other", Rules: rules}}},
		{Operation: &pb.SecurityChange_PutUser{PutUser: &pb.SecurityUserStateChange{Identity: identity, State: pb.SecurityPrincipalState_SECURITY_PRINCIPAL_STATE_ACTIVE}}},
		{Operation: &pb.SecurityChange_PutAssignment{PutAssignment: &pb.SecurityRoleAssignment{Identity: identity, RoleId: "receipt_writer"}}},
		{Operation: &pb.SecurityChange_PutAssignment{PutAssignment: &pb.SecurityRoleAssignment{Identity: other, RoleId: "receipt_other"}}},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	token := f.token(t, "receipt_writer", nil)
	capability, err := f.data.GetReceiptCapability(t.Context(), securityWireRequest(token, &pb.GetReceiptCapabilityRequest{}))
	if err != nil || !capability.Msg.Enabled {
		t.Fatal("scoped receipt capability", err)
	}
	newContext := func(seed byte) *pb.MutationReceiptContext {
		var epoch mutationreceipt.Epoch
		copy(epoch[:], capability.Msg.Policy.DeploymentEpoch)
		id, err := mutationreceipt.NewID(epoch, time.UnixMilli(int64(capability.Msg.ServerNowUnixMs)), [24]byte{seed})
		if err != nil {
			t.Fatal(err)
		}
		return &pb.MutationReceiptContext{Endpoint: capability.Msg.Endpoint, LogicalCallId: bytes.Repeat([]byte{seed}, 16), OperationIds: [][]byte{id.Bytes()}}
	}
	long := &pb.PutVertexRequest{ReceiptContext: newContext(1), Vertex: &pb.Vertex{Key: "orders:1", Value: &pb.Vertex_String_{String_: "long"}, Expiration: timestamppb.New(time.Now().Add(45 * time.Minute))}}
	if result, err := f.data.PutVertex(t.Context(), securityWireRequest(token, long)); err != nil || result.Msg.Outcome != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
		t.Fatal("scoped receipt Put", err)
	}
	short := &pb.PutVertexRequest{ReceiptContext: newContext(2), Vertex: &pb.Vertex{Key: "orders:1", Value: &pb.Vertex_String_{String_: "short"}, Expiration: timestamppb.New(time.Now().Add(30 * time.Minute))}}
	if _, err := f.data.PutVertex(t.Context(), securityWireRequest(token, short)); err != nil {
		t.Fatal("authorized lifecycle reduction", err)
	}
	if _, err := f.data.PutVertex(t.Context(), securityWireRequest(token, short)); err != nil {
		t.Fatal("authorized original replay", err)
	}
	hidden := &pb.PutVertexRequest{ReceiptContext: newContext(3), Vertex: &pb.Vertex{Key: "orders:private:1"}}
	if _, err := f.data.PutVertex(t.Context(), securityWireRequest(f.token(t, "other", nil), hidden)); err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][][]byte{
		hidden.ReceiptContext.OperationIds,
		newContext(4).OperationIds,
		{long.ReceiptContext.OperationIds[0], hidden.ReceiptContext.OperationIds[0]},
		{long.ReceiptContext.OperationIds[0], newContext(5).OperationIds[0]},
	} {
		response, err := f.data.GetReceiptStatuses(t.Context(), securityWireRequest(token, &pb.GetReceiptStatusesRequest{OperationIds: ids}))
		if response != nil || connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatal("hidden/unknown mixed status leaked", err)
		}
	}
	if _, err := f.data.PutVertices(t.Context(), securityWireRequest(token, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "orders:a"}, {Key: "orders:b"}}})); err != nil {
		t.Fatal(err)
	}
	if _, err := f.data.PutEdge(t.Context(), securityWireRequest(token, &pb.PutEdgeRequest{Edge: &pb.Edge{Tail: "orders:a", Head: "orders:b", Weight: math.MaxFloat32}})); err != nil {
		t.Fatal(err)
	}
	add := &pb.AddEdgeRequest{ReceiptContext: newContext(6), ContribId: bytes.Repeat([]byte{6}, 24), Edge: &pb.Edge{Tail: "orders:a", Head: "orders:b", Weight: math.MaxFloat32}}
	added, err := f.data.AddEdge(t.Context(), securityWireRequest(token, add))
	if err != nil || !math.IsInf(float64(added.Msg.EffectiveWeight), 1) {
		t.Fatal("authoritative Add overflow result", err)
	}
	status, err := f.data.GetReceiptStatus(t.Context(), securityWireRequest(token, &pb.GetReceiptStatusRequest{OperationId: add.ReceiptContext.OperationIds[0]}))
	if err != nil || status.Msg.Status.State != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED || !math.IsInf(float64(status.Msg.Status.Receipt.OriginalResult.GetAddEdgeEffectiveWeight()), 1) {
		t.Fatal("status changed original non-finite Add bytes", err)
	}
	before, _, _ := f.dataRuntime.MutationLogStats()
	if replay, err := f.data.AddEdge(t.Context(), securityWireRequest(token, add)); err != nil || !math.IsInf(float64(replay.Msg.EffectiveWeight), 1) {
		t.Fatal("Add replay changed original result", err)
	}
	after, _, _ := f.dataRuntime.MutationLogStats()
	if before != after {
		t.Fatal("receipt replay appended mutation")
	}
	var withoutDelete []*pb.SecurityRule
	for _, rule := range append(append([]*pb.SecurityRule(nil), rules...), private) {
		if rule.Action != pb.SecurityAction_SECURITY_ACTION_VERTEX_DELETE {
			withoutDelete = append(withoutDelete, rule)
		}
	}
	changed, err := f.client.ApplySecurityChange(t.Context(), securityWireRequest(admin, &pb.ApplySecurityChangeRequest{ExpectedRevision: change.Msg.Version.Revision, ChangeId: bytes.Repeat([]byte{51}, 16), Change: &pb.SecurityChange{Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Id: "receipt_writer", Rules: withoutDelete}}}}))
	if err != nil {
		t.Fatal(err)
	}
	if status, err := f.data.GetReceiptStatus(t.Context(), securityWireRequest(token, &pb.GetReceiptStatusRequest{OperationId: long.ReceiptContext.OperationIds[0]})); err != nil || status.Msg.Status.State != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED {
		t.Fatal("ordinary live Put invented a Delete requirement", err)
	}
	if _, err := f.data.GetReceiptStatus(t.Context(), securityWireRequest(token, &pb.GetReceiptStatusRequest{OperationId: short.ReceiptContext.OperationIds[0]})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("original TTL reduction disclosed after Delete loss", err)
	}
	if _, err := f.data.PutVertex(t.Context(), securityWireRequest(token, short)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("replay recomputed effect against shortened graph", err)
	}
	_, err = f.client.ApplySecurityChange(t.Context(), securityWireRequest(admin, &pb.ApplySecurityChangeRequest{ExpectedRevision: changed.Msg.Version.Revision, ChangeId: bytes.Repeat([]byte{52}, 16), Change: &pb.SecurityChange{Operation: &pb.SecurityChange_DeleteAssignment{DeleteAssignment: &pb.SecurityRoleAssignment{Identity: identity, RoleId: "receipt_writer"}}}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.data.GetReceiptStatus(t.Context(), securityWireRequest(token, &pb.GetReceiptStatusRequest{OperationId: add.ReceiptContext.OperationIds[0]})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Role removal disclosed original receipt", err)
	}
}

func TestAuth_OIDCScopedChangesRealConnect(t *testing.T) {
	f := newOIDCControlWireFixture(t)
	admin := f.token(t, "admin", nil)
	current, err := f.client.GetCurrentPrincipal(t.Context(), securityWireRequest(admin, &pb.GetCurrentPrincipalRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	rule := func(id string, action pb.SecurityAction, effect pb.SecurityEffect, prefix string) *pb.SecurityRule {
		return &pb.SecurityRule{Id: id, Action: action, Effect: effect, Resource: &pb.SecurityRule_Prefix{Prefix: prefix}}
	}
	readerRules := []*pb.SecurityRule{rule("read", pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, pb.SecurityEffect_SECURITY_EFFECT_ALLOW, "orders:"), rule("private", pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, pb.SecurityEffect_SECURITY_EFFECT_DENY, "orders:private:")}
	identityRules := []*pb.SecurityRule{
		rule("identity", pb.SecurityAction_SECURITY_ACTION_CDC_IDENTITY, pb.SecurityEffect_SECURITY_EFFECT_ALLOW, "orders:"),
		rule("private", pb.SecurityAction_SECURITY_ACTION_CDC_IDENTITY, pb.SecurityEffect_SECURITY_EFFECT_DENY, "orders:private:"),
		rule("read", pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, pb.SecurityEffect_SECURITY_EFFECT_ALLOW, "orders:"),
		rule("private-read", pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, pb.SecurityEffect_SECURITY_EFFECT_DENY, "orders:private:"),
	}
	valueOnlyRules := append(append([]*pb.SecurityRule(nil), readerRules...), rule("value", pb.SecurityAction_SECURITY_ACTION_CDC_VALUE, pb.SecurityEffect_SECURITY_EFFECT_ALLOW, "orders:"))
	valueRules := append(append([]*pb.SecurityRule(nil), valueOnlyRules...), rule("identity", pb.SecurityAction_SECURITY_ACTION_CDC_IDENTITY, pb.SecurityEffect_SECURITY_EFFECT_ALLOW, "orders:"))
	var writerRules []*pb.SecurityRule
	for i, action := range []pb.SecurityAction{pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, pb.SecurityAction_SECURITY_ACTION_VERTEX_WRITE, pb.SecurityAction_SECURITY_ACTION_VERTEX_DELETE} {
		writerRules = append(writerRules, rule(fmt.Sprintf("write%d", i), action, pb.SecurityEffect_SECURITY_EFFECT_ALLOW, ""))
	}
	roles := map[string][]*pb.SecurityRule{"reader": readerRules, "identity": identityRules, "values": valueRules, "value_only": valueOnlyRules, "writer": writerRules, "identity2": identityRules}
	var changes []*pb.SecurityChange
	for subject, rules := range roles {
		identity := &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC, Issuer: f.provider.URL, Subject: subject}
		changes = append(changes,
			&pb.SecurityChange{Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Id: subject, Rules: rules}}},
			&pb.SecurityChange{Operation: &pb.SecurityChange_PutUser{PutUser: &pb.SecurityUserStateChange{Identity: identity, State: pb.SecurityPrincipalState_SECURITY_PRINCIPAL_STATE_ACTIVE}}},
			&pb.SecurityChange{Operation: &pb.SecurityChange_PutAssignment{PutAssignment: &pb.SecurityRoleAssignment{Identity: identity, RoleId: subject}}})
	}
	result, err := f.client.ApplySecurityChanges(t.Context(), securityWireRequest(admin, &pb.ApplySecurityChangesRequest{ExpectedRevision: current.Msg.Version.Revision, ChangeId: bytes.Repeat([]byte{51}, 16), Changes: changes}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	open := func(subject string, projection pb.ChangeProjection, cursor []byte) *connect.ServerStreamForClient[pb.WatchChangesResponse] {
		t.Helper()
		stream, err := f.changes.WatchChanges(ctx, securityWireRequest(f.token(t, subject, nil), &pb.WatchChangesRequest{Prefix: "orders:", Projection: projection, Bootstrap: len(cursor) == 0, Cursor: cursor}))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = stream.Close() })
		return stream
	}
	denied := open("reader", pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY, nil)
	if denied.Receive() || connect.CodeOf(denied.Err()) != connect.CodePermissionDenied {
		t.Fatal("reader inherited CDC", denied.Err())
	}
	identityStream := open("identity", pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY, nil)
	if !identityStream.Receive() || !identityStream.Msg().Bootstrap || len(identityStream.Msg().Cursor) == 0 || len(identityStream.Msg().Invalidations) != 0 {
		t.Fatal("opaque bootstrap missing", identityStream.Err())
	}
	bootstrapCursor := append([]byte(nil), identityStream.Msg().Cursor...)
	valueStream := open("values", pb.ChangeProjection_CHANGE_PROJECTION_VALUE, nil)
	if !valueStream.Receive() || !valueStream.Msg().Bootstrap {
		t.Fatal("value bootstrap", valueStream.Err())
	}
	stolen := open("identity2", pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY, bootstrapCursor)
	if stolen.Receive() || connect.CodeOf(stolen.Err()) != connect.CodeFailedPrecondition {
		t.Fatal("same-policy Principal stole cursor", stolen.Err())
	}
	wrongProjection := open("value_only", pb.ChangeProjection_CHANGE_PROJECTION_VALUE, nil)
	if wrongProjection.Receive() || connect.CodeOf(wrongProjection.Err()) != connect.CodePermissionDenied {
		t.Fatal("value capability omitted required identity grant", wrongProjection.Err())
	}
	writer := f.token(t, "writer", nil)
	if _, err := f.data.PutVertices(ctx, securityWireRequest(writer, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "orders:1", Value: &pb.Vertex_String_{String_: "visible"}}, {Key: "orders:private:1", Value: &pb.Vertex_String_{String_: "hidden"}}, {Key: "other:1", Value: &pb.Vertex_String_{String_: "outside"}}}})); err != nil {
		t.Fatal(err)
	}
	nextVisible := func(stream *connect.ServerStreamForClient[pb.WatchChangesResponse]) *pb.WatchChangesResponse {
		t.Helper()
		for stream.Receive() {
			if len(stream.Msg().Invalidations) != 0 {
				return stream.Msg()
			}
		}
		t.Fatal("visible change missing", stream.Err())
		return nil
	}
	identityFrame, valueFrame := nextVisible(identityStream), nextVisible(valueStream)
	if len(identityFrame.Invalidations) != 1 || identityFrame.Invalidations[0].GetVertexKey() != "orders:1" || identityFrame.Invalidations[0].CurrentImage != nil || len(identityFrame.Cursor) != len(bootstrapCursor) {
		t.Fatal("identity disclosure", identityFrame)
	}
	if len(valueFrame.Invalidations) != 1 || valueFrame.Invalidations[0].GetVertex().GetString_() != "visible" || valueFrame.Invalidations[0].GetVertex().GetKey() != "orders:1" {
		t.Fatal("authorized local value projection", valueFrame)
	}
	resumeCursor := append([]byte(nil), identityFrame.Cursor...)
	_ = identityStream.Close()
	resumed := open("identity", pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY, resumeCursor)
	if _, err := f.data.PutVertex(ctx, securityWireRequest(writer, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "orders:private:2"}})); err != nil {
		t.Fatal(err)
	}
	// A hidden-only commit advances progress on a periodic empty frame, without
	// projecting a hidden identity, original batch count or raw origin watermark.
	if !resumed.Receive() || len(resumed.Msg().Invalidations) != 0 || len(resumed.Msg().Cursor) != len(resumeCursor) {
		t.Fatal("hidden-only progress", resumed.Err())
	}
	// Protected Edge Put requires explicit live endpoints; verify its separate CDC event.
	if _, err := f.data.PutVertex(ctx, securityWireRequest(writer, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "orders:2"}})); err != nil {
		t.Fatal(err)
	}
	for _, stream := range []*connect.ServerStreamForClient[pb.WatchChangesResponse]{resumed, valueStream} {
		frame := nextVisible(stream)
		if len(frame.Invalidations) != 1 || frame.Invalidations[0].GetVertexKey() != "orders:2" {
			t.Fatal("explicit endpoint CDC", frame)
		}
	}
	if _, err := f.data.PutEdges(ctx, securityWireRequest(writer, &pb.PutEdgesRequest{Edges: []*pb.Edge{{Tail: "orders:1", Head: "orders:2", Weight: 3}, {Tail: "orders:1", Head: "orders:private:1", Weight: 99}}})); err != nil {
		t.Fatal(err)
	}
	edgeFrame := nextVisible(resumed)
	if len(edgeFrame.Invalidations) != 1 || edgeFrame.Invalidations[0].GetEdgeKey().GetHead() != "orders:2" || edgeFrame.Invalidations[0].GetEdge() != nil {
		t.Fatal("Edge endpoint identity disclosure", edgeFrame)
	}
	valueEdge := nextVisible(valueStream)
	if len(valueEdge.Invalidations) != 1 || valueEdge.Invalidations[0].GetEdge().GetWeight() != 3 {
		t.Fatal("Edge value projection", valueEdge)
	}
	if _, err := f.data.DeleteVerticesByPrefix(ctx, securityWireRequest(writer, &pb.DeleteVerticesByPrefixRequest{Prefix: "orders:"})); err != nil {
		t.Fatal(err)
	}
	deleted := nextVisible(resumed)
	for _, item := range deleted.Invalidations {
		if strings.HasPrefix(item.GetVertexKey(), "orders:private:") {
			t.Fatal("prefix Delete broadened victims", deleted)
		}
	}
	readerToken := f.token(t, "reader", nil)
	if count, err := f.data.CountVerticesByPrefix(ctx, securityWireRequest(readerToken, &pb.CountVerticesByPrefixRequest{Prefix: "orders:"})); err != nil || count.Msg.Count != 0 {
		t.Fatal("read-only rebootstrap required Query", count, err)
	}
	// Any policy cut change terminates an established stream while idle and
	// invalidates its encrypted cursor, including an unrelated grant expansion.
	if _, err := f.client.ApplySecurityChange(ctx, securityWireRequest(admin, &pb.ApplySecurityChangeRequest{ExpectedRevision: result.Msg.Version.Revision, ChangeId: bytes.Repeat([]byte{52}, 16), Change: &pb.SecurityChange{Operation: &pb.SecurityChange_DeleteAssignment{DeleteAssignment: &pb.SecurityRoleAssignment{Identity: &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC, Issuer: f.provider.URL, Subject: "identity"}, RoleId: "identity"}}}})); err != nil {
		t.Fatal(err)
	}
	for resumed.Receive() {
	}
	if connect.CodeOf(resumed.Err()) != connect.CodeUnavailable {
		t.Fatal("established stream retained revoked cut", resumed.Err())
	}
	if _, err := f.data.SearchVertices(ctx, securityWireRequest(readerToken, &pb.SearchVerticesRequest{Query: "visible", Prefix: "orders:"})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("reader inherited Query", err)
	}
}

func TestAuth_ControlOffRealConnect(t *testing.T) {
	runtime, closeRuntime, err := provider.NewSecurityRuntime(provider.SecurityConfig{Mode: "off"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRuntime()
	_, handler := runtime.PublicControlHTTPHandler()
	srv := httptest.NewServer(handler)
	defer srv.Close()
	client := graphv1connect.NewLanternSecurityServiceClient(srv.Client(), srv.URL)
	capabilities, err := client.GetAuthCapabilities(t.Context(), connect.NewRequest(&pb.GetAuthCapabilitiesRequest{}))
	if err != nil || capabilities.Msg.Mode != pb.AuthMode_AUTH_MODE_OFF || len(capabilities.Msg.LoginIssuers) != 0 {
		t.Fatal("OFF requires OIDC", err)
	}
	if _, err := client.ListRoles(t.Context(), connect.NewRequest(&pb.ListRolesRequest{})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("OFF control accepts security edits", err)
	}
}

func (f *oidcControlWireFixture) browserRequest(t *testing.T, method, path string, cookies []*http.Cookie, csrf string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, f.server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "admin.example"
	req.Header.Set("X-Forwarded-Host", "admin.example")
	req.Header.Set("X-Forwarded-Proto", "https")
	if method == http.MethodPost {
		req.Header.Set("Origin", "https://admin.example")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	if csrf != "" {
		req.Header.Set("X-Lantern-CSRF", csrf)
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	return req
}
func wireBrowserDo(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}
func (f *oidcControlWireFixture) browserLoginStart(t *testing.T, cookies []*http.Cookie, stepUp bool) *http.Response {
	t.Helper()
	query := url.Values{"issuer": {f.provider.URL}, "return": {"/security/roles"}}
	if stepUp {
		query.Set("step_up", "true")
	}
	response := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, "/auth/login?"+query.Encode(), cookies, ""))
	if response.StatusCode != http.StatusFound || len(response.Cookies()) != 1 {
		t.Fatal("login start", response.StatusCode)
	}
	cookie := response.Cookies()[0]
	if cookie.Name != "__Host-lantern-login" || !cookie.Secure || !cookie.HttpOnly || cookie.Domain != "" || cookie.Path != "/" || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatal("transaction cookie boundary")
	}
	return response
}
func (f *oidcControlWireFixture) browserCallbackPath(t *testing.T, start *http.Response, subject string, wrongNonce bool) string {
	t.Helper()
	authorize, err := url.Parse(start.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	query := authorize.Query()
	if query.Get("code_challenge_method") != "S256" || query.Get("response_type") != "code" || query.Get("scope") != "openid" || query.Get("nonce") == "" || query.Get("state") == "" {
		t.Fatal("code/nonce/PKCE missing")
	}
	if query.Get("max_age") == "0" {
		if query.Get("prompt") != "login" || query.Get("claims") != `{"id_token":{"auth_time":{"essential":true}}}` {
			t.Fatal("step-up omitted signed authentication evidence")
		}
	} else if query.Get("max_age") != "" || query.Get("prompt") != "" || query.Get("claims") != "" {
		t.Fatal("ordinary login imposed reauthentication")
	}
	var rawCode [16]byte
	if _, err := rand.Read(rawCode[:]); err != nil {
		t.Fatal(err)
	}
	code := base64.RawURLEncoding.EncodeToString(rawCode[:])
	f.mu.Lock()
	f.codes[code] = oidcWireCode{nonce: query.Get("nonce"), challenge: query.Get("code_challenge"), subject: subject, wrongNonce: wrongNonce}
	f.mu.Unlock()
	callback, err := url.Parse(query.Get("redirect_uri"))
	if err != nil || callback.Host != "admin.example" || callback.Scheme != "https" {
		t.Fatal("exact registered callback", err)
	}
	callback.RawQuery = url.Values{"code": {code}, "state": {query.Get("state")}, "iss": {f.provider.URL}}.Encode()
	return callback.RequestURI()
}
func browserCookieValue(cookies []*http.Cookie, name string) string {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

// Simulate exactly the configured HTTPS gateway hop on public bearer RPCs.
type authIngressRoundTripper struct{ next http.RoundTripper }

func (r authIngressRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	req := request.Clone(request.Context())
	req.Header.Set("X-Forwarded-Proto", "https")
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	req.Header.Set("X-Forwarded-Host", host)
	return r.next.RoundTrip(req)
}

type authWireRoundTripper struct {
	next    http.RoundTripper
	cookies []*http.Cookie
	csrf    string
}

func (r authWireRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	req := request.Clone(request.Context())
	req.Host = "admin.example"
	req.Header.Set("X-Forwarded-Host", "admin.example")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Origin", "https://admin.example")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("X-Lantern-CSRF", r.csrf)
	for _, cookie := range r.cookies {
		req.AddCookie(cookie)
	}
	return r.next.RoundTrip(req)
}

func TestAuth_OIDCBrowserSessionRealConnect(t *testing.T) {
	f := newOIDCControlWireFixture(t)
	start := f.browserLoginStart(t, nil, false)
	callback := f.browserCallbackPath(t, start, "admin", false)
	response := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, callback, start.Cookies(), ""))
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/security/roles" {
		t.Fatal("validated login callback", response.StatusCode)
	}
	cookies := make([]*http.Cookie, 0, 2)
	for _, cookie := range response.Cookies() {
		if cookie.Name == "__Host-lantern-session" || cookie.Name == "__Host-lantern-csrf" {
			if !cookie.Secure || !cookie.HttpOnly || cookie.Domain != "" || cookie.Path != "/" || cookie.SameSite != http.SameSiteStrictMode {
				t.Fatal("opaque session flags")
			}
			cookies = append(cookies, cookie)
		}
	}
	if len(cookies) != 2 {
		t.Fatal("session material missing")
	}
	csrf := browserCookieValue(cookies, "__Host-lantern-csrf")
	bootstrap := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, "/auth/session", cookies, ""))
	raw, err := io.ReadAll(bootstrap.Body)
	if err != nil {
		t.Fatal(err)
	}
	var session pb.BrowserSession
	if bootstrap.StatusCode != http.StatusOK || protojson.Unmarshal(raw, &session) != nil || session.Mode != pb.AuthMode_AUTH_MODE_OIDC || session.Principal.Identity.Subject != "admin" || session.Principal.CsrfToken != csrf || bytes.Contains(raw, []byte("provider-private-access-token")) || bytes.Contains(raw, []byte(browserCookieValue(cookies, "__Host-lantern-session"))) {
		t.Fatal("session bootstrap leaked credentials or lost proof")
	}
	client := graphv1connect.NewLanternSecurityServiceClient(&http.Client{Transport: authWireRoundTripper{next: http.DefaultTransport, cookies: cookies, csrf: csrf}}, f.server.URL+"/browser")
	roles, err := client.ListRoles(t.Context(), connect.NewRequest(&pb.ListRolesRequest{}))
	if err != nil || len(roles.Msg.Roles) != 1 || roles.Msg.Roles[0].Id != "security_admin" || !roles.Msg.Roles[0].EnvOwned {
		t.Fatal("session-authenticated Connect", err)
	}
	_, err = client.ApplySecurityChange(t.Context(), connect.NewRequest(&pb.ApplySecurityChangeRequest{ExpectedRevision: roles.Msg.Version.Revision, ChangeId: bytes.Repeat([]byte{79}, 16), Change: &pb.SecurityChange{Operation: &pb.SecurityChange_PutRole{PutRole: roles.Msg.Roles[0]}}}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("read-only environment Role metadata accepted as a write", err)
	}
	publicRequest := connect.NewRequest(&pb.GetCurrentPrincipalRequest{})
	for _, cookie := range cookies {
		publicRequest.Header().Add("Cookie", cookie.Name+"="+cookie.Value)
	}
	if _, err := f.client.GetCurrentPrincipal(t.Context(), publicRequest); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("public bearer accepted browser cookies", err)
	}
	for name, mutate := range map[string]func(*http.Request){
		"missing csrf":      func(r *http.Request) { r.Header.Del("X-Lantern-CSRF") },
		"foreign origin":    func(r *http.Request) { r.Header.Set("Origin", "https://attacker.example") },
		"wrong host":        func(r *http.Request) { r.Host = "attacker.example" },
		"unqualified proxy": func(r *http.Request) { r.Header.Del("X-Forwarded-Proto") },
	} {
		t.Run(name, func(t *testing.T) {
			req := f.browserRequest(t, http.MethodPost, "/auth/logout", cookies, csrf)
			mutate(req)
			if response := wireBrowserDo(t, req); response.StatusCode == http.StatusOK {
				t.Fatal("untrusted logout accepted")
			}
		})
	}
	if replay := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, callback, start.Cookies(), "")); replay.StatusCode != http.StatusUnauthorized {
		t.Fatal("login state replay", replay.StatusCode)
	}
	stepUp := f.browserLoginStart(t, cookies, true)
	authorize, _ := url.Parse(stepUp.Header.Get("Location"))
	if authorize.Query().Get("max_age") != "0" || authorize.Query().Get("prompt") != "login" || authorize.Query().Get("claims") != `{"id_token":{"auth_time":{"essential":true}}}` {
		t.Fatal("step-up omitted fresh authentication")
	}
	rotated := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, f.browserCallbackPath(t, stepUp, "admin", false), stepUp.Cookies(), ""))
	if rotated.StatusCode != http.StatusSeeOther {
		t.Fatal("step-up callback", rotated.StatusCode)
	}
	newCookies := make([]*http.Cookie, 0, 2)
	for _, cookie := range rotated.Cookies() {
		if cookie.Name == "__Host-lantern-session" || cookie.Name == "__Host-lantern-csrf" {
			newCookies = append(newCookies, cookie)
		}
	}
	if browserCookieValue(newCookies, "__Host-lantern-session") == browserCookieValue(cookies, "__Host-lantern-session") {
		t.Fatal("step-up reused cookie")
	}
	if old := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, "/auth/session", cookies, "")); old.StatusCode != http.StatusUnauthorized {
		t.Fatal("replaced cookie remains live")
	}
	logout := wireBrowserDo(t, f.browserRequest(t, http.MethodPost, "/auth/logout", newCookies, browserCookieValue(newCookies, "__Host-lantern-csrf")))
	var revoked pb.SessionRevocation
	logoutBody, err := io.ReadAll(logout.Body)
	if err != nil || logout.StatusCode != http.StatusOK || protojson.Unmarshal(logoutBody, &revoked) != nil || revoked.Enforcement != pb.SecurityEnforcementState_SECURITY_ENFORCEMENT_STATE_ENFORCED {
		t.Fatal("logout commit", err)
	}
	if after := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, "/auth/session", newCookies, "")); after.StatusCode != http.StatusUnauthorized {
		t.Fatal("revoked session admitted")
	}
	// Logout does not turn independently issued API tokens into browser tokens
	// or revoke identity implicitly; current account policy still governs them.
	if _, err := f.client.GetCurrentPrincipal(t.Context(), securityWireRequest(f.token(t, "admin", nil), &pb.GetCurrentPrincipalRequest{})); err != nil {
		t.Fatal("independent API token lost identity", err)
	}
}

func TestAuth_OIDCBrowserLoginFailuresRealConnect(t *testing.T) {
	f := newOIDCControlWireFixture(t)
	for name, subject := range map[string]string{"wrong nonce": "admin", "unknown subject": "unknown"} {
		t.Run(name, func(t *testing.T) {
			start := f.browserLoginStart(t, nil, false)
			path := f.browserCallbackPath(t, start, subject, name == "wrong nonce")
			response := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, path, start.Cookies(), ""))
			if response.StatusCode != http.StatusUnauthorized || browserCookieValue(response.Cookies(), "__Host-lantern-session") != "" {
				t.Fatal("invalid login created a session", response.StatusCode)
			}
		})
	}
	for _, evidence := range []string{"future", "contradictory", "null"} {
		t.Run(evidence+" signed auth_time", func(t *testing.T) {
			start := f.browserLoginStart(t, nil, false)
			path := f.browserCallbackPath(t, start, "admin", false)
			callback, err := url.Parse(path)
			if err != nil {
				t.Fatal(err)
			}
			codeID := callback.Query().Get("code")
			f.mu.Lock()
			code := f.codes[codeID]
			code.authTime = evidence
			f.codes[codeID] = code
			f.mu.Unlock()
			response := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, path, start.Cookies(), ""))
			if response.StatusCode != http.StatusUnauthorized || browserCookieValue(response.Cookies(), "__Host-lantern-session") != "" {
				t.Fatal("unproven recent authentication created a session", response.StatusCode)
			}
		})
	}
	query := url.Values{"issuer": {f.provider.URL}, "return": {"//attacker.example"}}
	if response := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, "/auth/login?"+query.Encode(), nil, "")); response.StatusCode != http.StatusBadRequest {
		t.Fatal("open return redirect", response.StatusCode)
	}
	start := f.browserLoginStart(t, nil, false)
	path := f.browserCallbackPath(t, start, "admin", false)
	if response := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, path, nil, "")); response.StatusCode != http.StatusUnauthorized {
		t.Fatal("unbound callback admitted")
	}
	f.advance(11 * time.Minute)
	if response := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, path, start.Cookies(), "")); response.StatusCode != http.StatusUnauthorized {
		t.Fatal("expired login state admitted")
	}
}

func TestAuth_OIDCExactDataAndLifecycleRealConnect(t *testing.T) {
	f := newOIDCControlWireFixture(t)
	admin := f.token(t, "admin", nil)
	principal, err := f.client.GetCurrentPrincipal(t.Context(), securityWireRequest(admin, &pb.GetCurrentPrincipalRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	var rules []*pb.SecurityRule
	for i, action := range []pb.SecurityAction{pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, pb.SecurityAction_SECURITY_ACTION_VERTEX_WRITE} {
		rules = append(rules, &pb.SecurityRule{Id: fmt.Sprintf("allow%d", i), Effect: pb.SecurityEffect_SECURITY_EFFECT_ALLOW, Action: action, Resource: &pb.SecurityRule_Prefix{Prefix: "users:"}})
	}
	for i, action := range []pb.SecurityAction{pb.SecurityAction_SECURITY_ACTION_VERTEX_DELETE} {
		rules = append(rules, &pb.SecurityRule{Id: fmt.Sprintf("deny%d", i), Effect: pb.SecurityEffect_SECURITY_EFFECT_DENY, Action: action, Resource: &pb.SecurityRule_Prefix{Prefix: "users:"}})
	}
	rules = append(rules, &pb.SecurityRule{Id: "private", Effect: pb.SecurityEffect_SECURITY_EFFECT_DENY, Action: pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, Resource: &pb.SecurityRule_Prefix{Prefix: "users:private:"}})
	identity := &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC, Issuer: f.provider.URL, Subject: "writer"}
	_, err = f.client.ApplySecurityChanges(t.Context(), securityWireRequest(admin, &pb.ApplySecurityChangesRequest{ExpectedRevision: principal.Msg.Version.Revision, ChangeId: bytes.Repeat([]byte{7}, 16), Changes: []*pb.SecurityChange{
		{Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Id: "writer", Rules: rules}}},
		{Operation: &pb.SecurityChange_PutUser{PutUser: &pb.SecurityUserStateChange{Identity: identity, State: pb.SecurityPrincipalState_SECURITY_PRINCIPAL_STATE_ACTIVE}}},
		{Operation: &pb.SecurityChange_PutAssignment{PutAssignment: &pb.SecurityRoleAssignment{Identity: identity, RoleId: "writer"}}},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	writer := f.token(t, "writer", nil)
	put := func(key, value string, expiration time.Time) (*connect.Response[pb.PutVertexResponse], error) {
		vertex := &pb.Vertex{Key: key, Value: &pb.Vertex_String_{String_: value}}
		if !expiration.IsZero() {
			vertex.Expiration = timestamppb.New(expiration)
		}
		return f.data.PutVertex(t.Context(), securityWireRequest(writer, &pb.PutVertexRequest{Vertex: vertex}))
	}
	if result, err := put("users:1", "original", time.Now().Add(time.Hour)); err != nil || result.Msg.Outcome != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
		t.Fatal("write without Delete", err)
	}
	if _, err := put("users:1", "extension", time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal("TTL extension incorrectly needs Delete", err)
	}
	if _, err := put("users:1", "shortening", time.Now().Add(time.Minute)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("TTL shortening bypassed Delete Deny", err)
	}
	got, err := f.data.GetVertex(t.Context(), securityWireRequest(writer, &pb.GetVertexRequest{Key: "users:1"}))
	if err != nil || got.Msg.Vertex.GetString_() != "extension" {
		t.Fatal("denied TTL overwrite changed value", err)
	}
	if _, err := put("users:permanent", "keep", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := put("users:permanent", "finite", time.Now().Add(time.Hour)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("permanent lifetime reduced without Delete", err)
	}
	if _, err := f.data.PutVertex(t.Context(), securityWireRequest(writer, &pb.PutVertexRequest{IfAbsent: true, Vertex: &pb.Vertex{Key: "users:permanent", Expiration: timestamppb.New(time.Now().Add(-time.Hour))}})); err != nil {
		t.Fatal("condition miss invented a delete effect", err)
	}
	before, _, _ := f.dataRuntime.MutationLogStats()
	_, err = f.data.PutVertices(t.Context(), securityWireRequest(writer, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{
		{Key: "users:rollback", Value: &pb.Vertex_String_{String_: "safe"}, Expiration: timestamppb.New(time.Now().Add(time.Hour))},
		{Key: "users:expired", Expiration: timestamppb.New(time.Now().Add(-time.Hour))},
	}}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("mixed lifecycle batch admitted", err)
	}
	if _, live := f.graph.GetVertex("data:users:rollback"); live {
		t.Fatal("lifecycle failure partially wrote")
	}
	after, _, _ := f.dataRuntime.MutationLogStats()
	if after != before {
		t.Fatal("denied lifecycle batch appended replication data")
	}
	for name, message := range map[string]*pb.GetVerticesRequest{
		"hidden existing": {Keys: []string{"users:1", "users:private:existing"}},
		"hidden missing":  {Keys: []string{"users:1", "users:private:missing"}},
		"outside":         {Keys: []string{"users:1", "other:1"}},
	} {
		t.Run(name, func(t *testing.T) {
			if response, err := f.data.GetVertices(t.Context(), securityWireRequest(writer, message)); connect.CodeOf(err) != connect.CodePermissionDenied || response != nil {
				t.Fatal("denied exact batch disclosed results", err)
			}
		})
	}
	_, err = f.data.PutVertices(t.Context(), securityWireRequest(writer, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "users:mixed"}, {Key: "other:denied"}}}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("mixed namespace batch admitted", err)
	}
	if _, live := f.graph.GetVertex("data:users:mixed"); live {
		t.Fatal("permission failure partially wrote")
	}
	if _, err := f.data.AddEdge(t.Context(), securityWireRequest(writer, &pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "users:a", Head: "users:b", Weight: 1}})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("missing live endpoints admitted", err)
	}
	if _, err := f.data.PutVertices(t.Context(), securityWireRequest(writer, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "users:a"}, {Key: "users:b"}}})); err != nil {
		t.Fatal(err)
	}
	_, err = f.data.AddEdges(t.Context(), securityWireRequest(writer, &pb.AddEdgesRequest{Edges: []*pb.Edge{{Tail: "users:a", Head: "users:b", Weight: 2, Expiration: timestamppb.New(time.Now().Add(time.Hour))}}}))
	if err != nil {
		t.Fatal("authorized edge creation", err)
	}
	for _, key := range []string{"data:users:a", "data:users:b"} {
		if _, live := f.graph.GetVertex(key); !live {
			t.Fatal("explicitly created endpoint was lost")
		}
	}
	_, err = f.data.AddEdge(t.Context(), securityWireRequest(writer, &pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "users:a", Head: "other:b", Weight: 1}}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("cross-prefix Add admitted", err)
	}
	if _, live := f.graph.GetVertex("data:other:b"); live {
		t.Fatal("denied Add created endpoint")
	}
	_, err = f.data.PutEdge(t.Context(), securityWireRequest(writer, &pb.PutEdgeRequest{Edge: &pb.Edge{Tail: "users:a", Head: "users:b", Weight: 9, Expiration: timestamppb.New(time.Now().Add(time.Minute))}}))
	if err != nil {
		t.Fatal("Head Write did not permit Edge TTL change", err)
	}
	edge, err := f.data.GetEdge(t.Context(), securityWireRequest(writer, &pb.GetEdgeRequest{Tail: "users:a", Head: "users:b"}))
	if err != nil || edge.Msg.Edge.Weight != 9 {
		t.Fatal("authorized Edge Put did not update the Edge", err)
	}
	if _, err := f.data.DeleteVertex(t.Context(), securityWireRequest(writer, &pb.DeleteVertexRequest{Key: "users:1"})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("direct Delete Deny bypassed", err)
	}
	if _, err := f.data.GetServerStatus(t.Context(), securityWireRequest(writer, &pb.GetServerStatusRequest{})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("namespace writer received deployment totals", err)
	}
	if _, err := f.data.GetVertex(t.Context(), securityWireRequest(admin, &pb.GetVertexRequest{Key: "users:1"})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("security admin inherited data", err)
	}
	if _, err := f.data.GetVertex(t.Context(), connect.NewRequest(&pb.GetVertexRequest{Key: "users:1"})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("anonymous data", err)
	}
}

func TestAuth_OIDCScopedCollectionsRealConnect(t *testing.T) {
	f := newOIDCControlWireFixture(t)
	admin := f.token(t, "admin", nil)
	principal, err := f.client.GetCurrentPrincipal(t.Context(), securityWireRequest(admin, &pb.GetCurrentPrincipalRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	identity := &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC, Issuer: f.provider.URL, Subject: "scoped"}
	var rules []*pb.SecurityRule
	for i, action := range []pb.SecurityAction{pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, pb.SecurityAction_SECURITY_ACTION_VERTEX_WRITE, pb.SecurityAction_SECURITY_ACTION_VERTEX_DELETE, pb.SecurityAction_SECURITY_ACTION_QUERY} {
		rules = append(rules, &pb.SecurityRule{Id: fmt.Sprintf("allow%d", i), Effect: pb.SecurityEffect_SECURITY_EFFECT_ALLOW, Action: action, Resource: &pb.SecurityRule_Prefix{Prefix: "users:"}})
	}
	rules = append(rules, &pb.SecurityRule{Id: "private", Effect: pb.SecurityEffect_SECURITY_EFFECT_DENY, Action: pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, Resource: &pb.SecurityRule_Prefix{Prefix: "users:private:"}})
	rules = append(rules, &pb.SecurityRule{Id: "private-write", Effect: pb.SecurityEffect_SECURITY_EFFECT_DENY, Action: pb.SecurityAction_SECURITY_ACTION_VERTEX_WRITE, Resource: &pb.SecurityRule_Prefix{Prefix: "users:private:"}})
	_, err = f.client.ApplySecurityChanges(t.Context(), securityWireRequest(admin, &pb.ApplySecurityChangesRequest{ExpectedRevision: principal.Msg.Version.Revision, ChangeId: bytes.Repeat([]byte{8}, 16), Changes: []*pb.SecurityChange{
		{Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Id: "scoped", Rules: rules}}},
		{Operation: &pb.SecurityChange_PutUser{PutUser: &pb.SecurityUserStateChange{Identity: identity, State: pb.SecurityPrincipalState_SECURITY_PRINCIPAL_STATE_ACTIVE}}},
		{Operation: &pb.SecurityChange_PutAssignment{PutAssignment: &pb.SecurityRoleAssignment{Identity: identity, RoleId: "scoped"}}},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	token := f.token(t, "scoped", nil)
	for _, key := range []string{"users:1", "users:2", "users:private:1", "users:private:2", "users:z", "other:1"} {
		if err := f.graph.PutVertex("data:"+key, &pb.Vertex{Key: "data:" + key, Value: &pb.Vertex_String_{String_: key}}); err != nil {
			t.Fatal(err)
		}
	}
	f.graph.AddEdgesWithExpiration([]graphcache.EdgeItem[string]{{Tail: "data:users:1", Head: "data:users:private:1", Weight: 100}, {Tail: "data:users:1", Head: "data:users:2", Weight: 2}, {Tail: "data:other:1", Head: "data:users:2", Weight: 3}, {Tail: "data:users:2", Head: "data:users:z", Weight: 4}})
	for _, order := range []pb.ScanOrder{pb.ScanOrder_SCAN_ORDER_ASC, pb.ScanOrder_SCAN_ORDER_DESC} {
		var cursor []byte
		var keys []string
		for {
			page, err := f.data.ScanVertices(t.Context(), securityWireRequest(token, &pb.ScanVerticesRequest{Prefix: "users:", Limit: 1, Order: order, Cursor: cursor}))
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range page.Msg.Vertices {
				keys = append(keys, v.Key)
			}
			cursor = page.Msg.NextCursor
			if len(cursor) == 0 {
				break
			}
		}
		want := []string{"users:1", "users:2", "users:z"}
		if order == pb.ScanOrder_SCAN_ORDER_DESC {
			want = []string{"users:z", "users:2", "users:1"}
		}
		if !reflect.DeepEqual(keys, want) {
			t.Fatalf("order %v: keys=%v want=%v", order, keys, want)
		}
	}
	keys, err := f.data.ScanVertexKeys(t.Context(), securityWireRequest(token, &pb.ScanVertexKeysRequest{Prefix: "users:", Limit: 10}))
	if err != nil || !reflect.DeepEqual(keys.Msg.Keys, []string{"users:1", "users:2", "users:z"}) {
		t.Fatal("keys exposed denied scope", err)
	}
	count, err := f.data.CountVerticesByPrefix(t.Context(), securityWireRequest(token, &pb.CountVerticesByPrefixRequest{Prefix: "users:"}))
	if err != nil || count.Msg.Count != 3 {
		t.Fatal("count included denied vertices", err)
	}
	for _, prefix := range []string{"other:", "users:private:"} {
		_, err := f.data.CountVerticesByPrefix(t.Context(), securityWireRequest(token, &pb.CountVerticesByPrefixRequest{Prefix: prefix}))
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatal("unauthorized scope was inspected", prefix, err)
		}
	}
	edgePage, err := f.data.ScanEdges(t.Context(), securityWireRequest(token, &pb.ScanEdgesRequest{TailPrefix: "users:", HeadPrefix: "users:", Limit: 1}))
	if err != nil || len(edgePage.Msg.Edges) != 1 || edgePage.Msg.Edges[0].Head != "users:2" || len(edgePage.Msg.NextCursor) == 0 {
		t.Fatal("hidden edge consumed page", err)
	}
	edgeDry, err := f.data.DeleteEdgesByPrefix(t.Context(), securityWireRequest(token, &pb.DeleteEdgesByPrefixRequest{TailPrefix: "users:", HeadPrefix: "users:", Limit: 10, DryRun: true}))
	if err != nil || edgeDry.Msg.Deleted != 2 {
		t.Fatal("edge dry run included hidden endpoints", err)
	}
	edgeDelete, err := f.data.DeleteEdgesByPrefix(t.Context(), securityWireRequest(token, &pb.DeleteEdgesByPrefixRequest{TailPrefix: "users:", HeadPrefix: "users:", Limit: 10}))
	if err != nil || edgeDelete.Msg.Deleted != edgeDry.Msg.Deleted {
		t.Fatal("edge delete widened scope", err)
	}
	if _, _, found := f.graph.GetEdgeDetail("data:users:1", "data:users:private:1"); !found {
		t.Fatal("hidden edge was deleted")
	}
	dry, err := f.data.DeleteVerticesByPrefix(t.Context(), securityWireRequest(token, &pb.DeleteVerticesByPrefixRequest{Prefix: "users:", Limit: 2, DryRun: true}))
	if err != nil || dry.Msg.Deleted != 2 {
		t.Fatal("visible dry run", err)
	}
	deleted, err := f.data.DeleteVerticesByPrefix(t.Context(), securityWireRequest(token, &pb.DeleteVerticesByPrefixRequest{Prefix: "users:", Limit: 2}))
	if err != nil || deleted.Msg.Deleted != dry.Msg.Deleted {
		t.Fatal("prefix delete disagreed with dry run", err)
	}
	for _, key := range []string{"data:users:private:1", "data:users:private:2", "data:other:1", "data:users:z"} {
		if _, found := f.graph.GetVertex(key); !found {
			t.Fatal("delete widened authorized victim set", key)
		}
	}
	page, err := f.data.ScanVertices(t.Context(), securityWireRequest(token, &pb.ScanVerticesRequest{Prefix: "users:", Limit: 1}))
	if err != nil || len(page.Msg.Vertices) != 1 || len(page.Msg.NextCursor) != 0 {
		t.Fatal("hidden keys made a phantom next page", err)
	}
}

func TestAuth_OIDCSharedRankingConstrainedQueriesRealConnect(t *testing.T) {
	f := newOIDCControlWireFixture(t, service.TraversalLimits{WorkBudget: graphcache.PPRWorkBudget{MaxPushes: 10000, MaxTouchedEdges: 1000}})
	admin := f.token(t, "admin", nil)
	principal, err := f.client.GetCurrentPrincipal(t.Context(), securityWireRequest(admin, &pb.GetCurrentPrincipalRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	var rules []*pb.SecurityRule
	for i, action := range []pb.SecurityAction{pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, pb.SecurityAction_SECURITY_ACTION_QUERY} {
		rules = append(rules, &pb.SecurityRule{Id: fmt.Sprintf("allow%d", i), Effect: pb.SecurityEffect_SECURITY_EFFECT_ALLOW, Action: action, Resource: &pb.SecurityRule_Prefix{Prefix: "users:"}})
	}
	rules = append(rules, &pb.SecurityRule{Id: "private", Effect: pb.SecurityEffect_SECURITY_EFFECT_DENY, Action: pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, Resource: &pb.SecurityRule_Prefix{Prefix: "users:private:"}}, &pb.SecurityRule{Id: "blocked-vertex", Effect: pb.SecurityEffect_SECURITY_EFFECT_DENY, Action: pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, Resource: &pb.SecurityRule_Prefix{Prefix: "users:blocked:"}})
	changes := []*pb.SecurityChange{{Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Id: "query", Rules: rules}}}}
	for _, subject := range []string{"query1", "query2"} {
		identity := &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC, Issuer: f.provider.URL, Subject: subject}
		changes = append(changes, &pb.SecurityChange{Operation: &pb.SecurityChange_PutUser{PutUser: &pb.SecurityUserStateChange{Identity: identity, State: pb.SecurityPrincipalState_SECURITY_PRINCIPAL_STATE_ACTIVE}}}, &pb.SecurityChange{Operation: &pb.SecurityChange_PutAssignment{PutAssignment: &pb.SecurityRoleAssignment{Identity: identity, RoleId: "query"}}})
	}
	_, err = f.client.ApplySecurityChanges(t.Context(), securityWireRequest(admin, &pb.ApplySecurityChangesRequest{ExpectedRevision: principal.Msg.Version.Revision, ChangeId: bytes.Repeat([]byte{9}, 16), Changes: changes}))
	if err != nil {
		t.Fatal(err)
	}
	token := f.token(t, "query1", nil)
	put := func(key, text string) {
		t.Helper()
		physical := "data:" + key
		if err := f.graph.PutVertex(physical, &pb.Vertex{Key: physical, Value: &pb.Vertex_String_{String_: text}}); err != nil {
			t.Fatal(err)
		}
	}
	put("users:search:a", "needle launch")
	put("users:search:b", "needle longer ordinary document")
	put("users:private:best", "needle needle needle needle launch")
	searchRequest := &pb.SearchVerticesRequest{Query: "needle", Limit: 1, Projection: pb.SearchProjection_SEARCH_PROJECTION_FULL_VERTEX}
	page, err := f.data.SearchVertices(t.Context(), securityWireRequest(token, searchRequest))
	if err != nil || len(page.Msg.Hits) != 1 || len(page.Msg.NextCursor) == 0 {
		t.Fatal("restricted search did not fill top-k", page, err)
	}
	expected, _, err := f.graph.SearchVerticesMatchContext(context.Background(), "needle", 10, "", search.MatchOptions{}, false, search.Budget{})
	if err != nil {
		t.Fatal(err)
	}
	var wantKeys []string
	var wantScores []float64
	for _, hit := range expected {
		if !strings.Contains(hit.ID, "users:private:") {
			wantKeys = append(wantKeys, strings.TrimPrefix(hit.ID, "data:"))
			wantScores = append(wantScores, hit.Score)
		}
	}
	first := page.Msg.Hits[0]
	if first.Key != wantKeys[0] || first.Score != wantScores[0] || first.Vertex == nil || first.Vertex.Key != first.Key {
		t.Fatal("search did not use shared corpus statistics or hydrate admitted snapshot", first, wantKeys, wantScores)
	}
	stolen := protoCopySearchRequest(searchRequest, page.Msg.NextCursor)
	if _, err := f.data.SearchVertices(t.Context(), securityWireRequest(f.token(t, "query2", nil), stolen)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("same-Role user stole retained search cursor", err)
	}
	// A retained page remains its original snapshot under ordinary data churn.
	put("users:private:ordinary", "unrelated private ordinary corpus document")
	second, err := f.data.SearchVertices(t.Context(), securityWireRequest(token, stolen))
	if err != nil || len(second.Msg.Hits) != 1 || len(second.Msg.NextCursor) != 0 || second.Msg.Hits[0].Key != wantKeys[1] || second.Msg.Hits[0].Score != wantScores[1] {
		t.Fatal("scoped snapshot paging drifted or disclosed hidden hit", second, err)
	}
	fresh, err := f.data.SearchVertices(t.Context(), securityWireRequest(token, searchRequest))
	if err != nil || fresh.Msg.Hits[0].Score == first.Score {
		t.Fatal("private application data excluded from shared statistics", fresh, err)
	}
	count, err := f.data.CountVerticesByPrefix(t.Context(), securityWireRequest(token, &pb.CountVerticesByPrefixRequest{Prefix: "users:"}))
	if err != nil || count.Msg.Count != 2 {
		t.Fatal("shared N leaked into actual count", count, err)
	}
	if _, err := f.data.SearchVertices(t.Context(), securityWireRequest(token, &pb.SearchVerticesRequest{Query: "needle", Prefix: "users:private:"})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("denied search prefix admitted", err)
	}
	for _, edge := range []graphcache.EdgeItem[string]{{Tail: "data:users:seed", Head: "data:users:a", Weight: 3}, {Tail: "data:users:a", Head: "data:users:seed", Weight: 1}, {Tail: "data:users:seed", Head: "data:users:private:bridge", Weight: 1000}, {Tail: "data:users:private:bridge", Head: "data:users:unreachable", Weight: 1000}, {Tail: "data:users:seed", Head: "data:users:blocked:a", Weight: 1000}, {Tail: "data:other:incoming", Head: "data:users:a", Weight: 100}} {
		f.graph.AddEdgesWithExpiration([]graphcache.EdgeItem[string]{edge})
	}
	for _, weighting := range []pb.Weighting{pb.Weighting_WEIGHTING_RAW, pb.Weighting_WEIGHTING_TFIDF, pb.Weighting_WEIGHTING_BM25} {
		request := &pb.IlluminateRequest{Seed: "users:seed", Weighting: weighting, Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 3, FanOut: 1}}}
		result, err := f.data.Illuminate(t.Context(), securityWireRequest(token, request))
		if err != nil {
			t.Fatal(err)
		}
		for _, vertex := range result.Msg.Graph.Vertices {
			if vertex.Key != "users:seed" && vertex.Key != "users:a" {
				t.Fatal("hidden node/bridge traversed", vertex.Key)
			}
		}
		coreWeight := graphcache.WeightingRaw
		if weighting == pb.Weighting_WEIGHTING_TFIDF {
			coreWeight = graphcache.WeightingTFIDF
		}
		if weighting == pb.Weighting_WEIGHTING_BM25 {
			coreWeight = graphcache.WeightingBM25
		}
		base, _, err := f.graph.NeighborWithExpirationsContext(context.Background(), "data:users:seed", 1, 1, coreWeight, false, func(head string) bool { return head == "data:users:a" })
		if err != nil {
			t.Fatal(err)
		}
		for _, edge := range result.Msg.Graph.Edges {
			if edge.Tail == "users:seed" && (edge.Head != "users:a" || edge.Weight != base.Edges["data:users:seed"]["data:users:a"]) {
				t.Fatal("hidden edge disclosed or base weighting was permission-specific", edge)
			}
		}
		request.Params = &pb.IlluminateRequest_Ppr{Ppr: &pb.PprParams{TopN: 10, RestartProb: .2, Epsilon: 1e-4}}
		ppr, err := f.data.Illuminate(t.Context(), securityWireRequest(token, request))
		if err != nil {
			t.Fatal(err)
		}
		for _, vertex := range ppr.Msg.Graph.Vertices {
			if vertex.Key != "users:seed" && vertex.Key != "users:a" {
				t.Fatal("PPR crossed hidden path", vertex.Key)
			}
		}
		request.Params = &pb.IlluminateRequest_Community{Community: &pb.LocalCommunityParams{MaxSize: 10, RestartProb: .2, Epsilon: 1e-4}}
		community, err := f.data.Illuminate(t.Context(), securityWireRequest(token, request))
		if err != nil {
			t.Fatal(err)
		}
		for _, vertex := range community.Msg.Graph.Vertices {
			if vertex.Key != "users:seed" && vertex.Key != "users:a" {
				t.Fatal("community crossed hidden path", vertex.Key)
			}
		}
	}
	degree, err := f.data.TopVerticesByDegree(t.Context(), securityWireRequest(token, &pb.TopVerticesByDegreeRequest{Prefix: "users:seed", K: 1, Weighted: true}))
	if err != nil || len(degree.Msg.Entries) != 1 || degree.Msg.Entries[0].Degree != 1 || degree.Msg.Entries[0].WeightedDegree != 3 {
		t.Fatal("shared ranking statistics leaked into degree aggregate", degree, err)
	}
	if _, err := f.data.Illuminate(t.Context(), securityWireRequest(token, &pb.IlluminateRequest{Seed: "users:private:bridge", Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 1, FanOut: 1}}})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("hidden seed bypassed authorization", err)
	}
	// Resource limits count actual scan work, but public errors do not expose
	// physical counters from denied adjacency rows.
	for i := range 1100 {
		f.graph.AddEdge("data:users:seed", fmt.Sprintf("data:users:private:wide:%d", i), 1)
	}
	for _, community := range []bool{false, true} {
		request := &pb.IlluminateRequest{Seed: "users:seed", Weighting: pb.Weighting_WEIGHTING_BM25, Params: &pb.IlluminateRequest_Ppr{Ppr: &pb.PprParams{TopN: 10}}}
		if community {
			request.Params = &pb.IlluminateRequest_Community{Community: &pb.LocalCommunityParams{MaxSize: 10}}
		}
		_, err := f.data.Illuminate(t.Context(), securityWireRequest(token, request))
		var failure *connect.Error
		if connect.CodeOf(err) != connect.CodeResourceExhausted || !errors.As(err, &failure) || failure.Message() != graphcache.ErrPPRWorkBudgetExceeded.Error() {
			t.Fatal("denied adjacency escaped the budget or disclosed physical counters", community, err)
		}
	}
	// Policy edits invalidate protected result continuations even though
	// corpus ranking statistics are shared and have no policy cache.
	current, err := f.client.GetCurrentPrincipal(t.Context(), securityWireRequest(admin, &pb.GetCurrentPrincipalRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	rules = append(rules, &pb.SecurityRule{Id: "revoke-search", Effect: pb.SecurityEffect_SECURITY_EFFECT_DENY, Action: pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, Resource: &pb.SecurityRule_Prefix{Prefix: "users:search:"}})
	_, err = f.client.ApplySecurityChanges(t.Context(), securityWireRequest(admin, &pb.ApplySecurityChangesRequest{ExpectedRevision: current.Msg.Version.Revision, ChangeId: bytes.Repeat([]byte{10}, 16), Changes: []*pb.SecurityChange{{Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Id: "query", Rules: rules}}}}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.data.SearchVertices(t.Context(), securityWireRequest(token, protoCopySearchRequest(searchRequest, fresh.Msg.NextCursor))); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("policy change retained search continuation", err)
	}
}

func protoCopySearchRequest(request *pb.SearchVerticesRequest, cursor []byte) *pb.SearchVerticesRequest {
	return &pb.SearchVerticesRequest{Query: request.Query, Prefix: request.Prefix, Limit: request.Limit, Options: request.Options, Projection: request.Projection, Cursor: cursor}
}

func TestAuth_OFFScopedCursorQualifiedClockOverConnect(t *testing.T) {
	var mu sync.Mutex
	now := time.Now().Truncate(time.Second)
	issued := now
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	setTime := func(value time.Time) { mu.Lock(); now = value; mu.Unlock() }
	cache := provider.NewGraphCache(provider.CacheConfig{TTL: time.Hour}, provider.SearchConfig{})
	data, err := service.NewGraphOnlyServingRuntime(cache, mutationlog.New(mutationlog.Options{Capacity: 16}), hlc.New(hlc.NodeID{9}, hlc.Options{}), "namespaced-v1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = data.Close() })
	runtime, cleanup, err := provider.NewSecurityRuntime(provider.SecurityConfig{Mode: "off", Clock: clock}, data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	path, handler, err := runtime.PublicChangeHTTPHandler(data.NewLanternService(nil), service.ChangeServiceOptions{CursorKeys: []service.ChangeCursorKey{{Version: 1, Key: [32]byte{7}}}, CurrentKeyVersion: 1, Heartbeat: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	client := graphv1connect.NewLanternChangeServiceClient(srv.Client(), srv.URL)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	bootstrap, err := client.WatchChanges(ctx, connect.NewRequest(&pb.WatchChangesRequest{Bootstrap: true, Projection: pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY}))
	if err != nil || !bootstrap.Receive() {
		t.Fatal("bootstrap", err, bootstrap.Err())
	}
	cursor := bytes.Clone(bootstrap.Msg().Cursor)
	_ = bootstrap.Close()
	for _, skew := range []time.Duration{-time.Second, -2 * time.Second, -3 * time.Second, 10 * time.Minute} {
		setTime(issued.Add(skew))
		stream, err := client.WatchChanges(ctx, connect.NewRequest(&pb.WatchChangesRequest{Cursor: cursor, Projection: pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY}))
		if err != nil {
			t.Fatal(err)
		}
		if skew >= -2*time.Second && skew < 0 {
			if !stream.Receive() || len(stream.Msg().Invalidations) != 0 {
				t.Fatal("qualified resume", skew, stream.Err())
			}
		} else if stream.Receive() || connect.CodeOf(stream.Err()) != connect.CodeFailedPrecondition {
			t.Fatal("unqualified/expired cursor resumed", skew, stream.Err())
		}
		_ = stream.Close()
	}
}

func TestAuth_NodeSecurityFacadeRealConnect(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("Bun is required for the Node control-plane wire gate")
	}
	runtime, cleanup, err := provider.NewSecurityRuntime(provider.SecurityConfig{Mode: "off"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	mux := http.NewServeMux()
	mux.Handle(runtime.PublicControlHTTPHandler())
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	sdkEntry, err := filepath.Abs(filepath.Join(cwd, "../../sdks/node/src/web.ts"))
	if err != nil {
		t.Fatal(err)
	}
	entry, err := json.Marshal(sdkEntry)
	if err != nil {
		t.Fatal(err)
	}
	script := `import {connectSecurityWeb, AuthMode, FailedPreconditionError} from ` + string(entry) + `;
const client = connectSecurityWeb(process.env.LANTERN_SECURITY_WIRE_URL);
const caps = await client.getAuthCapabilities();
if (caps.mode !== AuthMode.OFF || caps.protocolVersion !== 1 || !caps.ready) throw new Error("invalid OFF capabilities");
try { await client.listRoles(); throw new Error("OFF management admitted"); }
catch (error) { if (!(error instanceof FailedPreconditionError)) throw error; }
`
	file := filepath.Join(t.TempDir(), "control-wire.ts")
	if err := os.WriteFile(file, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "run", file)
	cmd.Env = append(os.Environ(), "LANTERN_SECURITY_WIRE_URL="+srv.URL)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Node control wire: %v\n%s", err, output)
	}
}

// SDK facades exercise the production public authorization boundary, rather
// than the private per-origin replication protocol.
func newScopedSDKFixture(t *testing.T) *oidcControlWireFixture {
	t.Helper()
	f := newOIDCControlWireFixtureOptions(t, nil, true)
	admin := f.token(t, "admin", nil)
	current, err := f.client.GetCurrentPrincipal(t.Context(), securityWireRequest(admin, &pb.GetCurrentPrincipalRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	rule := func(id string, action pb.SecurityAction, effect pb.SecurityEffect, prefix string) *pb.SecurityRule {
		return &pb.SecurityRule{Id: id, Action: action, Effect: effect, Resource: &pb.SecurityRule_Prefix{Prefix: prefix}}
	}
	rules := []*pb.SecurityRule{rule("cdc", pb.SecurityAction_SECURITY_ACTION_CDC_IDENTITY, pb.SecurityEffect_SECURITY_EFFECT_ALLOW, "orders:"), rule("private", pb.SecurityAction_SECURITY_ACTION_CDC_IDENTITY, pb.SecurityEffect_SECURITY_EFFECT_DENY, "orders:private:"), rule("write", pb.SecurityAction_SECURITY_ACTION_VERTEX_WRITE, pb.SecurityEffect_SECURITY_EFFECT_ALLOW, "orders:"), rule("read", pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, pb.SecurityEffect_SECURITY_EFFECT_ALLOW, "orders:")}
	rules = append(rules, rule("receipt", pb.SecurityAction_SECURITY_ACTION_RECEIPT_READ, pb.SecurityEffect_SECURITY_EFFECT_ALLOW, "orders:create:"))
	identity := &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC, Issuer: f.provider.URL, Subject: "sdk"}
	_, err = f.client.ApplySecurityChanges(t.Context(), securityWireRequest(admin, &pb.ApplySecurityChangesRequest{ExpectedRevision: current.Msg.Version.Revision, ChangeId: bytes.Repeat([]byte{73}, 16), Changes: []*pb.SecurityChange{
		{Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Id: "sdk", Rules: rules}}},
		{Operation: &pb.SecurityChange_PutUser{PutUser: &pb.SecurityUserStateChange{Identity: identity, State: pb.SecurityPrincipalState_SECURITY_PRINCIPAL_STATE_ACTIVE}}},
		{Operation: &pb.SecurityChange_PutAssignment{PutAssignment: &pb.SecurityRoleAssignment{Identity: identity, RoleId: "sdk"}}},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestAuth_ChangesBoundedTerminationAndCursorResumeHTTP2(t *testing.T) {
	verify := func(t *testing.T, changes graphv1connect.LanternChangeServiceClient, token, refreshed string, maxDuration time.Duration) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), maxDuration+5*time.Second)
		defer cancel()
		started := time.Now()
		stream, err := changes.WatchChanges(ctx, securityWireRequest(token, &pb.WatchChangesRequest{Prefix: "orders:", Bootstrap: true, Projection: pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY}))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = stream.Close() }()
		var cursor []byte
		for stream.Receive() {
			frame := stream.Msg()
			if len(frame.Cursor) > 0 {
				cursor = bytes.Clone(frame.Cursor)
			}
		}
		if connect.CodeOf(stream.Err()) != connect.CodeDeadlineExceeded || len(cursor) == 0 || time.Since(started) > maxDuration+time.Second {
			t.Fatal("bounded HTTP/2 stream lost terminal status or proven progress", stream.Err())
		}
		resume, err := changes.WatchChanges(ctx, securityWireRequest(refreshed, &pb.WatchChangesRequest{Prefix: "orders:", Cursor: cursor, Projection: pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY}))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resume.Close() }()
		if !resume.Receive() || resume.Msg().Bootstrap || len(resume.Msg().Cursor) == 0 {
			t.Fatal("same-endpoint opaque cursor could not resume", resume.Err())
		}
	}
	t.Run("short OIDC admission", func(t *testing.T) {
		f := newScopedSDKFixture(t)
		changes := graphv1connect.NewLanternChangeServiceClient(&http.Client{Transport: authIngressRoundTripper{h2cClient().Transport}}, f.server.URL, connect.WithGRPC())
		short := f.token(t, "sdk", func(claims map[string]any) { claims["exp"] = f.clock().Add(2 * time.Second).Unix() })
		verify(t, changes, short, f.token(t, "sdk", nil), 2*time.Second)
	})
	t.Run("OFF existing admission bound", func(t *testing.T) {
		cache := provider.NewGraphCache(provider.CacheConfig{TTL: time.Hour}, provider.SearchConfig{})
		data, err := service.NewGraphOnlyServingRuntime(cache, mutationlog.New(mutationlog.Options{Capacity: 16}), hlc.New(hlc.NodeID{9}, hlc.Options{}), "namespaced-v1")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = data.Close() })
		runtime, cleanup, err := provider.NewSecurityRuntime(provider.SecurityConfig{Mode: "off"}, data)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(cleanup)
		path, handler, err := runtime.PublicChangeHTTPHandler(data.NewLanternService(nil), service.ChangeServiceOptions{CursorKeys: []service.ChangeCursorKey{{Version: 1, Key: [32]byte{7}}}, CurrentKeyVersion: 1, Heartbeat: 100 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		mux := http.NewServeMux()
		mux.Handle(path, handler)
		srv := httptest.NewUnstartedServer(mux)
		protocols := new(http.Protocols)
		protocols.SetHTTP1(true)
		protocols.SetUnencryptedHTTP2(true)
		srv.Config.Protocols = protocols
		srv.Start()
		t.Cleanup(srv.Close)
		verify(t, graphv1connect.NewLanternChangeServiceClient(h2cClient(), srv.URL, connect.WithGRPC()), "", "", 28*time.Second)
	})
}

func TestAuth_OIDCGoScopedChangesFacadeRealConnect(t *testing.T) {
	f := newScopedSDKFixture(t)
	sdk, err := client.NewLantern(f.server.URL, client.WithHTTPClient(&http.Client{Transport: authIngressRoundTripper{h2cClient().Transport}}), client.WithAuthToken(f.token(t, "sdk", nil)), client.WithDefaultTimeout(time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sdk.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	bootstrap := false
	for frame, err := range sdk.WatchChanges(ctx, client.WatchChangesOptions{Prefix: "orders:", Bootstrap: true}) {
		if err != nil {
			t.Fatal(err)
		}
		if frame.Bootstrap {
			bootstrap = true
			if len(frame.Cursor.Bytes()) == 0 || len(frame.Invalidations) != 0 {
				t.Fatal("bootstrap contract", frame)
			}
			if _, err := f.data.PutVertices(ctx, securityWireRequest(f.token(t, "sdk", nil), &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "orders:private:1"}, {Key: "orders:1"}}})); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if len(frame.Invalidations) == 0 {
			continue
		}
		if !bootstrap || len(frame.Invalidations) != 1 || frame.Invalidations[0].Key != "orders:1" || frame.Invalidations[0].Vertex != nil {
			t.Fatal("scope disclosure", frame)
		}
		break
	}
	for _, err := range sdk.WatchChanges(ctx, client.WatchChangesOptions{Prefix: "orders:", Bootstrap: true, Projection: client.ChangeValue}) {
		if connectCode(err) != connect.CodePermissionDenied {
			t.Fatal("value projection inherited grant", err)
		}
		break
	}
}

func TestAuth_PublicChangeRejectionPreservesRPCProtocol(t *testing.T) {
	f := newScopedSDKFixture(t)
	for _, protocol := range []struct {
		name    string
		options []connect.ClientOption
	}{{"connect", nil}, {"grpc", []connect.ClientOption{connect.WithGRPC()}}, {"grpc-web", []connect.ClientOption{connect.WithGRPCWeb()}}} {
		t.Run(protocol.name, func(t *testing.T) {
			raw := graphv1connect.NewLanternChangeServiceClient(&http.Client{Transport: authIngressRoundTripper{h2cClient().Transport}}, f.server.URL, protocol.options...)
			for _, test := range []struct {
				token string
				code  connect.Code
			}{
				{"wrong", connect.CodeUnauthenticated},
				{f.token(t, "admin", nil), connect.CodePermissionDenied},
			} {
				stream, err := raw.WatchChanges(t.Context(), securityWireRequest(test.token, &pb.WatchChangesRequest{Prefix: "orders:", Bootstrap: true, Projection: pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY}))
				if err == nil {
					if stream.Receive() {
						t.Fatal("rejected principal received a frame")
					}
					err = stream.Err()
					_ = stream.Close()
				}
				if connect.CodeOf(err) != test.code {
					t.Fatal("RPC rejection status lost", test.code, err)
				}
			}
			accepted, err := raw.WatchChanges(t.Context(), securityWireRequest(f.token(t, "sdk", nil), &pb.WatchChangesRequest{Prefix: "orders:", Bootstrap: true, Projection: pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY}))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = accepted.Close() }()
			if !accepted.Receive() || !accepted.Msg().Bootstrap || len(accepted.Msg().Cursor) == 0 {
				t.Fatal("authorized stream rejected", accepted.Err())
			}
		})
	}
}

func TestAuth_OIDCNodeScopedChangesFacadeRealConnect(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("Bun is required for the Node public CDC wire gate")
	}
	f := newScopedSDKFixture(t)
	entry, err := filepath.Abs("../../sdks/node/src/web.ts")
	if err != nil {
		t.Fatal(err)
	}
	quoted, _ := json.Marshal(entry)
	script := `import { connectWeb, LanternError, BatchError, mintReceiptOperationContext } from ` + string(quoted) + `;
const gatewayFetch = (input, init) => {
 const headers=new Headers(init?.headers); headers.set("X-Forwarded-Proto","https"); headers.set("X-Forwarded-Host",new URL(typeof input==="string" ? input : input.url).host);
 return fetch(input,{...init,headers});
};
const sdk=connectWeb(process.env.LANTERN_SDK_URL,{token:process.env.LANTERN_SDK_CREDENTIAL,transportOptions:{fetch:gatewayFetch}});
const cancellation=new AbortController();
let bootstrapped=false, visible=false;
try {
 for await (const frame of sdk.watchChanges({prefix:"orders:",bootstrap:true},cancellation.signal)) {
  if(frame.bootstrap) {
   if(!frame.cursor || frame.invalidations.length) throw new Error("invalid bootstrap"); bootstrapped=true;
   await sdk.putVertices([{key:"orders:private:1",value:"hidden"},{key:"orders:1",value:"visible"}]); continue;
  }
  if(!frame.invalidations.length) continue;
  if(!bootstrapped || frame.invalidations.length!==1 || frame.invalidations[0].key!=="orders:1" || frame.invalidations[0].current!==undefined) throw new Error("private data disclosed");
  visible=true; break;
 }
 if(!visible) throw new Error("visible event missing");
 await sdk.putVertices([{key:"orders:create:source:a",value:"source"},{key:"orders:create:target:b",value:"target"}]);
 const input={tail:"orders:create:source:a",head:"orders:create:target:b",weight:2};
 const created=await sdk.createEdges([input,{...input,weight:9},{...input,head:"orders:create:target:missing"},{...input,expiration:new Date(1)}]);
 if(JSON.stringify(created)!==JSON.stringify(["createdAndLive","edgeExists","endpointNotLive","expired"])) throw new Error("invalid Create outcomes");
 const capability=await sdk.getReceiptCapability(); if(!capability.enabled) throw new Error("Create receipts disabled");
 const context=mintReceiptOperationContext(capability,1);
 if(await sdk.createEdgeWithReceipt(input,context)!=="edgeExists") throw new Error("receipt collision changed existing Edge");
 const status=await sdk.getReceiptStatus(context.operationIds[0]);
 if(status.state!=="confirmed" || status.receipt.originalResult.kind!=="createEdge" || status.receipt.originalResult.outcome!=="edgeExists") throw new Error("original Create result missing");
 if(await sdk.createEdge({tail:input.head,head:input.tail,weight:1})!=="createdAndLive") throw new Error("whole-prefix Vertex grants did not derive reverse authority");
 try {await sdk.createEdge({tail:input.tail,head:"outside:denied",weight:1}); throw new Error("outside Head authorized");}
 catch(error) {const cause=error instanceof BatchError?error.cause:error; if(!(cause instanceof LanternError) || cause.cause?.code!==7) throw error;}

 try { for await(const frame of sdk.watchChanges({prefix:"orders:",projection:"value",bootstrap:true})) throw new Error("value projection inherited grant"); }
 catch(error) { if(!(error instanceof LanternError) || error.cause?.code!==7) throw error; }
} finally { cancellation.abort(); sdk.close(); }
`
	file := filepath.Join(t.TempDir(), "scoped-wire.ts")
	if err := os.WriteFile(file, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "run", file)
	cmd.Env = append(os.Environ(), "LANTERN_SDK_URL="+f.server.URL, "LANTERN_SDK_CREDENTIAL="+f.token(t, "sdk", nil))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Node public CDC wire: %v\n%s", err, output)
	}
}

// #1671: exercise the production OIDC/Connect path, Node SDK wrapping, and
// Admin adapters/handlers. Bootstrap management authority never implies data read.
func TestAuth_OIDCAdminVertexCountRealConnect(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("Bun is required for the Admin vertex-count wire gate")
	}
	f := newOIDCControlWireFixture(t)
	admin := f.token(t, "admin", nil)
	principal, err := f.client.GetCurrentPrincipal(t.Context(), securityWireRequest(admin, &pb.GetCurrentPrincipalRequest{}))
	if err != nil || len(principal.Msg.Roles) != 1 || principal.Msg.Roles[0].Id != "security_admin" || len(principal.Msg.Roles[0].Rules) != 1 || principal.Msg.Roles[0].Rules[0].Action != pb.SecurityAction_SECURITY_ACTION_MANAGE {
		t.Fatal("bootstrap must have management authority without data grants", err)
	}
	if err := f.graph.PutVertex("data:tenant:a", &pb.Vertex{Key: "data:tenant:a", Value: &pb.Vertex_String_{String_: "visible"}}); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	module := func(path string) string {
		quoted, err := json.Marshal(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		return string(quoted)
	}
	script := `import { connectWeb, LanternError } from ` + module("admin/node_modules/lantern-sdk/dist/web.js") + `;
import { LanternApiError } from ` + module("admin/app/lib/client/infrastructure/api/error.ts") + `;
import { countVerticesByPrefix } from ` + module("admin/app/lib/client/infrastructure/api/count-vertices-by-prefix.ts") + `;
import { fetchCount, fetchPage } from ` + module("admin/app/lib/client/usecase/browse-vertices/handlers.ts") + `;
import { browseVerticesReducer } from ` + module("admin/app/lib/client/usecase/browse-vertices/reducer.ts") + `;
import { INITIAL_BROWSE_VERTICES_STATE } from ` + module("admin/app/lib/client/usecase/browse-vertices/state.ts") + `;
const gatewayFetch = (input, init) => {
 const headers = new Headers(init?.headers);
 headers.set("X-Forwarded-Proto", "https");
 headers.set("X-Forwarded-Host", new URL(typeof input === "string" ? input : input.url).host);
 return fetch(input, {...init, headers});
};
const client = connectWeb(process.env.LANTERN_SDK_URL, {token: process.env.LANTERN_SDK_CREDENTIAL, transportOptions: {fetch: gatewayFetch}});
const require = (condition, message) => { if (!condition) throw new Error(message); };
let state = INITIAL_BROWSE_VERTICES_STATE;
const dispatch = action => { state = browseVerticesReducer(state, action); };
const input = {client, prefix: "tenant:", epoch: 0, requestId: 1};
try {
 if (process.env.LANTERN_ADMIN_WIRE_PHASE === "denied") {
  let denied = false;
  try { await client.countVerticesByPrefix("tenant:"); }
  catch (error) {
   require(error instanceof LanternError && error.cause?.code === 7, "SDK did not preserve real PermissionDenied cause");
   const adapted = LanternApiError.fromUnknown("CountVerticesByPrefix", error);
   require(adapted instanceof LanternApiError && adapted.code === "permission_denied", "Admin lost structured denial");
   denied = true;
  }
  require(denied, "bootstrap unexpectedly received data count");
 }
 await fetchPage({...input, cursor: "", pageSize: 50}, dispatch);
 await fetchCount(input, dispatch);
 if (process.env.LANTERN_ADMIN_WIRE_PHASE === "denied") {
  require(state.status === "error" && state.error?.kind === "denied" && state.count.status === "denied", "Admin did not retain independent scan/count denial");
  require(state.pages.length === 0 && !("count" in state.count), "denial fabricated data or zero");
 } else {
  require(state.status === "ready" && state.error === null && state.pages[0]?.vertices[0]?.key === "tenant:a", "explicit read Role did not authorize scan");
  require(state.count.status === "success" && state.count.count === 1, "explicit read Role did not authorize count");
  require(await countVerticesByPrefix(client, "tenant:empty:") === 0, "successful empty count lost zero");
  let denied = false;
  try { await countVerticesByPrefix(client, "outside:"); }
  catch (error) { denied = error instanceof LanternApiError && error.code === "permission_denied"; }
  require(denied, "scoped Role accidentally authorized an outside prefix");
 }
 console.log("Admin vertex count " + process.env.LANTERN_ADMIN_WIRE_PHASE + ": PASS");
} finally { client.close(); }
`
	file := filepath.Join(t.TempDir(), "admin-vertex-count-wire.ts")
	if err := os.WriteFile(file, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(phase string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bun, "run", file)
		cmd.Dir = filepath.Join(root, "admin")
		cmd.Env = append(os.Environ(), "LANTERN_SDK_URL="+f.server.URL, "LANTERN_SDK_CREDENTIAL="+f.token(t, "admin", nil), "LANTERN_ADMIN_WIRE_PHASE="+phase)
		if output, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(output), "Admin vertex count "+phase+": PASS") {
			t.Fatalf("Admin OIDC vertex-count wire (%s): %v\n%s", phase, err, output)
		} else {
			t.Log(strings.TrimSpace(string(output)))
		}
	}
	run("denied")
	role := &pb.SecurityRole{Id: "data_reader", Rules: []*pb.SecurityRule{{Id: "read", Effect: pb.SecurityEffect_SECURITY_EFFECT_ALLOW, Action: pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, Resource: &pb.SecurityRule_Prefix{Prefix: "tenant:"}}}}
	_, err = f.client.ApplySecurityChanges(t.Context(), securityWireRequest(admin, &pb.ApplySecurityChangesRequest{
		ExpectedRevision: principal.Msg.Version.Revision, ChangeId: bytes.Repeat([]byte{119}, 16),
		Changes: []*pb.SecurityChange{
			{Operation: &pb.SecurityChange_PutRole{PutRole: role}},
			{Operation: &pb.SecurityChange_PutAssignment{PutAssignment: &pb.SecurityRoleAssignment{Identity: principal.Msg.Identity, RoleId: role.Id}}},
		},
	}))
	if err != nil {
		t.Fatal("explicit data Role assignment", err)
	}
	run("authorized")
}

func runScopedLanguageWire(t *testing.T, language string) {
	t.Helper()
	binary, err := exec.LookPath(language)
	if err != nil {
		t.Skip(language + " is required for the public CDC wire gate")
	}
	f := newScopedSDKFixture(t)
	tlsServer, caFile := scopedSDKTLSServer(t, f)
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	var cmd *exec.Cmd
	switch language {
	case "dart":
		cmd = exec.CommandContext(ctx, binary, "test", "integration_test/scoped_changes_test.dart")
		cmd.Dir = filepath.Join(root, "sdks/dart")
	case "cargo":
		cmd = exec.CommandContext(ctx, binary, "test", "--locked", "--lib", "scoped_changes::tests::real_public_scoped_wire", "--", "--ignored", "--exact")
		cmd.Dir = filepath.Join(root, "sdks/rust")
	default:
		t.Fatal("unknown SDK")
	}
	cmd.Env = append(os.Environ(), "LANTERN_SCOPED_WIRE_URL="+tlsServer.URL, "LANTERN_SCOPED_WIRE_CA="+caFile, "LANTERN_SCOPED_WIRE_CREDENTIAL="+f.token(t, "sdk", nil))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s public CDC wire: %v\n%s", language, err, output)
	}
	if language == "cargo" && !strings.Contains(string(output), "test scoped_changes::tests::real_public_scoped_wire ... ok") {
		t.Fatalf("Rust public CDC wire did not execute the selected test:\n%s", output)
	}
}
func TestAuth_OIDCDartScopedChangesFacadeRealConnect(t *testing.T) { runScopedLanguageWire(t, "dart") }
func TestAuth_OIDCRustScopedChangesFacadeRealConnect(t *testing.T) { runScopedLanguageWire(t, "cargo") }

func TestAuth_OFFPublicChangeLifecycleRealConnect(t *testing.T) {
	cache := provider.NewGraphCache(provider.CacheConfig{TTL: time.Hour}, provider.SearchConfig{})
	data, err := service.NewGraphOnlyServingRuntime(cache, mutationlog.New(mutationlog.Options{Capacity: 16}), hlc.New(hlc.NodeID{9}, hlc.Options{}), "namespaced-v1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = data.Close() })
	runtime, cleanup, err := provider.NewSecurityRuntime(provider.SecurityConfig{Mode: "off"}, data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	registry := prometheus.NewRegistry()
	metrics := domainmetrics.New(registry, domainmetrics.Options{})
	configured := provider.NewChangeConfig(&provider.Config{Changes: provider.ChangeConfig{Enabled: true, Options: service.ChangeServiceOptions{CursorKeys: []service.ChangeCursorKey{{Version: 1, Key: [32]byte{7}}}, CurrentKeyVersion: 1}}}, metrics)
	path, handler, err := runtime.PublicChangeHTTPHandler(data.NewLanternService(nil), configured.Options)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client := graphv1connect.NewLanternChangeServiceClient(server.Client(), server.URL)
	active := func() float64 {
		families, err := registry.Gather()
		if err != nil {
			t.Fatal(err)
		}
		for _, family := range families {
			if family.GetName() == "lantern_changes_active_streams" {
				return family.Metric[0].GetGauge().GetValue()
			}
		}
		t.Fatal("public CDC gauge missing")
		return -1
	}
	rejected, err := client.WatchChanges(t.Context(), connect.NewRequest(&pb.WatchChangesRequest{Projection: pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY, Cursor: []byte{1}}))
	if err == nil {
		for rejected.Receive() {
		}
		err = rejected.Err()
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || active() != 0 {
		t.Fatal("rejected cursor counted as active", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := client.WatchChanges(ctx, connect.NewRequest(&pb.WatchChangesRequest{Projection: pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY, Bootstrap: true}))
	if err != nil || !stream.Receive() || !stream.Msg().Bootstrap || active() != 1 {
		t.Fatal("accepted stream not observed", err)
	}
	cancel()
	_ = stream.Close()
	deadline := time.Now().Add(time.Second)
	for active() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if active() != 0 {
		t.Fatal("canceled stream retained subscriber")
	}
}

func scopedSDKTLSServer(t *testing.T, f *oidcControlWireFixture) (*httptest.Server, string) {
	t.Helper()
	tlsServer := httptest.NewUnstartedServer(f.server.Config.Handler)
	tlsServer.EnableHTTP2 = true
	caPrivate, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ca := &x509.Certificate{Subject: pkix.Name{CommonName: "Lantern SDK conformance CA"}, SerialNumber: big.NewInt(1), NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caPrivate.PublicKey, caPrivate)
	if err != nil {
		t.Fatal(err)
	}
	leafPrivate, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{Subject: pkix.Name{CommonName: "Lantern SDK conformance listener"}, SerialNumber: big.NewInt(2), NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), BasicConstraintsValid: true, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, &leafPrivate.PublicKey, caPrivate)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(leafPrivate)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	tlsServer.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	tlsServer.StartTLS()
	t.Cleanup(tlsServer.Close)
	caFile := filepath.Join(t.TempDir(), "public-ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0600); err != nil {
		t.Fatal(err)
	}
	return tlsServer, caFile
}

func TestAuth_PublicChangesBenchmarkConsumerRealWire(t *testing.T) {
	f := newScopedSDKFixture(t)
	server, caFile := scopedSDKTLSServer(t, f)
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "changeprobe")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./testbed/bench/changeprobe")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("consumer build: %v %s", err, output)
	}
	for _, tc := range []struct {
		name, subject string
		success       bool
	}{{"authorized scope", "sdk", true}, {"missing CDC Role", "admin", false}} {
		t.Run(tc.name, func(t *testing.T) {
			report := filepath.Join(t.TempDir(), "consumer.json")
			command := exec.CommandContext(t.Context(), binary, "-endpoint", server.URL, "-ca-file", caFile, "-duration", "1s", "-out", report, "-request", `{"prefix":"orders:","projection":"CHANGE_PROJECTION_IDENTITY","bootstrap":true}`)
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "LANTERN_BENCH_AUTH_TOKEN=") {
					command.Env = append(command.Env, entry)
				}
			}
			command.Env = append(command.Env, "LANTERN_BENCH_AUTH_TOKEN="+f.token(t, tc.subject, nil))
			output, err := command.CombinedOutput()
			if (err == nil) != tc.success {
				t.Fatalf("consumer success=%v: %v %s", tc.success, err, output)
			}
			raw, readErr := os.ReadFile(report)
			var progress struct {
				CursorCommits   uint64 `json:"cursor_commits"`
				Frames          uint64 `json:"frames"`
				FailureCategory string `json:"failure_category"`
			}
			if !tc.success {
				if readErr != nil || json.Unmarshal(raw, &progress) != nil || progress.FailureCategory != "rpc_permission_denied" || progress.CursorCommits != 0 || progress.Frames != 0 {
					t.Fatal("rejected consumer did not record a bounded failure without authorized progress")
				}
				return
			}
			if readErr != nil || json.Unmarshal(raw, &progress) != nil || progress.FailureCategory != "" || progress.CursorCommits == 0 || progress.Frames == 0 {
				t.Fatal("consumer never established authorized public progress")
			}
		})
	}
}

func TestAuth_DartOfflineScopedChangesRealConnect(t *testing.T) {
	flutter, err := exec.LookPath("flutter")
	if err != nil {
		t.Skip("Flutter is required for the offline public CDC composition gate")
	}
	f := newScopedSDKFixture(t)
	server, ca := scopedSDKTLSServer(t, f)
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, flutter, "test", "--no-pub", "test/scoped_change_source_test.dart")
	cmd.Dir = filepath.Join(root, "sdks/dart/example")
	cmd.Env = append(os.Environ(), "LANTERN_SCOPED_WIRE_URL="+server.URL, "LANTERN_SCOPED_WIRE_CA="+ca, "LANTERN_SCOPED_WIRE_CREDENTIAL="+f.token(t, "sdk", nil), "LANTERN_SCOPED_WIRE_DENIED_CREDENTIAL="+f.token(t, "admin", nil))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("offline public scoped CDC wire: %v\n%s", err, output)
	}
}

func TestAuth_OIDCHeadManagedEdgesRealConnect(t *testing.T) {
	f := newOIDCControlWireFixtureOptions(t, nil, true, service.TraversalLimits{WorkBudget: graphcache.PPRWorkBudget{MaxPushes: 10000, MaxTouchedEdges: 10000}})
	admin := f.token(t, "admin", nil)
	current, err := f.client.GetCurrentPrincipal(t.Context(), securityWireRequest(admin, &pb.GetCurrentPrincipalRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	prefix := func(id string, action pb.SecurityAction, effect pb.SecurityEffect, value string) *pb.SecurityRule {
		return &pb.SecurityRule{Id: id, Action: action, Effect: effect, Resource: &pb.SecurityRule_Prefix{Prefix: value}}
	}
	readerRules := []*pb.SecurityRule{
		prefix("read-users", pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, pb.SecurityEffect_SECURITY_EFFECT_ALLOW, "users:"),
		prefix("read-targets", pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, pb.SecurityEffect_SECURITY_EFFECT_ALLOW, "targets:"),
		prefix("write-heads", pb.SecurityAction_SECURITY_ACTION_VERTEX_WRITE, pb.SecurityEffect_SECURITY_EFFECT_ALLOW, "targets:"),
		prefix("query", pb.SecurityAction_SECURITY_ACTION_QUERY, pb.SecurityEffect_SECURITY_EFFECT_ALLOW, ""),
		prefix("export", pb.SecurityAction_SECURITY_ACTION_EXPORT, pb.SecurityEffect_SECURITY_EFFECT_ALLOW, ""),
		prefix("receipt-users", pb.SecurityAction_SECURITY_ACTION_RECEIPT_READ, pb.SecurityEffect_SECURITY_EFFECT_ALLOW, "users:"),
		prefix("receipt-targets", pb.SecurityAction_SECURITY_ACTION_RECEIPT_READ, pb.SecurityEffect_SECURITY_EFFECT_ALLOW, "targets:"),
	}
	for _, action := range []pb.SecurityAction{pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, pb.SecurityAction_SECURITY_ACTION_VERTEX_WRITE, pb.SecurityAction_SECURITY_ACTION_RECEIPT_READ} {
		for i, denied := range []string{"targets:private:", "targets:secret:"} {
			readerRules = append(readerRules, prefix(fmt.Sprintf("deny_%s_%d", strings.ToLower(action.String()), i), action, pb.SecurityEffect_SECURITY_EFFECT_DENY, denied))
		}
	}
	var identityRules []*pb.SecurityRule
	for _, action := range []pb.SecurityAction{pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, pb.SecurityAction_SECURITY_ACTION_CDC_IDENTITY} {
		identityRules = append(identityRules,
			prefix("users_"+strings.ToLower(action.String()), action, pb.SecurityEffect_SECURITY_EFFECT_ALLOW, "users:"),
			prefix("targets_"+strings.ToLower(action.String()), action, pb.SecurityEffect_SECURITY_EFFECT_ALLOW, "targets:"),
			prefix("private_"+strings.ToLower(action.String()), action, pb.SecurityEffect_SECURITY_EFFECT_DENY, "targets:private:"),
			prefix("secret_"+strings.ToLower(action.String()), action, pb.SecurityEffect_SECURITY_EFFECT_DENY, "targets:secret:"))
	}
	creatorRules := []*pb.SecurityRule{
		prefix("own-tail-read", pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, pb.SecurityEffect_SECURITY_EFFECT_ALLOW, "users:1"),
		prefix("head-read", pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, pb.SecurityEffect_SECURITY_EFFECT_ALLOW, "targets:"),
		prefix("head-write", pb.SecurityAction_SECURITY_ACTION_VERTEX_WRITE, pb.SecurityEffect_SECURITY_EFFECT_ALLOW, "targets:"),
		prefix("receipt-tail", pb.SecurityAction_SECURITY_ACTION_RECEIPT_READ, pb.SecurityEffect_SECURITY_EFFECT_ALLOW, "users:1"),
		prefix("receipt-head", pb.SecurityAction_SECURITY_ACTION_RECEIPT_READ, pb.SecurityEffect_SECURITY_EFFECT_ALLOW, "targets:"),
		prefix("secret-read", pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, pb.SecurityEffect_SECURITY_EFFECT_DENY, "targets:secret:"),
		prefix("secret-write", pb.SecurityAction_SECURITY_ACTION_VERTEX_WRITE, pb.SecurityEffect_SECURITY_EFFECT_DENY, "targets:secret:"),
	}
	writerRules := []*pb.SecurityRule{
		prefix("read", pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, pb.SecurityEffect_SECURITY_EFFECT_ALLOW, ""),
		prefix("write", pb.SecurityAction_SECURITY_ACTION_VERTEX_WRITE, pb.SecurityEffect_SECURITY_EFFECT_ALLOW, ""),
	}
	var changes []*pb.SecurityChange
	for _, role := range []*pb.SecurityRole{{Id: "head_reader", Rules: readerRules}, {Id: "head_identity", Rules: identityRules}, {Id: "head_writer", Rules: writerRules}, {Id: "head_creator", Rules: creatorRules}} {
		identity := &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC, Issuer: f.provider.URL, Subject: role.Id}
		changes = append(changes,
			&pb.SecurityChange{Operation: &pb.SecurityChange_PutRole{PutRole: role}},
			&pb.SecurityChange{Operation: &pb.SecurityChange_PutUser{PutUser: &pb.SecurityUserStateChange{Identity: identity, State: pb.SecurityPrincipalState_SECURITY_PRINCIPAL_STATE_ACTIVE}}},
			&pb.SecurityChange{Operation: &pb.SecurityChange_PutAssignment{PutAssignment: &pb.SecurityRoleAssignment{Identity: identity, RoleId: role.Id}}})
	}
	if _, err = f.client.ApplySecurityChanges(t.Context(), securityWireRequest(admin, &pb.ApplySecurityChangesRequest{ExpectedRevision: current.Msg.Version.Revision, ChangeId: bytes.Repeat([]byte{111}, 16), Changes: changes})); err != nil {
		t.Fatal(err)
	}
	reader, writer, identity := f.token(t, "head_reader", nil), f.token(t, "head_writer", nil), f.token(t, "head_identity", nil)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	stream, err := f.changes.WatchChanges(ctx, securityWireRequest(identity, &pb.WatchChangesRequest{Bootstrap: true, Projection: pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	if !stream.Receive() || !stream.Msg().Bootstrap || len(stream.Msg().Cursor) == 0 {
		t.Fatal("explicit CDC bootstrap denied", stream.Err())
	}
	if _, err := f.data.GetVertex(ctx, securityWireRequest(identity, &pb.GetVertexRequest{Key: "outside:hidden"})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("CDC consumer inherited an outside-prefix read", err)
	}
	edges := []*pb.Edge{{Tail: "users:1", Head: "targets:1", Weight: 3}, {Tail: "users:1", Head: "targets:2", Weight: 2}, {Tail: "targets:1", Head: "users:1", Weight: 99}, {Tail: "users:1", Head: "users:2", Weight: 99}, {Tail: "users:1", Head: "targets:private:1", Weight: 99}, {Tail: "users:1", Head: "targets:secret:1", Weight: 99}, {Tail: "targets:1", Head: "outside:unreachable", Weight: 99}}
	var endpoints []*pb.Vertex
	endpointKeys := make(map[string]bool)
	for _, edge := range edges {
		for _, key := range []string{edge.Tail, edge.Head} {
			if !endpointKeys[key] {
				endpointKeys[key] = true
				endpoints = append(endpoints, &pb.Vertex{Key: key, Value: &pb.Vertex_Nil{Nil: true}})
			}
		}
	}
	if _, err := f.data.PutVertices(ctx, securityWireRequest(writer, &pb.PutVerticesRequest{Vertices: endpoints})); err != nil {
		t.Fatal(err)
	}
	if _, err := f.data.PutEdges(ctx, securityWireRequest(writer, &pb.PutEdgesRequest{Edges: edges})); err != nil {
		t.Fatal(err)
	}
	for stream.Receive() {
		frame := stream.Msg()
		if len(frame.Invalidations) == 0 {
			continue
		}
		if frame.Invalidations[0].GetEdgeKey() == nil {
			for _, item := range frame.Invalidations {
				key := item.GetVertexKey()
				if key != "users:1" && key != "users:2" && key != "targets:1" && key != "targets:2" || item.CurrentImage != nil {
					t.Fatal("explicit Vertex seed disclosed a denied identity or value", item)
				}
			}
			continue
		}
		if len(frame.Invalidations) != 4 || len(frame.Cursor) == 0 {
			t.Fatal("scoped CDC disclosed hidden batch items", frame)
		}
		for _, item := range frame.Invalidations {
			key := item.GetEdgeKey()
			if key == nil || key.Tail != "users:1" && key.Tail != "targets:1" || key.Head != "targets:1" && key.Head != "targets:2" && key.Head != "users:1" && key.Head != "users:2" || item.CurrentImage != nil {
				t.Fatal("CDC bypassed both endpoint visibility or disclosed a value", item)
			}
		}
		break
	}
	if stream.Err() != nil {
		t.Fatal(stream.Err())
	}
	var cursor []byte
	var got [][2]string
	for {
		page, err := f.data.ScanEdges(ctx, securityWireRequest(reader, &pb.ScanEdgesRequest{Limit: 1, Cursor: cursor}))
		if err != nil {
			t.Fatal(err)
		}
		for _, edge := range page.Msg.Edges {
			got = append(got, [2]string{edge.Tail, edge.Head})
		}
		cursor = append([]byte(nil), page.Msg.NextCursor...)
		if len(cursor) == 0 {
			break
		}
	}
	if !reflect.DeepEqual(got, [][2]string{{"targets:1", "users:1"}, {"users:1", "targets:1"}, {"users:1", "targets:2"}, {"users:1", "users:2"}}) {
		t.Fatal("Head scan/page did not preserve readable reverse Edges or hide denied endpoints", got)
	}
	for _, weighting := range []pb.Weighting{pb.Weighting_WEIGHTING_RAW, pb.Weighting_WEIGHTING_TFIDF, pb.Weighting_WEIGHTING_BM25} {
		for _, params := range []*pb.IlluminateRequest{
			{Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 3, FanOut: 10}}},
			{Params: &pb.IlluminateRequest_Ppr{Ppr: &pb.PprParams{TopN: 10, RestartProb: .2, Epsilon: 1e-4}}},
			{Params: &pb.IlluminateRequest_Community{Community: &pb.LocalCommunityParams{MaxSize: 10, RestartProb: .2, Epsilon: 1e-4}}},
		} {
			params.Seed, params.Weighting = "users:1", weighting
			result, err := f.data.Illuminate(ctx, securityWireRequest(reader, params))
			if err != nil {
				t.Fatal(err)
			}
			for _, vertex := range result.Msg.Graph.Vertices {
				if vertex.Key != "users:1" && vertex.Key != "users:2" && vertex.Key != "targets:1" && vertex.Key != "targets:2" {
					t.Fatal("Head traversal crossed a hidden endpoint", vertex.Key)
				}
			}
			for _, edge := range result.Msg.Graph.Edges {
				if edge.Tail != "users:1" && edge.Tail != "targets:1" || edge.Head != "targets:1" && edge.Head != "targets:2" && edge.Head != "users:1" && edge.Head != "users:2" {
					t.Fatal("Head traversal disclosed an unauthorized Edge", edge)
				}
			}
		}
	}
	degree, err := f.data.TopVerticesByDegree(ctx, securityWireRequest(reader, &pb.TopVerticesByDegreeRequest{Prefix: "users:1", K: 1, Weighted: true}))
	if err != nil || len(degree.Msg.Entries) != 1 || degree.Msg.Entries[0].Degree != 3 || degree.Msg.Entries[0].WeightedDegree != 104 {
		t.Fatal("Head degree included hidden edges", degree, err)
	}
	readerIdentity := &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC, Issuer: f.provider.URL, Subject: "head_reader"}
	for _, test := range []struct {
		tail, head string
		allowed    bool
	}{{"users:1", "targets:1", true}, {"targets:1", "users:1", false}, {"users:1", "targets:private:1", false}} {
		result, err := f.client.ExplainAccess(ctx, securityWireRequest(admin, &pb.ExplainAccessRequest{Identity: readerIdentity, Action: pb.SecurityAction_SECURITY_ACTION_EDGE_CREATE, Edge: &pb.SecurityEdgeIdentity{Tail: test.tail, Head: test.head}}))
		if err != nil || result.Msg.Allowed != test.allowed {
			t.Fatal("Head explanation differs from Server predicate", test, result, err)
		}
	}
	logical := "users:1"
	if _, err := f.client.ExplainAccess(ctx, securityWireRequest(admin, &pb.ExplainAccessRequest{Identity: readerIdentity, Action: pb.SecurityAction_SECURITY_ACTION_EDGE_READ, LogicalKey: &logical, Edge: &pb.SecurityEdgeIdentity{Tail: "users:1", Head: "targets:1"}})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("ambiguous explanation selector accepted", err)
	}
	if _, err := f.data.DeleteEdges(ctx, securityWireRequest(reader, &pb.DeleteEdgesRequest{Edges: []*pb.EdgeKey{{Tail: "users:1", Head: "targets:1"}, {Tail: "targets:1", Head: "users:1"}}})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("unauthorized batch member did not reject all effects", err)
	}
	if _, err := f.data.GetEdge(ctx, securityWireRequest(reader, &pb.GetEdgeRequest{Tail: "users:1", Head: "targets:1"})); err != nil {
		t.Fatal("rejected batch deleted an allowed member", err)
	}
	capability, err := f.data.GetReceiptCapability(ctx, securityWireRequest(reader, &pb.GetReceiptCapabilityRequest{}))
	if err != nil || !capability.Msg.Enabled {
		t.Fatal("Head receipt capability denied", err)
	}
	var epoch mutationreceipt.Epoch
	copy(epoch[:], capability.Msg.Policy.DeploymentEpoch)
	id, err := mutationreceipt.NewID(epoch, time.UnixMilli(int64(capability.Msg.ServerNowUnixMs)), [24]byte{112})
	if err != nil {
		t.Fatal(err)
	}
	receiptContext := &pb.MutationReceiptContext{Endpoint: capability.Msg.Endpoint, LogicalCallId: bytes.Repeat([]byte{112}, 16), OperationIds: [][]byte{id.Bytes()}}
	deleted, err := f.data.DeleteEdge(ctx, securityWireRequest(reader, &pb.DeleteEdgeRequest{Tail: "users:1", Head: "targets:1", ReceiptContext: receiptContext}))
	if err != nil || !deleted.Msg.Existed {
		t.Fatal("Head-authorized receipt Delete failed", deleted, err)
	}
	if _, err := f.data.GetReceiptStatus(ctx, securityWireRequest(reader, &pb.GetReceiptStatusRequest{OperationId: id.Bytes()})); err != nil {
		t.Fatal("Head original receipt result denied", err)
	}
	missing, err := mutationreceipt.NewID(epoch, time.UnixMilli(int64(capability.Msg.ServerNowUnixMs)), [24]byte{113})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.data.GetReceiptStatus(ctx, securityWireRequest(reader, &pb.GetReceiptStatusRequest{OperationId: missing.Bytes()})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("scoped grant disclosed receipt absence", err)
	}
	if added, err := f.data.AddEdge(ctx, securityWireRequest(reader, &pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "users:1", Head: "targets:1", Weight: 1}})); err != nil || added.Msg.GetEffectiveWeight() != 1 {
		t.Fatal("Head Write did not permit Add through the same base predicate", added, err)
	}

	creator := f.token(t, "head_creator", nil)
	for _, key := range []string{"targets:new", "targets:later"} {
		if _, err := f.data.PutVertex(ctx, securityWireRequest(writer, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: key, Expiration: timestamppb.New(time.Now().Add(time.Hour))}})); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.data.GetEdge(ctx, securityWireRequest(creator, &pb.GetEdgeRequest{Tail: "users:1", Head: "targets:2"})); err != nil {
		t.Fatal("both endpoint reads did not derive Edge Read", err)
	}
	createCap, err := f.data.GetReceiptCapability(ctx, securityWireRequest(creator, &pb.GetReceiptCapabilityRequest{}))
	if err != nil || !slices.Contains(createCap.Msg.SupportedMutations, pb.ReceiptMutationKind_RECEIPT_MUTATION_KIND_CREATE_EDGE) {
		t.Fatal("standalone creator capability missing", err)
	}
	createItems := []*pb.Edge{{Tail: "users:1", Head: "targets:new", Weight: 2}, {Tail: "users:1", Head: "targets:new", Weight: 9}, {Tail: "users:1", Head: "targets:missing", Weight: 1}, {Tail: "users:1", Head: "targets:new", Weight: 1, Expiration: timestamppb.New(time.Now().Add(-time.Minute))}}
	createIDs := make([][]byte, len(createItems))
	for i := range createIDs {
		operation, err := mutationreceipt.NewID(epoch, time.UnixMilli(int64(createCap.Msg.ServerNowUnixMs)), [24]byte{121, byte(i + 1)})
		if err != nil {
			t.Fatal(err)
		}
		createIDs[i] = operation.Bytes()
	}
	createReq := &pb.CreateEdgesRequest{Edges: createItems, ReceiptContext: &pb.MutationReceiptContext{Endpoint: createCap.Msg.Endpoint, LogicalCallId: bytes.Repeat([]byte{121}, 16), OperationIds: createIDs}}
	created, err := f.data.CreateEdges(ctx, securityWireRequest(creator, createReq))
	wantCreate := []pb.CreateEdgeOutcome{pb.CreateEdgeOutcome_CREATE_EDGE_OUTCOME_CREATED_AND_LIVE, pb.CreateEdgeOutcome_CREATE_EDGE_OUTCOME_EDGE_EXISTS, pb.CreateEdgeOutcome_CREATE_EDGE_OUTCOME_ENDPOINT_NOT_LIVE, pb.CreateEdgeOutcome_CREATE_EDGE_OUTCOME_EXPIRED}
	if err != nil || !slices.Equal(created.Msg.GetOutcomes(), wantCreate) {
		t.Fatal("least privilege Create failed", created, err)
	}
	if _, err := f.data.GetReceiptStatus(ctx, securityWireRequest(creator, &pb.GetReceiptStatusRequest{OperationId: createIDs[0]})); err != nil {
		t.Fatal("creator original result requires excess privileges", err)
	}
	if _, err := f.data.DeleteEdge(ctx, securityWireRequest(reader, &pb.DeleteEdgeRequest{Tail: "users:1", Head: "targets:new"})); err != nil {
		t.Fatal(err)
	}
	again, err := f.data.CreateEdges(ctx, securityWireRequest(creator, proto.Clone(createReq).(*pb.CreateEdgesRequest)))
	if err != nil || !slices.Equal(again.Msg.GetOutcomes(), wantCreate) {
		t.Fatal("Create retry lost original outcomes", again, err)
	}
	if _, err := f.data.GetEdge(ctx, securityWireRequest(writer, &pb.GetEdgeRequest{Tail: "users:1", Head: "targets:new"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatal("receipt retry resurrected deleted Edge", err)
	}
	sdk, err := client.NewLantern(f.server.URL, client.WithHTTPClient(&http.Client{Transport: authIngressRoundTripper{h2cClient().Transport}}), client.WithAuthToken(creator))
	if err != nil {
		t.Fatal(err)
	}
	sdkCap, err := sdk.GetReceiptCapability(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sdkContext, err := sdk.NewReceiptContext(sdkCap, client.ReceiptMutationCreateEdge, 1)
	if err != nil {
		t.Fatal(err)
	}
	sdkInput := client.EdgeInput{Tail: "users:1", Head: "targets:later", Weight: 2}
	outcome, err := sdk.CreateEdgeWithReceipt(ctx, sdkInput, sdkContext)
	if err != nil || outcome != client.CreateEdgeCreatedAndLive {
		t.Fatal("Go SDK least-privilege Create", outcome, err)
	}
	status, err := sdk.GetReceiptStatus(ctx, sdkContext.OperationIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if original, ok := status.Receipt.OriginalResult.(client.ReceiptCreateEdgeResult); !ok || original.Outcome != outcome {
		t.Fatal("Go SDK original Create result", status)
	}
	if _, err := f.data.DeleteEdge(ctx, securityWireRequest(reader, &pb.DeleteEdgeRequest{Tail: sdkInput.Tail, Head: sdkInput.Head})); err != nil {
		t.Fatal(err)
	}
	if outcome, err := sdk.CreateEdgeWithReceipt(ctx, sdkInput, sdkContext); err != nil || outcome != client.CreateEdgeCreatedAndLive {
		t.Fatal("Go SDK immutable replay", outcome, err)
	}
	if _, err := f.data.GetEdge(ctx, securityWireRequest(writer, &pb.GetEdgeRequest{Tail: sdkInput.Tail, Head: sdkInput.Head})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatal("Go SDK replay resurrected Edge", err)
	}
	for _, denied := range []*pb.CreateEdgesRequest{
		{Edges: []*pb.Edge{{Tail: "users:1", Head: "targets:later", Weight: 1}, {Tail: "targets:later", Head: "users:1", Weight: 1}}},
		{Edges: []*pb.Edge{{Tail: "users:1", Head: "targets:secret:1", Weight: 1}}},
		{Edges: []*pb.Edge{{Tail: "users:2", Head: "targets:later", Weight: 1}}},
	} {
		if _, err := f.data.CreateEdges(ctx, securityWireRequest(creator, denied)); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatal("invalid Create scope admitted", err)
		}
	}
	if _, err := f.data.GetEdge(ctx, securityWireRequest(writer, &pb.GetEdgeRequest{Tail: "users:1", Head: "targets:later"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatal("unauthorized Create batch partially applied", err)
	}
}

func TestAuth_HeadManagedBlindCreateDeleteReceiptAndGoFacadeRealConnect(t *testing.T) {
	f := newOIDCControlWireFixtureOptions(t, nil, true)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	admin := f.token(t, "admin", nil)
	current, err := f.client.GetCurrentPrincipal(ctx, securityWireRequest(admin, &pb.GetCurrentPrincipalRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	prefix := func(id string, action pb.SecurityAction, value string) *pb.SecurityRule {
		return &pb.SecurityRule{Id: id, Action: action, Effect: pb.SecurityEffect_SECURITY_EFFECT_ALLOW, Resource: &pb.SecurityRule_Prefix{Prefix: value}}
	}
	blindRules := []*pb.SecurityRule{
		prefix("tail-read", pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, "tails:"),
		prefix("head-write", pb.SecurityAction_SECURITY_ACTION_VERTEX_WRITE, "heads:"),
		prefix("receipts", pb.SecurityAction_SECURITY_ACTION_RECEIPT_READ, ""),
	}
	roles := []*pb.SecurityRole{
		{Id: "blind", Rules: blindRules},
		{Id: "seed", Rules: []*pb.SecurityRule{prefix("read", pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, ""), prefix("write", pb.SecurityAction_SECURITY_ACTION_VERTEX_WRITE, "")}},
	}
	var changes []*pb.SecurityChange
	for _, role := range roles {
		identity := &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC, Issuer: f.provider.URL, Subject: role.Id}
		changes = append(changes,
			&pb.SecurityChange{Operation: &pb.SecurityChange_PutRole{PutRole: role}},
			&pb.SecurityChange{Operation: &pb.SecurityChange_PutUser{PutUser: &pb.SecurityUserStateChange{Identity: identity, State: pb.SecurityPrincipalState_SECURITY_PRINCIPAL_STATE_ACTIVE}}},
			&pb.SecurityChange{Operation: &pb.SecurityChange_PutAssignment{PutAssignment: &pb.SecurityRoleAssignment{Identity: identity, RoleId: role.Id}}},
		)
	}
	committed, err := f.client.ApplySecurityChanges(ctx, securityWireRequest(admin, &pb.ApplySecurityChangesRequest{ExpectedRevision: current.Msg.Version.Revision, ChangeId: bytes.Repeat([]byte{117}, 16), Changes: changes}))
	if err != nil {
		t.Fatal(err)
	}
	seed := f.token(t, "seed", nil)
	vertexTTL := timestamppb.New(time.Now().Add(time.Hour))
	vertices := []*pb.Vertex{{Key: "tails:a", Value: &pb.Vertex_String_{String_: "tail value"}, Expiration: vertexTTL}, {Key: "heads:b", Value: &pb.Vertex_String_{String_: "private head value"}, Expiration: vertexTTL}}
	if _, err := f.data.PutVertices(ctx, securityWireRequest(seed, &pb.PutVerticesRequest{Vertices: vertices})); err != nil {
		t.Fatal(err)
	}
	blind := f.token(t, "blind", nil)
	sdk, err := client.NewLantern(f.server.URL, client.WithHTTPClient(&http.Client{Transport: authIngressRoundTripper{h2cClient().Transport}}), client.WithAuthToken(blind))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sdk.Close() }()
	capability, err := sdk.GetReceiptCapability(ctx)
	if err != nil || !capability.Supports(client.ReceiptMutationCreateEdge) {
		t.Fatal("blind Create capability unavailable", err)
	}
	receiptContext, err := sdk.NewReceiptContext(capability, client.ReceiptMutationCreateEdge, 1)
	if err != nil {
		t.Fatal(err)
	}
	input := client.EdgeInput{Tail: "tails:a", Head: "heads:b", Weight: 2}
	reply, err := client.MutationReplyFrom(sdk.CreateEdgeWithReceipt(ctx, input, receiptContext))
	if err != nil || !reply.AcceptedUndisclosed() {
		t.Fatal("blind Create was not handled with typed acceptance", err)
	}
	if _, known := reply.Effect(); known {
		t.Fatal("blind Create disclosed an outcome")
	}
	status, err := sdk.GetReceiptStatus(ctx, receiptContext.OperationIDs[0])
	if err != nil || status.State != client.ReceiptEffectUndisclosed || status.Receipt != nil {
		t.Fatal("blind status disclosed original receipt", status.State, err)
	}
	input.Weight = 7
	reply, err = client.MutationReplyFrom(sdk.CreateEdgeWithReceipt(ctx, input, receiptContext))
	if err != nil || !reply.AcceptedUndisclosed() {
		t.Fatal("private idempotency conflict disclosed", err)
	}
	stored, found := f.graph.GetWeight("data:tails:a", "data:heads:b")
	if !found || stored != 2 {
		t.Fatal("conflicting replay reexecuted Create")
	}
	if _, err := sdk.GetEdge(ctx, "tails:a", "heads:b"); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Head write granted Edge read", err)
	}
	if _, err := sdk.PutVertex(ctx, "heads:b", "overwritten", 0); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Head write granted endpoint value/TTL updates", err)
	}
	if _, err := sdk.DeleteVertex(ctx, "tails:a"); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Head write granted VertexDelete", err)
	}
	deleteReply, err := client.MutationReplyFrom(sdk.DeleteEdge(ctx, "tails:a", "heads:b"))
	if err != nil || !deleteReply.AcceptedUndisclosed() {
		t.Fatal("blind Delete disclosed existence", err)
	}
	input.Weight = 2
	reply, err = client.MutationReplyFrom(sdk.CreateEdgeWithReceipt(ctx, input, receiptContext))
	if err != nil || !reply.AcceptedUndisclosed() {
		t.Fatal("original replay after Delete failed", err)
	}
	if _, found := f.graph.GetWeight("data:tails:a", "data:heads:b"); found {
		t.Fatal("receipt replay resurrected deleted Edge")
	}
	for _, vertex := range vertices {
		stored, found := f.graph.GetVertex("data:" + vertex.Key)
		if !found || stored.GetString_() != vertex.GetString_() || !stored.Expiration.AsTime().Equal(vertex.Expiration.AsTime()) {
			t.Fatal("Edge mutation changed endpoint value/TTL", vertex.Key)
		}
	}

	// Liveness is a private effect detail for a head writer without HeadRead.
	// A missing endpoint rejects the whole batch internally and still returns
	// only typed acceptance, never auto-creating the endpoint or first Edge.
	hiddenBatch := []*pb.Edge{{Tail: "tails:a", Head: "heads:b", Weight: 7}, {Tail: "tails:a", Head: "heads:missing", Weight: 3}}
	addReply, err := f.data.AddEdges(ctx, securityWireRequest(blind, &pb.AddEdgesRequest{Edges: hiddenBatch}))
	if err != nil || !proto.Equal(addReply.Msg, &pb.AddEdgesResponse{Acceptance: &pb.MutationAcceptance{Kind: pb.MutationAcceptanceKind_MUTATION_ACCEPTANCE_KIND_HANDLED_EFFECT_UNDISCLOSED}}) {
		t.Fatal("blind Add disclosed endpoint liveness", err)
	}
	putReply, err := f.data.PutEdges(ctx, securityWireRequest(blind, &pb.PutEdgesRequest{Edges: hiddenBatch}))
	if err != nil || !proto.Equal(putReply.Msg, &pb.PutEdgesResponse{Acceptance: &pb.MutationAcceptance{Kind: pb.MutationAcceptanceKind_MUTATION_ACCEPTANCE_KIND_HANDLED_EFFECT_UNDISCLOSED}}) {
		t.Fatal("blind Put disclosed endpoint liveness", err)
	}
	if _, ok := f.graph.GetVertex("data:heads:missing"); ok {
		t.Fatal("protected Edge created missing endpoint")
	}
	if _, ok := f.graph.GetWeight("data:tails:a", "data:heads:b"); ok {
		t.Fatal("protected mixed batch partially applied")
	}
	// The CLI consumes the same dedicated SDK acknowledgement, emits no
	// fabricated effect and preserves real authorization errors over Connect.
	var cliOutput bytes.Buffer
	cli := cliservice.NewCLIService(sdk, cliservice.WithOutput(&cliOutput))
	for _, args := range [][]string{
		{"add", "edge", "tails:a", "heads:b", "2"},
		{"put", "edge", "tails:a", "heads:b", "3"},
		{"add", "decaying-edge", "tails:a", "heads:b", "16", "0.5", "5", "1"},
		{"delete", "edge", "tails:a", "heads:b"},
		{"delete", "edge", "tails:a", "heads:b", "tails:a", "heads:missing"},
		{"delete", "contribution", "tails:a", "heads:b", strings.Repeat("ab", client.ContribIDSize)},
	} {
		cliOutput.Reset()
		if err := cli.RunArgs(ctx, args); err != nil || cliOutput.String() != "{\"acceptance\":\"acceptedUndisclosed\"}\n" {
			t.Fatal("CLI blind acknowledgement", args, err, cliOutput.String())
		}
	}
	cliOutput.Reset()
	if err := cli.RunArgs(ctx, []string{"put", "edge", "outside:a", "heads:b", "2"}); err == nil || cliOutput.Len() != 0 {
		t.Fatal("CLI concealed authorization failure", err, cliOutput.String())
	}
	// Current disclosure rights expose the original result only after they are
	// granted; the private receipt bytes remain intact across blind handling.
	blindRules = append(blindRules, prefix("head-read", pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, "heads:"))
	_, err = f.client.ApplySecurityChanges(ctx, securityWireRequest(admin, &pb.ApplySecurityChangesRequest{ExpectedRevision: committed.Msg.Version.Revision, ChangeId: bytes.Repeat([]byte{118}, 16), Changes: []*pb.SecurityChange{{Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Id: "blind", Rules: blindRules}}}}}))
	if err != nil {
		t.Fatal(err)
	}
	status, err = sdk.GetReceiptStatus(ctx, receiptContext.OperationIDs[0])
	if err != nil || status.State != client.ReceiptConfirmed || status.Receipt == nil {
		t.Fatal("original receipt lost after disclosure grant", status.State, err)
	}
	result, ok := status.Receipt.OriginalResult.(client.ReceiptCreateEdgeResult)
	if !ok || result.Outcome != client.CreateEdgeCreatedAndLive {
		t.Fatal("original Create outcome was replaced")
	}
}

func TestAuth_OIDCBrowserLoginAndStepUpPurposesRealConnect(t *testing.T) {
	for _, evidence := range []string{"missing", "stale"} {
		t.Run(evidence, func(t *testing.T) {
			f := newOIDCControlWireFixture(t)
			callback := func(start *http.Response, evidence string) string {
				path := f.browserCallbackPath(t, start, "admin", false)
				parsed, _ := url.Parse(path)
				f.mu.Lock()
				code := f.codes[parsed.Query().Get("code")]
				code.authTime = evidence
				f.codes[parsed.Query().Get("code")] = code
				f.mu.Unlock()
				return path
			}
			sessionCookies := func(response *http.Response) []*http.Cookie {
				var cookies []*http.Cookie
				for _, cookie := range response.Cookies() {
					if cookie.Name == "__Host-lantern-session" || cookie.Name == "__Host-lantern-csrf" {
						cookies = append(cookies, cookie)
					}
				}
				return cookies
			}
			clientFor := func(cookies []*http.Cookie, csrf string) graphv1connect.LanternSecurityServiceClient {
				return graphv1connect.NewLanternSecurityServiceClient(&http.Client{Transport: authWireRoundTripper{next: http.DefaultTransport, cookies: cookies, csrf: csrf}}, f.server.URL+"/browser")
			}
			start := f.browserLoginStart(t, nil, false)
			response := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, callback(start, evidence), start.Cookies(), ""))
			if response.StatusCode != http.StatusSeeOther {
				t.Fatal("ordinary SSO denied", response.StatusCode)
			}
			cookies := sessionCookies(response)
			if len(cookies) != 2 {
				t.Fatal("ordinary login did not issue opaque session")
			}
			csrf := browserCookieValue(cookies, "__Host-lantern-csrf")
			client := clientFor(cookies, csrf)
			principal, err := client.GetCurrentPrincipal(t.Context(), connect.NewRequest(&pb.GetCurrentPrincipalRequest{}))
			if err != nil || principal.Msg.RecentAuthentication {
				t.Fatal("ordinary session inferred recent authentication", err)
			}
			roles, err := client.ListRoles(t.Context(), connect.NewRequest(&pb.ListRolesRequest{}))
			if err != nil {
				t.Fatal("ordinary session could not read permitted management state", err)
			}
			change := &pb.ApplySecurityChangesRequest{ExpectedRevision: roles.Msg.Version.Revision, ChangeId: bytes.Repeat([]byte{42}, 16), Changes: []*pb.SecurityChange{{Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Id: "recent_proof", Rules: []*pb.SecurityRule{{Id: "read", Effect: pb.SecurityEffect_SECURITY_EFFECT_ALLOW, Action: pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, Resource: &pb.SecurityRule_Prefix{Prefix: "users:"}}}}}}}}
			if _, err := client.ApplySecurityChanges(t.Context(), connect.NewRequest(change)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatal("unproven management change admitted", err)
			}
			if _, err := clientFor(cookies, "").ApplySecurityChanges(t.Context(), connect.NewRequest(change)); connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatal("ordinary session bypassed CSRF", err)
			}

			// The callback cannot downgrade the saved step-up purpose. Refusal leaves
			// the existing ordinary cookie usable, with no new recent-auth evidence.
			stepUp := f.browserLoginStart(t, cookies, true)
			stepPath := callback(stepUp, evidence)
			if rejected := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, stepPath+"&step_up=false", stepUp.Cookies(), "")); rejected.StatusCode != http.StatusBadRequest {
				t.Fatal("callback purpose override accepted", rejected.StatusCode)
			}
			failed := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, stepPath, stepUp.Cookies(), ""))
			if failed.StatusCode != http.StatusUnauthorized || len(sessionCookies(failed)) != 0 {
				t.Fatal("step-up accepted missing/old proof", failed.StatusCode)
			}
			unchanged, err := client.GetCurrentPrincipal(t.Context(), connect.NewRequest(&pb.GetCurrentPrincipalRequest{}))
			if err != nil || unchanged.Msg.RecentAuthentication || unchanged.Msg.Version.Revision != principal.Msg.Version.Revision {
				t.Fatal("failed step-up changed ordinary session", err)
			}

			// A normal relogin also replaces a cookie; replacement is not step-up.
			relogin := f.browserLoginStart(t, cookies, false)
			rotated := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, callback(relogin, evidence), relogin.Cookies(), ""))
			if rotated.StatusCode != http.StatusSeeOther {
				t.Fatal("ordinary replacement required recent authentication", rotated.StatusCode)
			}
			newCookies := sessionCookies(rotated)
			if browserCookieValue(newCookies, "__Host-lantern-session") == browserCookieValue(cookies, "__Host-lantern-session") {
				t.Fatal("ordinary replacement reused cookie")
			}
			if old := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, "/auth/session", cookies, "")); old.StatusCode != http.StatusUnauthorized {
				t.Fatal("ordinary replacement left old cookie live")
			}
			client = clientFor(newCookies, browserCookieValue(newCookies, "__Host-lantern-csrf"))
			ordinary, err := client.GetCurrentPrincipal(t.Context(), connect.NewRequest(&pb.GetCurrentPrincipalRequest{}))
			if err != nil || ordinary.Msg.RecentAuthentication {
				t.Fatal("rotation upgraded unknown/old evidence", err)
			}

			fresh := f.browserLoginStart(t, newCookies, true)
			freshResponse := wireBrowserDo(t, f.browserRequest(t, http.MethodGet, callback(fresh, ""), fresh.Cookies(), ""))
			if freshResponse.StatusCode != http.StatusSeeOther {
				t.Fatal("fresh step-up refused", freshResponse.StatusCode)
			}
			freshCookies := sessionCookies(freshResponse)
			client = clientFor(freshCookies, browserCookieValue(freshCookies, "__Host-lantern-csrf"))
			verified, err := client.GetCurrentPrincipal(t.Context(), connect.NewRequest(&pb.GetCurrentPrincipalRequest{}))
			if err != nil || !verified.Msg.RecentAuthentication {
				t.Fatal("actual fresh evidence did not qualify", err)
			}
			change.ExpectedRevision = verified.Msg.Version.Revision
			if _, err := client.ApplySecurityChanges(t.Context(), connect.NewRequest(change)); err != nil {
				t.Fatal("fresh authorized management change refused", err)
			}
		})
	}
}
