package service

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func latestPrefixMutation(t *testing.T, log *mutationlog.Log) *pb.Mutation {
	t.Helper()
	seq, ok := log.LastSeq()
	if !ok {
		t.Fatal("mutation log is empty")
	}
	ch, cancel, err := log.Subscribe(seq)
	if err != nil {
		t.Fatalf("Subscribe(%d): %v", seq, err)
	}
	defer cancel()
	select {
	case entry := <-ch:
		if entry.Seq != seq {
			t.Fatalf("latest log entry seq = %d, want %d", entry.Seq, seq)
		}
		effect, ok := entry.Op.(*graphDeleteEffectEnvelope)
		if !ok {
			t.Fatalf("latest prefix WAL entry = %T, want graph Delete effect", entry.Op)
		}
		count, ok := graphDeleteRequestCount(effect.Mutation)
		if !ok || len(effect.AcceptedIndexes) != count {
			t.Fatalf("prefix Delete evidence = %+v, want every exact victim accepted", effect)
		}
		for i, index := range effect.AcceptedIndexes {
			if index != uint32(i) {
				t.Fatalf("prefix Delete accepted indexes = %v, want complete ordered set", effect.AcceptedIndexes)
			}
		}
		return mustGraphMutation(t, entry.Op)
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out reading mutation %d", seq)
		return nil
	}
}

type prefixScanReapBackend struct {
	*graphcache.GraphCache[string, *pb.Vertex]
	vertexScans int
	edgeScans   int
}

type prefixCancelBackend struct {
	Backend
	cancel context.CancelFunc
}

func (b *prefixCancelBackend) DeleteByPrefixKeysWithPreflight(ctx context.Context, prefix string, limit int, preflight func([]string) error) ([]string, error) {
	b.cancel()
	return b.Backend.DeleteByPrefixKeysWithPreflight(ctx, prefix, limit, preflight)
}

func (b *prefixCancelBackend) DeleteByPrefixHLCCheckedKeysWithPreflight(ctx context.Context, prefix string, limit uint32, ts hlc.Timestamp, expiration time.Time, preflight func([]string) error) ([]string, error) {
	b.cancel()
	return b.Backend.DeleteByPrefixHLCCheckedKeysWithPreflight(ctx, prefix, limit, ts, expiration, preflight)
}

func (b *prefixCancelBackend) DeleteEdgesByPrefixKeysWithPreflight(ctx context.Context, tailPrefix, headPrefix string, limit int, preflight func([]graphcache.EdgeKey[string]) error) ([]graphcache.EdgeKey[string], error) {
	b.cancel()
	return b.Backend.DeleteEdgesByPrefixKeysWithPreflight(ctx, tailPrefix, headPrefix, limit, preflight)
}

func (b *prefixCancelBackend) DeleteEdgesByPrefixHLCCheckedKeysWithPreflight(ctx context.Context, tailPrefix, headPrefix string, limit int, ts hlc.Timestamp, expiration time.Time, preflight func([]graphcache.EdgeKey[string]) error) ([]graphcache.EdgeKey[string], error) {
	b.cancel()
	return b.Backend.DeleteEdgesByPrefixHLCCheckedKeysWithPreflight(ctx, tailPrefix, headPrefix, limit, ts, expiration, preflight)
}

func (b *prefixScanReapBackend) ScanByPrefixPage(
	ctx context.Context, prefix, after string, limit int, desc bool,
	fn func(string, string, *pb.Vertex) bool,
) (bool, bool) {
	var first string
	more, ok := b.GraphCache.ScanByPrefixPage(ctx, prefix, after, limit, desc, func(projected, key string, value *pb.Vertex) bool {
		if first == "" {
			first = key
		}
		return fn(projected, key, value)
	})
	b.vertexScans++
	if first != "" {
		b.DeleteVertex(first)
	}
	return more, ok
}

func (b *prefixScanReapBackend) ScanEdgesByPrefixPage(
	ctx context.Context, tailPrefix, headPrefix, afterTail, afterHead string, limit int,
	fn func(string, string, string, string, float32, time.Time) bool,
) (bool, bool) {
	var first graphcache.EdgeKey[string]
	more, ok := b.GraphCache.ScanEdgesByPrefixPage(ctx, tailPrefix, headPrefix, afterTail, afterHead, limit,
		func(tailProjected, tail, headProjected, head string, weight float32, expiration time.Time) bool {
			if first == (graphcache.EdgeKey[string]{}) {
				first = graphcache.EdgeKey[string]{Tail: tail, Head: head}
			}
			return fn(tailProjected, tail, headProjected, head, weight, expiration)
		})
	b.edgeScans++
	if first != (graphcache.EdgeKey[string]{}) {
		b.DeleteEdge(first.Tail, first.Head)
	}
	return more, ok
}

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
	fb.vertices["users/1"].Value = &pb.Vertex_String_{String_: "changed during replay"}
	if got := r1.Vertices[0].GetString_(); got != "users/1" {
		t.Fatalf("scan response retained a mutable cache vertex: %q", got)
	}
}

func TestPrefixGraphReadsFailClosedUntilVerifiedSnapshot(t *testing.T) {
	fb := newFakeBackend()
	fb.vertices["user:1"] = &pb.Vertex{Key: "user:1"}
	fb.edges["user:1"] = map[string]float32{"post:1": 2}
	svc := NewLanternService(fb)
	ctx := context.Background()
	reads := []struct {
		name string
		run  func() error
	}{
		{"ScanVertices", func() error {
			_, err := svc.ScanVertices(ctx, &pb.ScanVerticesRequest{Prefix: "user:"})
			return err
		}},
		{"ScanVertexKeys", func() error {
			_, err := svc.ScanVertexKeys(ctx, &pb.ScanVertexKeysRequest{Prefix: "user:"})
			return err
		}},
		{"CountVerticesByPrefix", func() error {
			_, err := svc.CountVerticesByPrefix(ctx, &pb.CountVerticesByPrefixRequest{Prefix: "user:"})
			return err
		}},
		{"ScanEdges", func() error {
			_, err := svc.ScanEdges(ctx, &pb.ScanEdgesRequest{TailPrefix: "user:"})
			return err
		}},
		{"DeleteVerticesByPrefix dry run", func() error {
			_, err := svc.DeleteVerticesByPrefix(ctx, &pb.DeleteVerticesByPrefixRequest{Prefix: "user:", DryRun: true})
			return err
		}},
		{"DeleteEdgesByPrefix dry run", func() error {
			_, err := svc.DeleteEdgesByPrefix(ctx, &pb.DeleteEdgesByPrefixRequest{TailPrefix: "user:", DryRun: true})
			return err
		}},
	}
	assertFaulted := func(stage string) {
		t.Helper()
		for _, read := range reads {
			t.Run(stage+"/"+read.name, func(t *testing.T) {
				if err := read.run(); connect.CodeOf(err) != connect.CodeFailedPrecondition {
					t.Fatalf("read during %s = %v, want FailedPrecondition", stage, err)
				}
			})
		}
	}
	finish, err := svc.BeginSnapshotInstall()
	if err != nil {
		t.Fatal(err)
	}
	assertFaulted("active install")
	finish(false)
	assertFaulted("failed install")
	retry, err := svc.BeginSnapshotInstall()
	if err != nil {
		t.Fatal(err)
	}
	retry(true)
	for _, read := range reads {
		t.Run("verified retry/"+read.name, func(t *testing.T) {
			if err := read.run(); err != nil {
				t.Fatalf("read after verified retry: %v", err)
			}
		})
	}
	if len(fb.vertices) != 1 || len(fb.edges["user:1"]) != 1 {
		t.Fatal("dry-run reads mutated the graph")
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

func TestDeleteVerticesByPrefix_ReplicationFrameRejectedBeforeDelete(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	cache.EnablePrefixIndex(func(key string) string { return key })
	keys := []string{
		"users/a" + strings.Repeat("a", 96),
		"users/b" + strings.Repeat("b", 96),
	}
	for _, key := range keys {
		if err := cache.PutVertex(key, &pb.Vertex{Key: key}); err != nil {
			t.Fatal(err)
		}
	}
	log := mutationlog.New(mutationlog.Options{Capacity: 8, SubscriberBuffer: 8})
	runtime, err := NewGraphOnlyServingRuntime(cache, log, hlc.New(hlc.NodeID{0xC0}, hlc.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	svc := runtime.NewLanternService(nil).WithScanLimits(ScanLimits{
		DeleteByPrefixDefaultLimit: 100,
		DeleteByPrefixMaxLimit:     100,
	})
	replication, err := runtime.NewLanternReplicationService(svc)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CertifyInstallationWithReplicationSendLimit(svc, replication, 128); err != nil {
		t.Fatal(err)
	}

	_, err = svc.DeleteVerticesByPrefix(
		context.Background(),
		&pb.DeleteVerticesByPrefixRequest{Prefix: "users/"},
	)
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("oversized prefix delete = %v, want ResourceExhausted", err)
	}
	for _, key := range keys {
		if _, ok := cache.GetVertex(key); !ok {
			t.Fatalf("oversized prefix delete removed %q", key)
		}
	}
	if length, _, _ := runtime.MutationLogStats(); length != 0 {
		t.Fatalf("oversized prefix delete retained %d log entries", length)
	}
	if seq := svc.LocalSeq(hlc.NodeID{0xC0}); seq != 0 {
		t.Fatalf("oversized prefix delete advanced origin seq to %d", seq)
	}
}

func TestDeleteVerticesByPrefix_AtomicVictimSelection(t *testing.T) {
	for _, tombstone := range []struct {
		name string
		ttl  time.Duration
	}{
		{name: "no tombstones"},
		{name: "tombstones", ttl: time.Hour},
	} {
		t.Run(tombstone.name, func(t *testing.T) {
			cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
			cache.EnablePrefixIndex(func(key string) string { return key })
			for _, key := range []string{"users/a", "users/b"} {
				if err := cache.PutVertex(key, &pb.Vertex{Key: key}); err != nil {
					t.Fatal(err)
				}
			}
			backend := &prefixScanReapBackend{GraphCache: cache}
			log := mutationlog.New(mutationlog.Options{Capacity: 8, SubscriberBuffer: 8})
			t.Cleanup(func() { _ = log.Close() })
			svc := NewLanternService(backend).
				WithReplication(log, hlc.New(hlc.NodeID{0xF1}, hlc.Options{}), nil).
				WithTombstoneTTL(tombstone.ttl)

			resp, err := svc.DeleteVerticesByPrefix(context.Background(), &pb.DeleteVerticesByPrefixRequest{
				Prefix: "users/", Limit: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			if resp.GetDeleted() != 1 {
				t.Fatalf("deleted = %d with %d live matches remaining; want 1", resp.GetDeleted(), cache.CountByPrefix("users/"))
			}
			if backend.vertexScans != 0 {
				t.Fatalf("replicated delete ran %d unlocked scans", backend.vertexScans)
			}
			if _, ok := cache.GetVertex("users/a"); ok {
				t.Fatal("first victim remains")
			}
			if _, ok := cache.GetVertex("users/b"); !ok {
				t.Fatal("bounded delete removed the second victim")
			}
			if mutation := latestPrefixMutation(t, log); !slices.Equal(mutation.GetOp().GetDeleteVertices().GetKeys(), []string{"users/a"}) {
				t.Fatalf("logged mutation = %v, want exact first victim", mutation)
			}
		})
	}
}

func TestDeletePrefix_CancelledBeforeEmptyCacheLock(t *testing.T) {
	for _, tc := range []struct {
		name string
		edge bool
		ttl  time.Duration
	}{
		{name: "vertex without tombstones"},
		{name: "vertex with tombstones", ttl: time.Hour},
		{name: "edge without tombstones", edge: true},
		{name: "edge with tombstones", edge: true, ttl: time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backend := &prefixCancelBackend{Backend: cache, cancel: cancel}
			log := mutationlog.New(mutationlog.Options{Capacity: 8})
			t.Cleanup(func() { _ = log.Close() })
			node := hlc.NodeID{0xE0}
			svc := NewLanternService(backend).
				WithReplication(log, hlc.New(node, hlc.Options{}), nil).
				WithTombstoneTTL(tc.ttl)

			var err error
			if tc.edge {
				_, err = svc.DeleteEdgesByPrefix(ctx, &pb.DeleteEdgesByPrefixRequest{TailPrefix: "users/"})
			} else {
				_, err = svc.DeleteVerticesByPrefix(ctx, &pb.DeleteVerticesByPrefixRequest{Prefix: "users/"})
			}
			if connect.CodeOf(err) != connect.CodeCanceled {
				t.Fatalf("canceled prefix delete = %v, want Canceled", err)
			}
			if log.Len() != 0 || svc.LocalSeq(node) != 0 {
				t.Fatal("canceled prefix delete published a mutation")
			}
		})
	}
}

// TestDeleteVerticesByPrefix_CausalLimitReplicatesExactVictim verifies that a
// bounded local prefix delete is represented on the HA log by the exact key
// that committed. Replaying the broad prefix on a peer would delete both
// matches and violate both the request limit and the causal admission result.
// A second call needs a new causal identity, so it must reject atomically and
// leave both graph state and the mutation log unchanged.
func TestDeleteVerticesByPrefix_CausalLimitReplicatesExactVictim(t *testing.T) {
	ctx := context.Background()
	newCache := func() *graphcache.GraphCache[string, *pb.Vertex] {
		cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
		cache.EnablePrefixIndex(func(key string) string { return key })
		cache.SetCausalMetadataLimits(graphcache.CausalMetadataLimits{MaxVertexEntries: 1})
		for _, key := range []string{"users/a", "users/b"} {
			if err := cache.PutVertex(key, &pb.Vertex{Key: key, Value: &pb.Vertex_String_{String_: key}}); err != nil {
				t.Fatalf("seed %q: %v", key, err)
			}
		}
		return cache
	}
	originCache := newCache()
	peerCache := newCache()
	log := mutationlog.New(mutationlog.Options{Capacity: 8, SubscriberBuffer: 8})
	t.Cleanup(func() { _ = log.Close() })
	origin := NewLanternService(originCache).
		WithTombstoneTTL(time.Hour).
		WithReplication(log, hlc.New(hlc.NodeID{0xC1}, hlc.Options{}), nil)
	peer := NewLanternService(peerCache).WithTombstoneTTL(time.Hour)

	resp, err := origin.DeleteVerticesByPrefix(ctx, &pb.DeleteVerticesByPrefixRequest{Prefix: "users/", Limit: 1})
	if err != nil {
		t.Fatalf("first bounded delete: %v", err)
	}
	if resp.GetDeleted() != 1 {
		t.Fatalf("first bounded delete count = %d, want 1", resp.GetDeleted())
	}
	mutation := latestPrefixMutation(t, log)
	exact := mutation.GetOp().GetDeleteVertices()
	if exact == nil || len(exact.GetKeys()) != 1 {
		t.Fatalf("logged op = %v, want exact single-key DeleteVertices", mutation.GetOp())
	}
	committed := exact.GetKeys()[0]
	remaining := "users/a"
	if committed == remaining {
		remaining = "users/b"
	}
	if _, ok := originCache.GetVertex(committed); ok {
		t.Fatalf("origin retained committed victim %q", committed)
	}
	if _, ok := originCache.GetVertex(remaining); !ok {
		t.Fatalf("origin widened bounded delete to %q", remaining)
	}

	if err := peer.ApplyMutation(ctx, mutation); err != nil {
		t.Fatalf("peer apply exact delete: %v", err)
	}
	if _, ok := peerCache.GetVertex(committed); ok {
		t.Fatalf("peer retained committed victim %q", committed)
	}
	if _, ok := peerCache.GetVertex(remaining); !ok {
		t.Fatalf("peer replay widened exact delete to %q", remaining)
	}

	beforeSeq, _ := log.LastSeq()
	beforeEntries := originCache.CausalMetadataStats().VertexEntries
	_, err = origin.DeleteVerticesByPrefix(ctx, &pb.DeleteVerticesByPrefixRequest{Prefix: "users/", Limit: 1})
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("second identity error = %v (%v), want ResourceExhausted", err, connect.CodeOf(err))
	}
	if afterSeq, _ := log.LastSeq(); afterSeq != beforeSeq {
		t.Fatalf("rejected prefix delete appended log seq %d -> %d", beforeSeq, afterSeq)
	}
	if _, ok := originCache.GetVertex(remaining); !ok {
		t.Fatalf("rejected prefix delete removed %q", remaining)
	}
	if got := originCache.CausalMetadataStats().VertexEntries; got != beforeEntries {
		t.Fatalf("rejected prefix delete changed causal entries %d -> %d", beforeEntries, got)
	}
}

func TestDeleteVerticesByPrefix_ZeroTombstoneTTLReplicatesExactVictim(t *testing.T) {
	ctx := context.Background()
	newCache := func() *graphcache.GraphCache[string, *pb.Vertex] {
		cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
		cache.EnablePrefixIndex(func(key string) string { return key })
		for _, key := range []string{"users/a", "users/b"} {
			if err := cache.PutVertex(key, &pb.Vertex{Key: key}); err != nil {
				t.Fatalf("seed %q: %v", key, err)
			}
		}
		return cache
	}
	originCache, peerCache := newCache(), newCache()
	log := mutationlog.New(mutationlog.Options{Capacity: 8, SubscriberBuffer: 8})
	t.Cleanup(func() { _ = log.Close() })
	origin := NewLanternService(originCache).
		WithReplication(log, hlc.New(hlc.NodeID{0xD1}, hlc.Options{}), nil)
	peer := NewLanternService(peerCache)

	resp, err := origin.DeleteVerticesByPrefix(ctx, &pb.DeleteVerticesByPrefixRequest{
		Prefix: "users/", Limit: 1,
	})
	if err != nil || resp.GetDeleted() != 1 {
		t.Fatalf("bounded zero-TTL delete = (%v, %v), want deleted=1", resp, err)
	}
	mutation := latestPrefixMutation(t, log)
	exact := mutation.GetOp().GetDeleteVertices()
	if exact == nil || len(exact.GetKeys()) != 1 || mutation.GetOp().GetDeleteVerticesByPrefix() != nil {
		t.Fatalf("logged op = %v, want exact single-key DeleteVertices", mutation.GetOp())
	}
	committed := exact.GetKeys()[0]
	remaining := "users/a"
	if committed == remaining {
		remaining = "users/b"
	}
	if err := peer.ApplyMutation(ctx, mutation); err != nil {
		t.Fatalf("peer apply exact zero-TTL delete: %v", err)
	}
	for name, cache := range map[string]*graphcache.GraphCache[string, *pb.Vertex]{
		"origin": originCache, "peer": peerCache,
	} {
		if _, ok := cache.GetVertex(committed); ok {
			t.Fatalf("%s retained committed victim %q", name, committed)
		}
		if _, ok := cache.GetVertex(remaining); !ok {
			t.Fatalf("%s widened bounded delete to %q", name, remaining)
		}
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

func TestDeleteEdgesByPrefix_ReplicationFrameRejectedBeforeDelete(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	cache.EnablePrefixIndex(func(key string) string { return key })
	edges := []graphcache.EdgeKey[string]{
		{Tail: "users/a" + strings.Repeat("a", 96), Head: "posts/a" + strings.Repeat("a", 96)},
		{Tail: "users/b" + strings.Repeat("b", 96), Head: "posts/b" + strings.Repeat("b", 96)},
	}
	for _, key := range edges {
		cache.PutEdgeWithExpiration(key.Tail, key.Head, 1, time.Now().Add(time.Hour))
	}
	log := mutationlog.New(mutationlog.Options{Capacity: 8, SubscriberBuffer: 8})
	runtime, err := NewGraphOnlyServingRuntime(cache, log, hlc.New(hlc.NodeID{0xC4}, hlc.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	svc := runtime.NewLanternService(nil).WithScanLimits(ScanLimits{
		DeleteByPrefixDefaultLimit: 100,
		DeleteByPrefixMaxLimit:     100,
	})
	replication, err := runtime.NewLanternReplicationService(svc)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CertifyInstallationWithReplicationSendLimit(svc, replication, 128); err != nil {
		t.Fatal(err)
	}

	_, err = svc.DeleteEdgesByPrefix(context.Background(), &pb.DeleteEdgesByPrefixRequest{
		TailPrefix: "users/", HeadPrefix: "posts/",
	})
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("oversized edge prefix delete = %v, want ResourceExhausted", err)
	}
	for _, key := range edges {
		if _, ok := cache.GetWeight(key.Tail, key.Head); !ok {
			t.Fatalf("oversized edge prefix delete removed %q -> %q", key.Tail, key.Head)
		}
	}
	if length, _, _ := runtime.MutationLogStats(); length != 0 {
		t.Fatalf("oversized edge prefix delete retained %d log entries", length)
	}
	if seq := svc.LocalSeq(hlc.NodeID{0xC4}); seq != 0 {
		t.Fatalf("oversized edge prefix delete advanced origin seq to %d", seq)
	}
}

// TestDeleteEdgesByPrefix_CausalLimitReplicatesExactVictim is the edge sibling
// of TestDeleteVerticesByPrefix_CausalLimitReplicatesExactVictim.
func TestDeleteEdgesByPrefix_CausalLimitReplicatesExactVictim(t *testing.T) {
	ctx := context.Background()
	type edgeID struct{ tail, head string }
	edges := []edgeID{{tail: "users/a", head: "posts/a"}, {tail: "users/b", head: "posts/b"}}
	newCache := func() *graphcache.GraphCache[string, *pb.Vertex] {
		cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
		cache.EnablePrefixIndex(func(key string) string { return key })
		cache.SetCausalMetadataLimits(graphcache.CausalMetadataLimits{MaxEdgeEntries: 1})
		for _, edge := range edges {
			cache.PutEdgeWithExpiration(edge.tail, edge.head, 1, time.Now().Add(time.Hour))
		}
		return cache
	}
	originCache := newCache()
	peerCache := newCache()
	log := mutationlog.New(mutationlog.Options{Capacity: 8, SubscriberBuffer: 8})
	t.Cleanup(func() { _ = log.Close() })
	origin := NewLanternService(originCache).
		WithTombstoneTTL(time.Hour).
		WithReplication(log, hlc.New(hlc.NodeID{0xC2}, hlc.Options{}), nil)
	peer := NewLanternService(peerCache).WithTombstoneTTL(time.Hour)

	resp, err := origin.DeleteEdgesByPrefix(ctx, &pb.DeleteEdgesByPrefixRequest{
		TailPrefix: "users/", HeadPrefix: "posts/", Limit: 1,
	})
	if err != nil {
		t.Fatalf("first bounded edge delete: %v", err)
	}
	if resp.GetDeleted() != 1 {
		t.Fatalf("first bounded edge delete count = %d, want 1", resp.GetDeleted())
	}
	mutation := latestPrefixMutation(t, log)
	exact := mutation.GetOp().GetDeleteEdges()
	if exact == nil || len(exact.GetEdges()) != 1 {
		t.Fatalf("logged op = %v, want exact single-key DeleteEdges", mutation.GetOp())
	}
	committed := edgeID{tail: exact.GetEdges()[0].GetTail(), head: exact.GetEdges()[0].GetHead()}
	remaining := edges[0]
	if committed == remaining {
		remaining = edges[1]
	}
	if _, ok := originCache.GetWeight(committed.tail, committed.head); ok {
		t.Fatalf("origin retained committed edge %q -> %q", committed.tail, committed.head)
	}
	if _, ok := originCache.GetWeight(remaining.tail, remaining.head); !ok {
		t.Fatalf("origin widened bounded edge delete to %q -> %q", remaining.tail, remaining.head)
	}

	if err := peer.ApplyMutation(ctx, mutation); err != nil {
		t.Fatalf("peer apply exact edge delete: %v", err)
	}
	if _, ok := peerCache.GetWeight(committed.tail, committed.head); ok {
		t.Fatalf("peer retained committed edge %q -> %q", committed.tail, committed.head)
	}
	if _, ok := peerCache.GetWeight(remaining.tail, remaining.head); !ok {
		t.Fatalf("peer replay widened exact edge delete to %q -> %q", remaining.tail, remaining.head)
	}

	beforeSeq, _ := log.LastSeq()
	beforeEntries := originCache.CausalMetadataStats().EdgeEntries
	_, err = origin.DeleteEdgesByPrefix(ctx, &pb.DeleteEdgesByPrefixRequest{
		TailPrefix: "users/", HeadPrefix: "posts/", Limit: 1,
	})
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("second edge identity error = %v (%v), want ResourceExhausted", err, connect.CodeOf(err))
	}
	if afterSeq, _ := log.LastSeq(); afterSeq != beforeSeq {
		t.Fatalf("rejected edge prefix delete appended log seq %d -> %d", beforeSeq, afterSeq)
	}
	if _, ok := originCache.GetWeight(remaining.tail, remaining.head); !ok {
		t.Fatalf("rejected edge prefix delete removed %q -> %q", remaining.tail, remaining.head)
	}
	if got := originCache.CausalMetadataStats().EdgeEntries; got != beforeEntries {
		t.Fatalf("rejected edge prefix delete changed causal entries %d -> %d", beforeEntries, got)
	}
}

func TestDeleteEdgesByPrefix_ZeroTombstoneTTLReplicatesExactVictim(t *testing.T) {
	ctx := context.Background()
	type edgeID struct{ tail, head string }
	edges := []edgeID{{tail: "users/a", head: "posts/a"}, {tail: "users/b", head: "posts/b"}}
	newCache := func() *graphcache.GraphCache[string, *pb.Vertex] {
		cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
		cache.EnablePrefixIndex(func(key string) string { return key })
		for _, edge := range edges {
			cache.PutEdgeWithExpiration(edge.tail, edge.head, 1, time.Now().Add(time.Hour))
		}
		return cache
	}
	originCache, peerCache := newCache(), newCache()
	log := mutationlog.New(mutationlog.Options{Capacity: 8, SubscriberBuffer: 8})
	t.Cleanup(func() { _ = log.Close() })
	origin := NewLanternService(originCache).
		WithReplication(log, hlc.New(hlc.NodeID{0xD2}, hlc.Options{}), nil)
	peer := NewLanternService(peerCache)

	resp, err := origin.DeleteEdgesByPrefix(ctx, &pb.DeleteEdgesByPrefixRequest{
		TailPrefix: "users/", HeadPrefix: "posts/", Limit: 1,
	})
	if err != nil || resp.GetDeleted() != 1 {
		t.Fatalf("bounded zero-TTL edge delete = (%v, %v), want deleted=1", resp, err)
	}
	mutation := latestPrefixMutation(t, log)
	exact := mutation.GetOp().GetDeleteEdges()
	if exact == nil || len(exact.GetEdges()) != 1 || mutation.GetOp().GetDeleteEdgesByPrefix() != nil {
		t.Fatalf("logged op = %v, want exact single-edge DeleteEdges", mutation.GetOp())
	}
	committed := edgeID{tail: exact.GetEdges()[0].GetTail(), head: exact.GetEdges()[0].GetHead()}
	remaining := edges[0]
	if committed == remaining {
		remaining = edges[1]
	}
	if err := peer.ApplyMutation(ctx, mutation); err != nil {
		t.Fatalf("peer apply exact zero-TTL edge delete: %v", err)
	}
	for name, cache := range map[string]*graphcache.GraphCache[string, *pb.Vertex]{
		"origin": originCache, "peer": peerCache,
	} {
		if _, ok := cache.GetWeight(committed.tail, committed.head); ok {
			t.Fatalf("%s retained committed edge %q -> %q", name, committed.tail, committed.head)
		}
		if _, ok := cache.GetWeight(remaining.tail, remaining.head); !ok {
			t.Fatalf("%s widened bounded edge delete to %q -> %q", name, remaining.tail, remaining.head)
		}
	}
}

func TestDeleteEdgesByPrefix_AtomicVictimSelection(t *testing.T) {
	for _, tombstone := range []struct {
		name string
		ttl  time.Duration
	}{
		{name: "no tombstones"},
		{name: "tombstones", ttl: time.Hour},
	} {
		t.Run(tombstone.name, func(t *testing.T) {
			cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
			cache.EnablePrefixIndex(func(key string) string { return key })
			for _, head := range []string{"posts/a", "posts/b"} {
				cache.PutEdgeWithExpiration("users/a", head, 1, time.Now().Add(time.Hour))
			}
			backend := &prefixScanReapBackend{GraphCache: cache}
			log := mutationlog.New(mutationlog.Options{Capacity: 8, SubscriberBuffer: 8})
			t.Cleanup(func() { _ = log.Close() })
			svc := NewLanternService(backend).
				WithReplication(log, hlc.New(hlc.NodeID{0xF2}, hlc.Options{}), nil).
				WithTombstoneTTL(tombstone.ttl)

			resp, err := svc.DeleteEdgesByPrefix(context.Background(), &pb.DeleteEdgesByPrefixRequest{
				TailPrefix: "users/", HeadPrefix: "posts/", Limit: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			if resp.GetDeleted() != 1 {
				t.Fatalf("deleted = %d with later edge still live; want 1", resp.GetDeleted())
			}
			if backend.edgeScans != 0 {
				t.Fatalf("replicated edge delete ran %d unlocked scans", backend.edgeScans)
			}
			if _, ok := cache.GetWeight("users/a", "posts/a"); ok {
				t.Fatal("first edge remains")
			}
			if _, ok := cache.GetWeight("users/a", "posts/b"); !ok {
				t.Fatal("bounded delete removed the second edge")
			}
			mutation := latestPrefixMutation(t, log)
			edges := mutation.GetOp().GetDeleteEdges().GetEdges()
			if len(edges) != 1 || edges[0].GetTail() != "users/a" || edges[0].GetHead() != "posts/a" {
				t.Fatalf("logged mutation = %v, want exact first edge", mutation)
			}
		})
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
