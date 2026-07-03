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

// TestScanVertices_DescendingOrder pins descending scans (#898): results come
// back high-to-low and pagination walks the range from the top, with the
// concatenated pages equal to the ascending set reversed.
func TestScanVertices_DescendingOrder(t *testing.T) {
	fb := newFakeBackend()
	for _, k := range []string{"users/1", "users/2", "users/3", "orders/1"} {
		fb.vertices[k] = &pb.Vertex{Key: k, Value: &pb.Vertex_String_{String_: k}}
	}
	svc := NewLanternService(fb)
	ctx := context.Background()

	r1, err := svc.ScanVertices(ctx, &pb.ScanVerticesRequest{Prefix: "users/", Limit: 2, Order: pb.ScanOrder_SCAN_ORDER_DESC})
	if err != nil {
		t.Fatalf("ScanVertices desc: %v", err)
	}
	if got := []string{r1.Vertices[0].Key, r1.Vertices[1].Key}; got[0] != "users/3" || got[1] != "users/2" {
		t.Fatalf("desc page1 = %v, want [users/3 users/2]", got)
	}
	if len(r1.NextCursor) == 0 {
		t.Fatalf("expected next_cursor on descending page1")
	}
	r2, err := svc.ScanVertices(ctx, &pb.ScanVerticesRequest{Prefix: "users/", Limit: 2, Order: pb.ScanOrder_SCAN_ORDER_DESC, Cursor: r1.NextCursor})
	if err != nil {
		t.Fatalf("ScanVertices desc page2: %v", err)
	}
	if len(r2.Vertices) != 1 || r2.Vertices[0].Key != "users/1" {
		t.Fatalf("desc page2 = %v, want [users/1]", r2.Vertices)
	}
	if len(r2.NextCursor) != 0 {
		t.Errorf("expected empty next_cursor on final descending page, got %q", r2.NextCursor)
	}
}

// TestScanVertices_OrderBoundCursor pins the order-binding rule (#898): a
// cursor minted by an ascending scan is rejected when replayed under a
// descending scan and vice versa, since its resume key means opposite things
// in the two directions.
func TestScanVertices_OrderBoundCursor(t *testing.T) {
	fb := newFakeBackend()
	for _, k := range []string{"users/1", "users/2", "users/3", "users/4"} {
		fb.vertices[k] = &pb.Vertex{Key: k, Value: &pb.Vertex_String_{String_: k}}
	}
	svc := NewLanternService(fb)
	ctx := context.Background()

	asc, err := svc.ScanVertices(ctx, &pb.ScanVerticesRequest{Prefix: "users/", Limit: 2})
	if err != nil || len(asc.NextCursor) == 0 {
		t.Fatalf("setup asc scan: err=%v cursor=%d", err, len(asc.NextCursor))
	}
	if _, err := svc.ScanVertices(ctx, &pb.ScanVerticesRequest{Prefix: "users/", Limit: 2, Order: pb.ScanOrder_SCAN_ORDER_DESC, Cursor: asc.NextCursor}); err == nil {
		t.Errorf("descending scan accepted an ascending cursor; want InvalidArgument")
	} else if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("order mismatch code = %v, want InvalidArgument", connect.CodeOf(err))
	}

	desc, err := svc.ScanVertices(ctx, &pb.ScanVerticesRequest{Prefix: "users/", Limit: 2, Order: pb.ScanOrder_SCAN_ORDER_DESC})
	if err != nil || len(desc.NextCursor) == 0 {
		t.Fatalf("setup desc scan: err=%v cursor=%d", err, len(desc.NextCursor))
	}
	if _, err := svc.ScanVertices(ctx, &pb.ScanVerticesRequest{Prefix: "users/", Limit: 2, Cursor: desc.NextCursor}); err == nil {
		t.Errorf("ascending scan accepted a descending cursor; want InvalidArgument")
	} else if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("order mismatch code = %v, want InvalidArgument", connect.CodeOf(err))
	}

	// An explicit SCAN_ORDER_ASC request must still accept a default
	// (unspecified-order) cursor — both normalise to ascending.
	if _, err := svc.ScanVertices(ctx, &pb.ScanVerticesRequest{Prefix: "users/", Limit: 2, Order: pb.ScanOrder_SCAN_ORDER_ASC, Cursor: asc.NextCursor}); err != nil {
		t.Errorf("explicit-ASC scan rejected a default-order cursor: %v", err)
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

	t.Run("DescendingOrder", func(t *testing.T) {
		r1, err := svc.ScanVertexKeys(ctx, &pb.ScanVertexKeysRequest{Prefix: "users/", Limit: 2, Order: pb.ScanOrder_SCAN_ORDER_DESC})
		if err != nil {
			t.Fatalf("ScanVertexKeys desc: %v", err)
		}
		if want := []string{"users/3", "users/2"}; !slices.Equal(r1.Keys, want) {
			t.Fatalf("desc page1 = %v, want %v", r1.Keys, want)
		}
		if len(r1.NextCursor) == 0 {
			t.Fatalf("expected next_cursor on descending page1")
		}
		r2, err := svc.ScanVertexKeys(ctx, &pb.ScanVertexKeysRequest{Prefix: "users/", Limit: 2, Order: pb.ScanOrder_SCAN_ORDER_DESC, Cursor: r1.NextCursor})
		if err != nil {
			t.Fatalf("ScanVertexKeys desc page2: %v", err)
		}
		if want := []string{"users/1"}; !slices.Equal(r2.Keys, want) {
			t.Errorf("desc page2 = %v, want %v", r2.Keys, want)
		}
	})

	t.Run("OrderBoundCursor", func(t *testing.T) {
		asc, err := svc.ScanVertexKeys(ctx, &pb.ScanVertexKeysRequest{Prefix: "users/", Limit: 1})
		if err != nil || len(asc.NextCursor) == 0 {
			t.Fatalf("setup asc: err=%v cursor=%d", err, len(asc.NextCursor))
		}
		if _, err := svc.ScanVertexKeys(ctx, &pb.ScanVertexKeysRequest{Prefix: "users/", Limit: 1, Order: pb.ScanOrder_SCAN_ORDER_DESC, Cursor: asc.NextCursor}); err == nil {
			t.Errorf("descending keys scan accepted an ascending cursor; want InvalidArgument")
		} else if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("order mismatch code = %v, want InvalidArgument", connect.CodeOf(err))
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

// TestDeleteEdgesByPrefix_ValidationDryRunAndReal covers the #899 handler:
// the whole-graph-wipe guard (both prefixes empty -> InvalidArgument + reject
// hook), the non-mutating dry run (count only, capped by limit), and the real
// delete (mutates and reports the count).
func TestDeleteEdgesByPrefix_ValidationDryRunAndReal(t *testing.T) {
	seed := func() *fakeBackend {
		fb := newFakeBackend()
		fb.edges = map[string]map[string]float32{
			"user:1":  {"post:10": 1, "post:11": 1, "session:a": 1},
			"user:2":  {"post:20": 1, "session:b": 1},
			"admin:1": {"post:99": 1},
		}
		return fb
	}
	limits := ScanLimits{
		DeleteByPrefixDefaultLimit: 100, DeleteByPrefixMaxLimit: 1000,
		ScanDefaultLimit: 100, ScanMaxLimit: 100,
	}
	ctx := context.Background()

	// Both prefixes empty is rejected and fires the validation hook.
	{
		var reasons []string
		svc := NewLanternService(seed()).
			WithScanLimits(limits).
			WithValidationRejectHook(func(r string) { reasons = append(reasons, r) })
		_, err := svc.DeleteEdgesByPrefix(ctx, &pb.DeleteEdgesByPrefixRequest{})
		if err == nil {
			t.Fatal("both-empty prefix: expected error")
		}
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("both-empty prefix: code = %v, want InvalidArgument", connect.CodeOf(err))
		}
		if len(reasons) != 1 || reasons[0] != "empty_edge_prefix" {
			t.Fatalf("reject hook = %v, want [empty_edge_prefix]", reasons)
		}
	}

	// Dry run counts the tail-scoped matches without mutating.
	{
		fb := seed()
		svc := NewLanternService(fb).WithScanLimits(limits)
		dr, err := svc.DeleteEdgesByPrefix(ctx, &pb.DeleteEdgesByPrefixRequest{TailPrefix: "user:", DryRun: true})
		if err != nil {
			t.Fatalf("dry run: %v", err)
		}
		if dr.Deleted != 5 {
			t.Errorf("dry deleted = %d, want 5", dr.Deleted)
		}
		total := 0
		for _, hs := range fb.edges {
			total += len(hs)
		}
		if total != 6 {
			t.Errorf("dry run mutated edges: %d remain, want 6", total)
		}
	}

	// Dry run is capped by limit.
	{
		svc := NewLanternService(seed()).WithScanLimits(limits)
		dr, err := svc.DeleteEdgesByPrefix(ctx, &pb.DeleteEdgesByPrefixRequest{TailPrefix: "user:", DryRun: true, Limit: 2})
		if err != nil {
			t.Fatalf("dry run capped: %v", err)
		}
		if dr.Deleted != 2 {
			t.Errorf("dry capped deleted = %d, want 2", dr.Deleted)
		}
	}

	// Real delete with tail+head intersection removes exactly the matches.
	{
		fb := seed()
		svc := NewLanternService(fb).WithScanLimits(limits)
		r, err := svc.DeleteEdgesByPrefix(ctx, &pb.DeleteEdgesByPrefixRequest{TailPrefix: "user:", HeadPrefix: "post:"})
		if err != nil {
			t.Fatalf("real delete: %v", err)
		}
		if r.Deleted != 3 {
			t.Errorf("deleted = %d, want 3", r.Deleted)
		}
		total := 0
		for _, hs := range fb.edges {
			total += len(hs)
		}
		if total != 3 {
			t.Errorf("after delete: %d edges remain, want 3", total)
		}
	}
}

// TestScan_BadCursorFiresValidationRejectHook covers the #222
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
