package integration_test

import (
	"context"
	"net"
	"testing"
	"time"

	cachegraph "github.com/anaregdesign/lantern/core/cache/graph"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// newPrefixScanRaw spins up a bufconn-backed LanternService whose cache
// has the prefix index enabled, returning a raw pb client. The SDK does
// not yet expose prefix wrappers (Phase 4) so the test goes straight to
// the generated client.
func newPrefixScanRaw(t *testing.T) (pb.LanternServiceClient, func()) {
	t.Helper()
	lis := bufconn.Listen(1 << 16)
	gc := cachegraph.NewGraphCache[string, *pb.Vertex](time.Minute)
	gc.EnablePrefixIndex(func(s string) string { return s })

	srv := grpc.NewServer()
	pb.RegisterLanternServiceServer(srv, service.NewLanternService(gc))
	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient(
		"passthrough://bufconn",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	cleanup := func() {
		_ = conn.Close()
		srv.Stop()
		_ = lis.Close()
	}
	return pb.NewLanternServiceClient(conn), cleanup
}

func TestPrefixScan_EndToEnd(t *testing.T) {
	c, cleanup := newPrefixScanRaw(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Seed: 5 users/ + 2 orders/ + 3 sessions/.
	seed := []string{
		"users/1", "users/2", "users/3", "users/4", "users/5",
		"orders/1", "orders/2",
		"sessions/a", "sessions/b", "sessions/c",
	}
	verts := make([]*pb.Vertex, len(seed))
	exp := timestamppb.New(time.Now().Add(time.Hour))
	for i, k := range seed {
		verts[i] = &pb.Vertex{Key: k, Value: &pb.Vertex_String_{String_: k}, Expiration: exp}
	}
	if _, err := c.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: verts}); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}

	// Count.
	cr, err := c.CountVerticesByPrefix(ctx, &pb.CountVerticesByPrefixRequest{Prefix: "users/"})
	if err != nil {
		t.Fatalf("CountVerticesByPrefix: %v", err)
	}
	if cr.Count != 5 {
		t.Errorf("count users/ = %d, want 5", cr.Count)
	}

	// Paginated scan.
	got := []string{}
	var cursor []byte
	for {
		r, err := c.ScanVertices(ctx, &pb.ScanVerticesRequest{Prefix: "users/", Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("ScanVertices: %v", err)
		}
		for _, v := range r.Vertices {
			got = append(got, v.Key)
		}
		if len(r.NextCursor) == 0 {
			break
		}
		cursor = r.NextCursor
	}
	want := []string{"users/1", "users/2", "users/3", "users/4", "users/5"}
	if len(got) != len(want) {
		t.Fatalf("scan returned %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("scan[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// Dry run delete should not mutate.
	dr, err := c.DeleteVerticesByPrefix(ctx, &pb.DeleteVerticesByPrefixRequest{Prefix: "orders/", DryRun: true})
	if err != nil {
		t.Fatalf("DeleteVerticesByPrefix dry: %v", err)
	}
	if dr.Deleted != 2 {
		t.Errorf("dry deleted = %d, want 2", dr.Deleted)
	}
	cr2, _ := c.CountVerticesByPrefix(ctx, &pb.CountVerticesByPrefixRequest{Prefix: "orders/"})
	if cr2.Count != 2 {
		t.Errorf("orders/ count after dry run = %d, want 2 (no mutation)", cr2.Count)
	}

	// Real delete.
	r, err := c.DeleteVerticesByPrefix(ctx, &pb.DeleteVerticesByPrefixRequest{Prefix: "orders/"})
	if err != nil {
		t.Fatalf("DeleteVerticesByPrefix: %v", err)
	}
	if r.Deleted != 2 {
		t.Errorf("deleted = %d, want 2", r.Deleted)
	}
	cr3, _ := c.CountVerticesByPrefix(ctx, &pb.CountVerticesByPrefixRequest{Prefix: "orders/"})
	if cr3.Count != 0 {
		t.Errorf("orders/ count after real delete = %d, want 0", cr3.Count)
	}
}
