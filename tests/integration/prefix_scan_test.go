package integration_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestPrefixScan_EndToEnd drives the prefix RPCs through the raw
// Connect-Go client (the SDK does not yet expose prefix wrappers —
// Phase 4). Seeded fixtures use a future Expiration so the suite also exercises
// expiring rows; an absent wire Timestamp is permanent storage.
func TestPrefixScan_EndToEnd(t *testing.T) {
	c, _ := newRawConnectClient(t, true)
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
	if _, err := c.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: verts})); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}

	// Count.
	cr, err := c.CountVerticesByPrefix(ctx, connect.NewRequest(&pb.CountVerticesByPrefixRequest{Prefix: "users/"}))
	if err != nil {
		t.Fatalf("CountVerticesByPrefix: %v", err)
	}
	if cr.Msg.Count != 5 {
		t.Errorf("count users/ = %d, want 5", cr.Msg.Count)
	}

	// Paginated scan.
	got := []string{}
	var cursor []byte
	for {
		r, err := c.ScanVertices(ctx, connect.NewRequest(&pb.ScanVerticesRequest{Prefix: "users/", Limit: 2, Cursor: cursor}))
		if err != nil {
			t.Fatalf("ScanVertices: %v", err)
		}
		for _, v := range r.Msg.Vertices {
			got = append(got, v.Key)
		}
		if len(r.Msg.NextCursor) == 0 {
			break
		}
		cursor = r.Msg.NextCursor
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
	dr, err := c.DeleteVerticesByPrefix(ctx, connect.NewRequest(&pb.DeleteVerticesByPrefixRequest{Prefix: "orders/", DryRun: true}))
	if err != nil {
		t.Fatalf("DeleteVerticesByPrefix dry: %v", err)
	}
	if dr.Msg.Deleted != 2 {
		t.Errorf("dry deleted = %d, want 2", dr.Msg.Deleted)
	}
	cr2, _ := c.CountVerticesByPrefix(ctx, connect.NewRequest(&pb.CountVerticesByPrefixRequest{Prefix: "orders/"}))
	if cr2.Msg.Count != 2 {
		t.Errorf("orders/ count after dry run = %d, want 2 (no mutation)", cr2.Msg.Count)
	}

	// Real delete.
	r, err := c.DeleteVerticesByPrefix(ctx, connect.NewRequest(&pb.DeleteVerticesByPrefixRequest{Prefix: "orders/"}))
	if err != nil {
		t.Fatalf("DeleteVerticesByPrefix: %v", err)
	}
	if r.Msg.Deleted != 2 {
		t.Errorf("deleted = %d, want 2", r.Msg.Deleted)
	}
	cr3, _ := c.CountVerticesByPrefix(ctx, connect.NewRequest(&pb.CountVerticesByPrefixRequest{Prefix: "orders/"}))
	if cr3.Msg.Count != 0 {
		t.Errorf("orders/ count after real delete = %d, want 0", cr3.Msg.Count)
	}
}
