package integration_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestBackupSnapshot_E2E drives the BackupSnapshot streaming RPC (#691)
// full-stack: seed a graph via the SDK, then dump it through the raw
// Connect client and assert every live vertex + folded edge is streamed.
// BackupSnapshot has no replication gate, so the server is single-node
// (rep = nil).
func TestBackupSnapshot_E2E(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	val := provider.NewValidationInterceptor(defaultIntegrationValidationLimits())
	svc := service.NewLanternService(cache)
	srv := newConnectTestServer(t, svc, nil, val.ConnectInterceptor())
	sdk := newConnectClientFor(t, srv.url)
	raw := graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := sdk.PutVertex(ctx, "alice", "Alice", time.Minute); err != nil {
		t.Fatalf("PutVertex alice: %v", err)
	}
	if _, err := sdk.PutVertex(ctx, "bob", int64(42), time.Minute); err != nil {
		t.Fatalf("PutVertex bob: %v", err)
	}
	if _, err := sdk.PutVertex(ctx, "carol", "Carol", time.Minute); err != nil {
		t.Fatalf("PutVertex carol: %v", err)
	}
	if _, err := sdk.AddEdge(ctx, "alice", "bob", 1.5, time.Minute); err != nil {
		t.Fatalf("AddEdge alice->bob: %v", err)
	}
	if _, err := sdk.AddEdge(ctx, "bob", "carol", 2.0, time.Minute); err != nil {
		t.Fatalf("AddEdge bob->carol: %v", err)
	}

	vertices, edges := drainBackup(t, ctx, raw, &pb.BackupSnapshotRequest{})

	if len(vertices) != 3 {
		t.Fatalf("got %d vertices, want 3: %v", len(vertices), vertices)
	}
	for _, k := range []string{"alice", "bob", "carol"} {
		if _, ok := vertices[k]; !ok {
			t.Errorf("missing vertex %q", k)
		}
	}
	// The BackupSnapshotResponse carries the live *pb.Vertex, so values survive.
	if got, err := client.StringValue(vertices["alice"]); err != nil || got != "Alice" {
		t.Errorf("alice value = %q (err %v), want Alice", got, err)
	}
	if len(edges) != 2 {
		t.Fatalf("got %d edges, want 2: %v", len(edges), edges)
	}
	if e := edges["alice->bob"]; e == nil || e.GetWeight() != 1.5 {
		t.Errorf("edge alice->bob = %v, want weight 1.5", e)
	}
	if e := edges["bob->carol"]; e == nil || e.GetWeight() != 2.0 {
		t.Errorf("edge bob->carol = %v, want weight 2.0", e)
	}

	// vertex_prefix scopes the dump to the induced subgraph.
	t.Run("Prefix", func(t *testing.T) {
		if _, err := sdk.PutVertex(ctx, "x:1", "one", time.Minute); err != nil {
			t.Fatalf("PutVertex x:1: %v", err)
		}
		if _, err := sdk.PutVertex(ctx, "x:2", "two", time.Minute); err != nil {
			t.Fatalf("PutVertex x:2: %v", err)
		}
		if _, err := sdk.AddEdge(ctx, "x:1", "x:2", 1.0, time.Minute); err != nil {
			t.Fatalf("AddEdge x:1->x:2: %v", err)
		}
		vs, es := drainBackup(t, ctx, raw, &pb.BackupSnapshotRequest{VertexPrefix: "x:"})
		for k := range vs {
			if !strings.HasPrefix(k, "x:") {
				t.Errorf("prefix backup leaked vertex %q", k)
			}
		}
		if len(vs) != 2 {
			t.Errorf("prefix backup got %d vertices, want 2: %v", len(vs), vs)
		}
		if len(es) != 1 {
			t.Errorf("prefix backup got %d edges, want 1: %v", len(es), es)
		}
	})
}

func TestBackupSnapshot_ImplicitEndpoints_E2E(t *testing.T) {
	for _, finite := range []bool{false, true} {
		name := "Permanent"
		if finite {
			name = "Finite"
		}
		t.Run(name, func(t *testing.T) {
			cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
			svc := service.NewLanternService(cache).WithDataNamespace()
			val := provider.NewValidationInterceptor(defaultIntegrationValidationLimits())
			srv := newConnectTestServer(t, svc, nil, val.ConnectInterceptor())
			raw := graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)
			sdk := newConnectClientFor(t, srv.url)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := sdk.PutVertex(ctx, "implicit:value", "kept", time.Hour); err != nil {
				t.Fatal(err)
			}
			var expiration *timestamppb.Timestamp
			if finite {
				expiration = timestamppb.New(time.Now().Add(time.Hour))
			}
			if _, err := raw.PutEdges(ctx, connect.NewRequest(&pb.PutEdgesRequest{Edges: []*pb.Edge{
				{Tail: "implicit:tail", Head: "implicit:head", Weight: 1, Expiration: expiration},
				{Tail: "implicit:tail", Head: "outside:head", Weight: 2, Expiration: expiration},
			}})); err != nil {
				t.Fatalf("OFF PutEdges with absent endpoints: %v", err)
			}
			vertices, edges := drainBackup(t, ctx, raw, &pb.BackupSnapshotRequest{VertexPrefix: "implicit:"})
			if len(vertices) != 3 || len(edges) != 1 {
				t.Fatalf("prefix backup returned %d vertices/%d edges, want 3/1", len(vertices), len(edges))
			}
			for _, key := range []string{"implicit:tail", "implicit:head"} {
				vertex := vertices[key]
				if vertex == nil || vertex.GetKey() != key || !vertex.GetNil() {
					t.Fatalf("implicit endpoint %q lost logical identity or nil value: %v", key, vertex)
				}
				if (vertex.GetExpiration() == nil) != (expiration == nil) ||
					(expiration != nil && !vertex.GetExpiration().AsTime().Equal(expiration.AsTime())) {
					t.Fatalf("implicit endpoint %q expiration = %v, want %v", key, vertex.GetExpiration(), expiration)
				}
			}
			if vertices["implicit:value"].GetString_() != "kept" {
				t.Fatal("backup changed the explicit value")
			}
			if edge := edges["implicit:tail->implicit:head"]; edge == nil || edge.GetWeight() != 1 {
				t.Fatalf("backup changed the folded edge: %v", edge)
			}
			if vertices["outside:head"] != nil || edges["implicit:tail->outside:head"] != nil {
				t.Fatal("prefix backup exposed an outside endpoint or crossing edge")
			}
		})
	}
}

// drainBackup runs BackupSnapshot to completion and returns the streamed
// vertices (by key) and folded edges (by "tail->head").
func drainBackup(t *testing.T, ctx context.Context, raw graphv1connect.LanternServiceClient, req *pb.BackupSnapshotRequest) (map[string]*pb.Vertex, map[string]*pb.Edge) {
	t.Helper()
	stream, err := raw.BackupSnapshot(ctx, connect.NewRequest(req))
	if err != nil {
		t.Fatalf("BackupSnapshot: %v", err)
	}
	vertices := map[string]*pb.Vertex{}
	edges := map[string]*pb.Edge{}
	for stream.Receive() {
		switch x := stream.Msg().GetRecord().(type) {
		case *pb.BackupSnapshotResponse_Vertex:
			vertices[x.Vertex.GetKey()] = x.Vertex
		case *pb.BackupSnapshotResponse_Edge:
			edges[x.Edge.GetTail()+"->"+x.Edge.GetHead()] = x.Edge
		}
	}
	if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("BackupSnapshot stream: %v", err)
	}
	return vertices, edges
}

// TestBackupper_ServerInternal_RoundTrip_E2E drives the server-internal
// snapshot engine (#770) end-to-end against real services + caches: seed a
// graph, dump it to a temp dir via the Backupper's BackupNow (same path the
// periodic loop uses), then RestoreOnStartup it into a FRESH service and
// confirm every vertex value + edge weight survived. Proves the
// server-internal proto dump interchanges with the BackupSnapshot surface
// and that restore replays faithfully through PutVertices / PutEdges.
func TestBackupper_ServerInternal_RoundTrip_E2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	dir := t.TempDir()
	cfg := backup.Config{Enabled: true, Dir: dir, Interval: time.Hour, Retain: 3, InstanceID: "itest", RestoreOnStart: true}
	val := provider.NewValidationInterceptor(defaultIntegrationValidationLimits())

	// Source service, seeded via the SDK.
	searchLimits := productionSearchLimits(true, true)
	srcCache := newProductionSearchCache(time.Minute, true, true, searchLimits.AnalysisLimits)
	srcSvc := service.NewLanternService(srcCache).WithSearchLimits(searchLimits)
	srcSrv := newConnectTestServer(t, srcSvc, nil, val.ConnectInterceptor())
	srcSDK := newConnectClientFor(t, srcSrv.url)
	srcRaw := graphv1connect.NewLanternServiceClient(h2cClient(), srcSrv.url)

	const bigInt = int64(9007199254740993) // 2^53 + 1
	if _, err := srcSDK.PutVertex(ctx, "alice", "Alice", time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := srcSDK.PutVertex(ctx, "num", bigInt, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := srcSDK.AddEdge(ctx, "alice", "num", 1.5, time.Minute); err != nil {
		t.Fatal(err)
	}
	expiresBeforeRestore := time.Now().Add(500 * time.Millisecond)
	if _, err := srcSDK.PutVertexAt(ctx, "expired-during-restart", "restore-expired-term", expiresBeforeRestore); err != nil {
		t.Fatal(err)
	}

	// Server-internal dump (same path as the periodic loop).
	if _, err := backup.New(srcSvc, cfg, nil, nil).BackupNow(ctx); err != nil {
		t.Fatalf("BackupNow: %v", err)
	}
	time.Sleep(time.Until(expiresBeforeRestore) + 25*time.Millisecond)

	// Fresh service, restored on startup from the dump.
	dstCache := newProductionSearchCache(time.Minute, true, true, searchLimits.AnalysisLimits)
	dstSvc := service.NewLanternService(dstCache).WithSearchLimits(searchLimits)
	dstSrv := newConnectTestServer(t, dstSvc, nil, val.ConnectInterceptor())
	dstRaw := graphv1connect.NewLanternServiceClient(h2cClient(), dstSrv.url)

	stats, err := backup.New(dstSvc, cfg, nil, nil).RestoreOnStartup(ctx)
	if err != nil {
		t.Fatalf("RestoreOnStartup: %v", err)
	}
	if stats.Vertices != 2 || stats.Edges != 1 {
		t.Fatalf("restore stats = %+v, want {2,1}", stats)
	}
	if vertices, edges := dstCache.CausalBarrierCounts(); vertices != 0 || edges != 0 {
		t.Fatalf("zero-HLC backup restore retained causal barriers = %d/%d, want 0/0", vertices, edges)
	}

	// Re-dump the restored service and confirm fidelity.
	vs, es := drainBackup(t, ctx, dstRaw, &pb.BackupSnapshotRequest{})
	if got, err := client.StringValue(vs["alice"]); err != nil || got != "Alice" {
		t.Errorf("restored alice = %q (err %v), want Alice", got, err)
	}
	if got, err := client.IntValue(vs["num"]); err != nil || int64(got) != bigInt {
		t.Errorf("restored num = %d (err %v), want %d", got, err, bigInt)
	}
	if e := es["alice->num"]; e == nil || e.GetWeight() != 1.5 {
		t.Errorf("restored edge alice->num = %v, want weight 1.5", e)
	}
	if _, ok := vs["expired-during-restart"]; ok {
		t.Errorf("born-expired restore resurrected expired vertex: %v", vs)
	}
	waitForSearchConvergence(t, ctx, "Alice", nil, srcRaw, dstRaw)
	if got, err := wireSearchSignature(ctx, dstRaw, "restore-expired-term", nil); err != nil || len(got) != 0 {
		t.Fatalf("expired restore search = %+v err=%v", got, err)
	}
	status, err := dstRaw.GetServerStatus(ctx, connect.NewRequest(&pb.GetServerStatusRequest{}))
	if err != nil {
		t.Fatalf("GetServerStatus after restore: %v", err)
	}
	if got := status.Msg.GetSearch().GetIndexStats(); got.GetHealth() != pb.SearchIndexHealth_SEARCH_INDEX_HEALTH_HEALTHY || got.GetDocuments() != 2 {
		t.Fatalf("restored search stats = %+v, want healthy with two documents", got)
	}
}

// TestBackupRestore_E2E_SDK round-trips a graph through the SDK Backup
// (dump) and Restore (load) helpers across both wire formats, into a fresh
// server, and verifies values + an int64 above 2^53 survive (json.Number).
func TestBackupRestore_E2E_SDK(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format client.Format
	}{
		{"proto", client.FormatProto},
		{"ndjson", client.FormatNDJSON},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src, _ := newInProcessClient(t)
			dst, _ := newInProcessClient(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			const bigInt = int64(9007199254740993) // 2^53 + 1
			if _, err := src.PutVertex(ctx, "alice", "Alice", time.Minute); err != nil {
				t.Fatal(err)
			}
			if _, err := src.PutVertex(ctx, "num", bigInt, time.Minute); err != nil {
				t.Fatal(err)
			}
			if _, err := src.PutVertex(ctx, "carol", "Carol", time.Minute); err != nil {
				t.Fatal(err)
			}
			if _, err := src.AddEdge(ctx, "alice", "num", 1.5, time.Minute); err != nil {
				t.Fatal(err)
			}
			if _, err := src.AddEdge(ctx, "num", "carol", 2.0, time.Minute); err != nil {
				t.Fatal(err)
			}

			var buf bytes.Buffer
			bstats, err := src.Backup(ctx, &buf, client.WithBackupFormat(tc.format))
			if err != nil {
				t.Fatalf("Backup: %v", err)
			}
			if bstats.Vertices != 3 || bstats.Edges != 2 {
				t.Fatalf("backup stats = %+v, want {3,2}", bstats)
			}

			rstats, err := dst.Restore(ctx, &buf, client.WithRestoreFormat(tc.format))
			if err != nil {
				t.Fatalf("Restore: %v", err)
			}
			if rstats.Vertices != 3 || rstats.Edges != 2 {
				t.Fatalf("restore stats = %+v, want {3,2}", rstats)
			}

			alice, err := dst.GetVertex(ctx, "alice")
			if err != nil {
				t.Fatalf("GetVertex alice: %v", err)
			}
			if got, err := client.StringValue(alice); err != nil || got != "Alice" {
				t.Errorf("alice = %q (err %v), want Alice", got, err)
			}
			num, err := dst.GetVertex(ctx, "num")
			if err != nil {
				t.Fatalf("GetVertex num: %v", err)
			}
			if got, err := client.IntValue(num); err != nil || int64(got) != bigInt {
				t.Errorf("num = %d (err %v), want %d (2^53+1 must survive)", got, err, bigInt)
			}
			edge, err := dst.GetEdge(ctx, "alice", "num")
			if err != nil {
				t.Fatalf("GetEdge alice->num: %v", err)
			}
			if edge.GetWeight() != 1.5 {
				t.Errorf("edge alice->num weight = %v, want 1.5", edge.GetWeight())
			}
		})
	}
}

// TestBackupSnapshot_ReferentialClosure_E2E proves the read/snapshot
// referential-closure contract (#750) end-to-end: after a vertex is deleted via
// the SDK, BackupSnapshot must not stream that vertex nor any edge incident to
// it, even though the edge physically survives until the dangling-edge GC sweep.
func TestBackupSnapshot_ReferentialClosure_E2E(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	val := provider.NewValidationInterceptor(defaultIntegrationValidationLimits())
	svc := service.NewLanternService(cache)
	srv := newConnectTestServer(t, svc, nil, val.ConnectInterceptor())
	sdk := newConnectClientFor(t, srv.url)
	raw := graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, k := range []string{"alice", "bob", "carol"} {
		if _, err := sdk.PutVertex(ctx, k, k, time.Minute); err != nil {
			t.Fatalf("PutVertex %s: %v", k, err)
		}
	}
	if _, err := sdk.AddEdge(ctx, "alice", "bob", 1.5, time.Minute); err != nil {
		t.Fatalf("AddEdge alice->bob: %v", err)
	}
	if _, err := sdk.AddEdge(ctx, "bob", "carol", 2.0, time.Minute); err != nil {
		t.Fatalf("AddEdge bob->carol: %v", err)
	}
	if _, err := sdk.DeleteVertex(ctx, "bob"); err != nil {
		t.Fatalf("DeleteVertex bob: %v", err)
	}

	vertices, edges := drainBackup(t, ctx, raw, &pb.BackupSnapshotRequest{})
	if _, ok := vertices["bob"]; ok {
		t.Errorf("backup streamed deleted vertex bob: %v", vertices)
	}
	if _, ok := edges["alice->bob"]; ok {
		t.Errorf("backup streamed dangling edge alice->bob: %v", edges)
	}
	if _, ok := edges["bob->carol"]; ok {
		t.Errorf("backup streamed dangling edge bob->carol: %v", edges)
	}
	// The surviving vertices are still streamed.
	for _, k := range []string{"alice", "carol"} {
		if _, ok := vertices[k]; !ok {
			t.Errorf("backup dropped live vertex %q: %v", k, vertices)
		}
	}
}

// Production config, runtime certification and private backup must agree on the
// namespace even when public authentication is OFF (#1935). The legacy fixture
// below deliberately omits that boundary only to test rejection, not recovery.
func TestBackupper_ProductionOFF_FinalDumpRestart_RealConnectWire(t *testing.T) {
	t.Run("FinalDumpFreshRestore", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		dir := t.TempDir()
		cfg := productionOFFBackupConfig(t, dir)
		var logs bytes.Buffer
		runtime, svc, producer := productionOFFBackupper(t, cfg, &logs)
		if stats, err := producer.RestoreOnStartup(ctx); err != nil || stats.Vertices != 0 {
			t.Fatalf("initial empty restore = %+v, %v", stats, err)
		}
		srv := newConnectTestServer(t, svc, nil)
		sdk := newConnectClientFor(t, srv.url)
		expiration := time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond)
		keys := []string{"ordinary", "sys:literal", "data:literal"}
		for _, key := range keys {
			deadline := expiration
			if key == "ordinary" {
				deadline = time.Time{}
			}
			if _, err := sdk.PutVertexAt(ctx, key, "persistent "+key, deadline); err != nil {
				t.Fatalf("PutVertexAt %q: %v", key, err)
			}
		}
		if _, err := sdk.PutEdgeAt(ctx, "sys:literal", "data:literal", 1.5, expiration); err != nil {
			t.Fatal(err)
		}
		if vertex, err := sdk.GetVertex(ctx, "ordinary"); err != nil || vertex.GetString_() != "persistent ordinary" {
			t.Fatalf("initial wire GetVertex = %v, %v", vertex, err)
		}

		// A long interval and an already-cancelled context enter the same final
		// dump branch as graceful worker shutdown without a timing-dependent tick.
		shutdown, stop := context.WithCancel(ctx)
		stop()
		if err := producer.Run(shutdown); err != nil {
			t.Fatal(err)
		}
		if text := logs.String(); !strings.Contains(text, `"msg":"backup: wrote dump"`) ||
			!strings.Contains(text, `"source":"shutdown"`) || strings.Contains(text, "dump on shutdown failed") {
			t.Fatalf("final dump did not complete: %s", text)
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".lbk") {
			t.Fatalf("final dump files = %v, %v; want one .lbk", entries, err)
		}
		srv.srv.Close()
		if err := runtime.Close(); err != nil {
			t.Fatal(err)
		}

		// No cache/service from the first instance participates in this restore.
		freshCfg := productionOFFBackupConfig(t, dir)
		freshRuntime, freshSvc, consumer := productionOFFBackupper(t, freshCfg, &logs)
		if freshRuntime.GraphCache() == runtime.GraphCache() {
			t.Fatal("restart reused the original cache")
		}
		stats, err := consumer.RestoreOnStartup(ctx)
		if err != nil || stats.Vertices != len(keys) || stats.Edges != 1 {
			t.Fatalf("fresh restore = %+v, %v", stats, err)
		}
		if !strings.Contains(logs.String(), `"msg":"backup: restored from dump"`) {
			t.Fatalf("restore success missing from logs: %s", logs.String())
		}
		fresh := newConnectTestServer(t, freshSvc, nil)
		freshSDK := newConnectClientFor(t, fresh.url)
		for _, key := range keys {
			vertex, err := freshSDK.GetVertex(ctx, key)
			if err != nil || vertex.GetKey() != key || vertex.GetString_() != "persistent "+key {
				t.Fatalf("restored GetVertex %q = %v, %v", key, vertex, err)
			}
			if key == "ordinary" {
				if vertex.GetExpiration() != nil {
					t.Fatalf("permanent vertex gained TTL: %v", vertex)
				}
			} else if vertex.GetExpiration() == nil || !vertex.GetExpiration().AsTime().Equal(expiration) {
				t.Fatalf("restored vertex TTL = %v, want %v", vertex.GetExpiration(), expiration)
			}
		}
		edge, err := freshSDK.GetEdge(ctx, "sys:literal", "data:literal")
		if err != nil || edge.GetWeight() != 1.5 || edge.GetExpiration() == nil || !edge.GetExpiration().AsTime().Equal(expiration) {
			t.Fatalf("restored edge = %v, %v", edge, err)
		}
		raw := graphv1connect.NewLanternServiceClient(h2cClient(), fresh.url)
		vertices, edges := drainBackup(t, ctx, raw, &pb.BackupSnapshotRequest{})
		if len(vertices) != len(keys) || len(edges) != 1 {
			t.Fatalf("public snapshot exposed unexpected identities: %v, %v", vertices, edges)
		}
		for _, key := range keys {
			if vertices[key] == nil {
				t.Fatalf("public snapshot lost logical key %q: %v", key, vertices)
			}
		}
	})

	t.Run("LegacyDumpRejectedWithoutGraphOrIndexEffects", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		dir := t.TempDir()
		legacySvc := service.NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour))
		legacy := newConnectTestServer(t, legacySvc, nil)
		legacySDK := newConnectClientFor(t, legacy.url)
		for _, key := range []string{"data:ordinary", "data:injected"} {
			if _, err := legacySDK.PutVertexAt(ctx, key, "legacyterm", time.Time{}); err != nil {
				t.Fatal(err)
			}
		}
		fixture := backup.New(legacySvc, backup.Config{Dir: dir, InstanceID: "legacy-fixture"}, nil, nil)
		if _, err := fixture.BackupNow(ctx); err != nil {
			t.Fatal(err)
		}
		legacy.srv.Close()
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 1 {
			t.Fatalf("legacy fixture files = %v, %v", entries, err)
		}
		path := filepath.Join(dir, entries[0].Name())
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		cfg := productionOFFBackupConfig(t, dir)
		var logs bytes.Buffer
		_, svc, consumer := productionOFFBackupper(t, cfg, &logs)
		// This already-mounted observer is deliberately a test fixture. A real
		// startup must return the restore error before mounting its listener.
		observer := newConnectTestServer(t, svc, nil)
		sdk := newConnectClientFor(t, observer.url)
		if _, err := sdk.PutVertexAt(ctx, "ordinary", "survivorterm", time.Time{}); err != nil {
			t.Fatal(err)
		}
		raw := graphv1connect.NewLanternServiceClient(h2cClient(), observer.url)
		statusBefore, err := raw.GetServerStatus(ctx, connect.NewRequest(&pb.GetServerStatusRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		// Search queries are not exact value filters; preserve the observed
		// ranked signatures for both the live and legacy content.
		searchBefore := make(map[string][]searchHitSignature)
		for _, query := range []string{"survivorterm", "legacyterm"} {
			signature, err := wireSearchSignature(ctx, raw, query, nil)
			if err != nil {
				t.Fatal(err)
			}
			searchBefore[query] = signature
		}
		stats, err := consumer.RestoreOnStartup(ctx)
		if err == nil || !strings.Contains(err.Error(), "incompatible physical namespace format") || stats.Vertices != 0 || stats.Edges != 0 {
			t.Fatalf("legacy dump was accepted or partially applied: %+v, %v", stats, err)
		}
		if vertex, err := sdk.GetVertex(ctx, "ordinary"); err != nil || vertex.GetString_() != "survivorterm" {
			t.Fatalf("failed restore changed live value: %v, %v", vertex, err)
		}
		if _, err := raw.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "injected"})); connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("failed restore injected a legacy record: %v", err)
		}
		statusAfter, err := raw.GetServerStatus(ctx, connect.NewRequest(&pb.GetServerStatusRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		beforeIndex := statusBefore.Msg.GetSearch().GetIndexStats()
		afterIndex := statusAfter.Msg.GetSearch().GetIndexStats()
		if beforeIndex == nil || afterIndex.GetHealth() != pb.SearchIndexHealth_SEARCH_INDEX_HEALTH_HEALTHY ||
			afterIndex.GetDocuments() != beforeIndex.GetDocuments() || afterIndex.GetLiveTerms() != beforeIndex.GetLiveTerms() {
			t.Fatalf("failed restore changed search index: before=%v after=%v", beforeIndex, afterIndex)
		}
		for query, signature := range searchBefore {
			if matches, err := wireSearchSignature(ctx, raw, query, nil); err != nil || !slices.Equal(matches, signature) {
				t.Fatalf("failed restore changed search %q: before=%v after=%v err=%v", query, signature, matches, err)
			}
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("failed restore rewrote the legacy archive: %v", err)
		}
	})
}

func productionOFFBackupConfig(t *testing.T, dir string) *provider.Config {
	t.Helper()
	t.Setenv("LANTERN_AUTH_MODE", "off")
	t.Setenv("LANTERN_RECEIPT_WAL_MODE", "graph-only")
	t.Setenv("LANTERN_BACKUP_ENABLED", "true")
	t.Setenv("LANTERN_BACKUP_DIR", dir)
	t.Setenv("LANTERN_BACKUP_INTERVAL", "1h")
	t.Setenv("LANTERN_BACKUP_RETAIN", "1")
	t.Setenv("LANTERN_BACKUP_INSTANCE_ID", "off-wire-owner")
	t.Setenv("LANTERN_BACKUP_RESTORE_ON_START", "true")
	t.Setenv("LANTERN_BACKUP_RESTORE_REQUIRED", "false")
	t.Setenv("LANTERN_SEARCH_ENABLED", "true")
	cfg, err := provider.NewConfig()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func productionOFFBackupper(t *testing.T, cfg *provider.Config, logs io.Writer) (*service.ServingRuntime, *service.LanternService, *backup.Backupper) {
	t.Helper()
	runtime, cleanup, err := provider.NewServingRuntime(cfg.ReceiptWAL, cfg.Cache, cfg.Search, cfg.MutationLog, cfg.Replication, cfg.Backup, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	svc := runtime.NewExecutor(nil).
		WithTombstoneTTL(cfg.Replication.TombstoneTTL).
		WithSearchLimits(productionSearchLimits(cfg.Search.Enabled, cfg.Search.Positions))
	replication, err := runtime.NewReplication(svc)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := provider.NewRuntimeRestored(runtime, svc)
	if err != nil {
		t.Fatal(err)
	}
	certified, err := provider.NewRuntimeCertified(runtime, svc, replication, restored, cfg.Net)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	b, err := provider.NewBackupper(provider.NewBackupConfig(cfg), cfg.ReceiptWAL, runtime, svc, certified, prometheus.NewRegistry(), logger)
	if err != nil {
		t.Fatal(err)
	}
	return runtime, svc, b
}
