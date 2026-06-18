package integration_test

import (
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
