package service

import (
	"context"
	"slices"
	"testing"

	"connectrpc.com/connect"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestScanVertices_BasicAndCursor(t *testing.T) {
	fb := newFakeBackend()
	for _, k := range []string{"users/1", "users/2", "users/3", "orders/1"} {
		fb.vertices[k] = &pb.Vertex{Key: k, Value: &pb.Vertex_String_{String_: k}}
	}
	svc := NewLanternService(fb)
	ctx := context.Background()

	// First page of 2.
	r1, err := svc.ScanVertices(ctx, &pb.ScanVerticesRequest{Prefix: "users/", Limit: 2})
	if err != nil {
		t.Fatalf("ScanVertices: %v", err)
	}
	if len(r1.Vertices) != 2 {
		t.Fatalf("page1 size = %d, want 2", len(r1.Vertices))
	}
	if r1.Vertices[0].Key != "users/1" || r1.Vertices[1].Key != "users/2" {
		t.Errorf("page1 keys = %v", []string{r1.Vertices[0].Key, r1.Vertices[1].Key})
	}
	if len(r1.NextCursor) == 0 {
		t.Fatalf("expected non-empty next_cursor when limit hit")
	}

	// Second page.
	r2, err := svc.ScanVertices(ctx, &pb.ScanVerticesRequest{Prefix: "users/", Limit: 2, Cursor: r1.NextCursor})
	if err != nil {
		t.Fatalf("ScanVertices page2: %v", err)
	}
	if len(r2.Vertices) != 1 || r2.Vertices[0].Key != "users/3" {
		t.Errorf("page2 = %v", r2.Vertices)
	}
	if len(r2.NextCursor) != 0 {
		t.Errorf("expected empty next_cursor on final page, got %q", r2.NextCursor)
	}
}

func TestScanVertices_ClampsLimit(t *testing.T) {
	fb := newFakeBackend()
	svc := NewLanternService(fb).WithScanLimits(ScanLimits{
		ScanDefaultLimit: 5, ScanMaxLimit: 7,
		DeleteByPrefixDefaultLimit: 3, DeleteByPrefixMaxLimit: 10,
	})
	for i := 0; i < 20; i++ {
		k := string(rune('a'+i)) + "x"
		fb.vertices[k] = &pb.Vertex{Key: k}
	}
	// limit = 0 -> default 5
	r, err := svc.ScanVertices(context.Background(), &pb.ScanVerticesRequest{Limit: 0})
	if err != nil {
		t.Fatalf("ScanVertices: %v", err)
	}
	if len(r.Vertices) != 5 {
		t.Errorf("default limit page size = %d, want 5", len(r.Vertices))
	}
	// limit = 9999 -> capped at 7
	r2, err := svc.ScanVertices(context.Background(), &pb.ScanVerticesRequest{Limit: 9999})
	if err != nil {
		t.Fatalf("ScanVertices: %v", err)
	}
	if len(r2.Vertices) != 7 {
		t.Errorf("capped limit page size = %d, want 7", len(r2.Vertices))
	}
}

func TestScanVertices_RejectsBadCursor(t *testing.T) {
	svc := NewLanternService(newFakeBackend())
	_, err := svc.ScanVertices(context.Background(), &pb.ScanVerticesRequest{Cursor: []byte("not-base64!@#$")})
	if err == nil {
		t.Errorf("expected error for malformed cursor")
	}
}

// TestScanVertexKeys pins the keys-only prefix scan (#674): keys-only
// pagination, the mandatory non-empty prefix, and the independent cursor
// kind that must NOT interchange with ScanVertices in either direction.
func TestScanVertexKeys(t *testing.T) {
	fb := newFakeBackend()
	for _, k := range []string{"users/1", "users/2", "users/3", "orders/1"} {
		fb.vertices[k] = &pb.Vertex{Key: k, Value: &pb.Vertex_String_{String_: k}}
	}
	svc := NewLanternService(fb)
	ctx := context.Background()

	t.Run("BasicAndCursor", func(t *testing.T) {
		r1, err := svc.ScanVertexKeys(ctx, &pb.ScanVertexKeysRequest{Prefix: "users/", Limit: 2})
		if err != nil {
			t.Fatalf("ScanVertexKeys: %v", err)
		}
		if want := []string{"users/1", "users/2"}; !slices.Equal(r1.Keys, want) {
			t.Fatalf("page1 = %v, want %v", r1.Keys, want)
		}
		if len(r1.NextCursor) == 0 {
			t.Fatalf("expected non-empty next_cursor when limit hit")
		}
		r2, err := svc.ScanVertexKeys(ctx, &pb.ScanVertexKeysRequest{Prefix: "users/", Limit: 2, Cursor: r1.NextCursor})
		if err != nil {
			t.Fatalf("ScanVertexKeys page2: %v", err)
		}
		if want := []string{"users/3"}; !slices.Equal(r2.Keys, want) {
			t.Errorf("page2 = %v, want %v", r2.Keys, want)
		}
		if len(r2.NextCursor) != 0 {
			t.Errorf("expected empty next_cursor on final page, got %q", r2.NextCursor)
		}
	})

	t.Run("EmptyPrefixRejected", func(t *testing.T) {
		_, err := svc.ScanVertexKeys(ctx, &pb.ScanVertexKeysRequest{Prefix: ""})
		if err == nil {
			t.Fatalf("expected InvalidArgument for empty prefix")
		}
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("empty-prefix error code = %v, want InvalidArgument", connect.CodeOf(err))
		}
	})

	t.Run("CursorIndependentFromScanVertices", func(t *testing.T) {
		// A ScanVertices cursor must be rejected by ScanVertexKeys...
		rv, err := svc.ScanVertices(ctx, &pb.ScanVerticesRequest{Prefix: "users/", Limit: 1})
		if err != nil || len(rv.NextCursor) == 0 {
			t.Fatalf("setup ScanVertices: err=%v cursor=%d", err, len(rv.NextCursor))
		}
		if _, err := svc.ScanVertexKeys(ctx, &pb.ScanVertexKeysRequest{Prefix: "users/", Limit: 1, Cursor: rv.NextCursor}); err == nil {
			t.Errorf("ScanVertexKeys accepted a ScanVertices cursor; want rejection")
		}
		// ...and a ScanVertexKeys cursor must be rejected by ScanVertices.
		rk, err := svc.ScanVertexKeys(ctx, &pb.ScanVertexKeysRequest{Prefix: "users/", Limit: 1})
		if err != nil || len(rk.NextCursor) == 0 {
			t.Fatalf("setup ScanVertexKeys: err=%v cursor=%d", err, len(rk.NextCursor))
		}
		if _, err := svc.ScanVertices(ctx, &pb.ScanVerticesRequest{Prefix: "users/", Limit: 1, Cursor: rk.NextCursor}); err == nil {
			t.Errorf("ScanVertices accepted a ScanVertexKeys cursor; want rejection")
		}
	})
}

func TestCountVerticesByPrefix(t *testing.T) {
	fb := newFakeBackend()
	for _, k := range []string{"users/1", "users/2", "orders/1"} {
		fb.vertices[k] = &pb.Vertex{Key: k}
	}
	svc := NewLanternService(fb)
	r, err := svc.CountVerticesByPrefix(context.Background(), &pb.CountVerticesByPrefixRequest{Prefix: "users/"})
	if err != nil {
		t.Fatalf("CountVerticesByPrefix: %v", err)
	}
	if r.Count != 2 {
		t.Errorf("count = %d, want 2", r.Count)
	}
}

func TestDeleteVerticesByPrefix_DryRunAndReal(t *testing.T) {
	fb := newFakeBackend()
	for _, k := range []string{"users/1", "users/2", "users/3"} {
		fb.vertices[k] = &pb.Vertex{Key: k}
	}
	svc := NewLanternService(fb).WithScanLimits(ScanLimits{
		DeleteByPrefixDefaultLimit: 100, DeleteByPrefixMaxLimit: 1000,
		ScanDefaultLimit: 100, ScanMaxLimit: 100,
	})

	// Dry run should not mutate.
	dr, err := svc.DeleteVerticesByPrefix(context.Background(), &pb.DeleteVerticesByPrefixRequest{Prefix: "users/", DryRun: true})
	if err != nil {
		t.Fatalf("DeleteVerticesByPrefix dry: %v", err)
	}
	if dr.Deleted != 3 {
		t.Errorf("dry deleted = %d, want 3", dr.Deleted)
	}
	if len(fb.vertices) != 3 {
		t.Errorf("dry run mutated cache: %d remain", len(fb.vertices))
	}

	// Dry run is capped by limit.
	dr2, err := svc.DeleteVerticesByPrefix(context.Background(), &pb.DeleteVerticesByPrefixRequest{Prefix: "users/", DryRun: true, Limit: 2})
	if err != nil {
		t.Fatalf("DeleteVerticesByPrefix dry capped: %v", err)
	}
	if dr2.Deleted != 2 {
		t.Errorf("dry capped deleted = %d, want 2", dr2.Deleted)
	}

	// Real delete.
	r, err := svc.DeleteVerticesByPrefix(context.Background(), &pb.DeleteVerticesByPrefixRequest{Prefix: "users/"})
	if err != nil {
		t.Fatalf("DeleteVerticesByPrefix: %v", err)
	}
	if r.Deleted != 3 {
		t.Errorf("deleted = %d, want 3", r.Deleted)
	}
	if len(fb.vertices) != 0 {
		t.Errorf("cache still has %d entries after delete", len(fb.vertices))
	}
}

// TestScan_BadCursorFiresValidationRejectHook covers the #222
// bad_cursor hook fires from both ScanVertices and ScanEdges when the
// caller passes a malformed cursor.
func TestScan_BadCursorFiresValidationRejectHook(t *testing.T) {
	fb := newFakeBackend()
	var got []string
	svc := NewLanternService(fb).
		WithValidationRejectHook(func(reason string) { got = append(got, reason) })
	ctx := context.Background()

	if _, err := svc.ScanVertices(ctx, &pb.ScanVerticesRequest{Prefix: "x", Cursor: []byte("not-base64-or-anything-valid!!!")}); err == nil {
		t.Fatal("ScanVertices with bad cursor: expected error")
	}
	if _, err := svc.ScanEdges(ctx, &pb.ScanEdgesRequest{TailPrefix: "x", Cursor: []byte("not-base64-or-anything-valid!!!")}); err == nil {
		t.Fatal("ScanEdges with bad cursor: expected error")
	}
	if len(got) != 2 || got[0] != "bad_cursor" || got[1] != "bad_cursor" {
		t.Fatalf("reject hook calls = %v, want [bad_cursor bad_cursor]", got)
	}
}
