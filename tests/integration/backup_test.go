package integration_test

import (
	"bytes"
	"context"
	"errors"
	"io"
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

	if err := sdk.PutVertex(ctx, "alice", "Alice", time.Minute); err != nil {
		t.Fatalf("PutVertex alice: %v", err)
	}
	if err := sdk.PutVertex(ctx, "bob", int64(42), time.Minute); err != nil {
		t.Fatalf("PutVertex bob: %v", err)
	}
	if err := sdk.PutVertex(ctx, "carol", "Carol", time.Minute); err != nil {
		t.Fatalf("PutVertex carol: %v", err)
	}
	if err := sdk.AddEdge(ctx, "alice", "bob", 1.5, time.Minute); err != nil {
		t.Fatalf("AddEdge alice->bob: %v", err)
	}
	if err := sdk.AddEdge(ctx, "bob", "carol", 2.0, time.Minute); err != nil {
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
		if err := sdk.PutVertex(ctx, "x:1", "one", time.Minute); err != nil {
			t.Fatalf("PutVertex x:1: %v", err)
		}
		if err := sdk.PutVertex(ctx, "x:2", "two", time.Minute); err != nil {
			t.Fatalf("PutVertex x:2: %v", err)
		}
		if err := sdk.AddEdge(ctx, "x:1", "x:2", 1.0, time.Minute); err != nil {
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
	srcCache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	srcSvc := service.NewLanternService(srcCache)
	srcSrv := newConnectTestServer(t, srcSvc, nil, val.ConnectInterceptor())
	srcSDK := newConnectClientFor(t, srcSrv.url)

	const bigInt = int64(9007199254740993) // 2^53 + 1
	if err := srcSDK.PutVertex(ctx, "alice", "Alice", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := srcSDK.PutVertex(ctx, "num", bigInt, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := srcSDK.AddEdge(ctx, "alice", "num", 1.5, time.Minute); err != nil {
		t.Fatal(err)
	}

	// Server-internal dump (same path as the periodic loop).
	if _, err := backup.New(srcSvc, cfg, nil, nil).BackupNow(ctx); err != nil {
		t.Fatalf("BackupNow: %v", err)
	}

	// Fresh service, restored on startup from the dump.
	dstCache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	dstSvc := service.NewLanternService(dstCache)
	dstSrv := newConnectTestServer(t, dstSvc, nil, val.ConnectInterceptor())
	dstRaw := graphv1connect.NewLanternServiceClient(h2cClient(), dstSrv.url)

	stats, err := backup.New(dstSvc, cfg, nil, nil).RestoreOnStartup(ctx)
	if err != nil {
		t.Fatalf("RestoreOnStartup: %v", err)
	}
	if stats.Vertices != 2 || stats.Edges != 1 {
		t.Fatalf("restore stats = %+v, want {2,1}", stats)
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
			if err := src.PutVertex(ctx, "alice", "Alice", time.Minute); err != nil {
				t.Fatal(err)
			}
			if err := src.PutVertex(ctx, "num", bigInt, time.Minute); err != nil {
				t.Fatal(err)
			}
			if err := src.PutVertex(ctx, "carol", "Carol", time.Minute); err != nil {
				t.Fatal(err)
			}
			if err := src.AddEdge(ctx, "alice", "num", 1.5, time.Minute); err != nil {
				t.Fatal(err)
			}
			if err := src.AddEdge(ctx, "num", "carol", 2.0, time.Minute); err != nil {
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
		if err := sdk.PutVertex(ctx, k, k, time.Minute); err != nil {
			t.Fatalf("PutVertex %s: %v", k, err)
		}
	}
	if err := sdk.AddEdge(ctx, "alice", "bob", 1.5, time.Minute); err != nil {
		t.Fatalf("AddEdge alice->bob: %v", err)
	}
	if err := sdk.AddEdge(ctx, "bob", "carol", 2.0, time.Minute); err != nil {
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
