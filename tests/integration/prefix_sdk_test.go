package integration_test

import (
	"context"
	"net"
	"testing"
	"time"

	cachegraph "github.com/anaregdesign/lantern/core/cache/graph"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// newPrefixSDKClient wires the high-level SDK to an in-process server whose
// cache has the prefix index enabled. Mirrors newPrefixScanRaw but goes
// through *client.Lantern so we exercise the Phase 4 wrappers end-to-end.
func newPrefixSDKClient(t *testing.T) (*client.Lantern, func()) {
	t.Helper()

	lis := bufconn.Listen(1 << 16)
	gc := cachegraph.NewGraphCache[string, *pb.Vertex](time.Minute)
	gc.EnablePrefixIndex(func(s string) string { return s })

	srv := grpc.NewServer()
	pb.RegisterLanternServiceServer(srv, service.NewLanternService(gc))
	go func() { _ = srv.Serve(lis) }()

	dialer := func(context.Context, string) (net.Conn, error) { return lis.Dial() }
	l, err := client.NewLantern(
		"passthrough://bufconn",
		client.WithTransportCredentials(insecure.NewCredentials()),
		client.WithDialOption(grpc.WithContextDialer(dialer)),
	)
	if err != nil {
		t.Fatalf("NewLantern: %v", err)
	}
	cleanup := func() {
		_ = l.Close()
		srv.Stop()
		_ = lis.Close()
	}
	return l, cleanup
}

// seedPrefixVertices upserts keys with an explicit one-hour Expiration.
// Without an explicit Expiration, pb.Vertex defaults to the zero timestamp
// (1970-01-01), so the cache treats every vertex as born-expired:
// CountVerticesByPrefix would lie (radix-only) while ScanVertices returns
// nothing. See /memories/repo/lantern.md "GraphCache vertex seeding gotcha"
// for the full diagnosis.
func seedPrefixVertices(t *testing.T, ctx context.Context, l *client.Lantern, keys []string) {
	t.Helper()
	inputs := make([]client.VertexInput, len(keys))
	exp := time.Now().Add(time.Hour)
	for i, k := range keys {
		inputs[i] = client.VertexInput{Key: k, Value: k, Expiration: exp}
	}
	if err := l.PutVertices(ctx, inputs); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}
}

func TestSDK_CountVerticesByPrefix(t *testing.T) {
	l, cleanup := newPrefixSDKClient(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	seedPrefixVertices(t, ctx, l, []string{
		"users/1", "users/2", "users/3",
		"orders/1", "orders/2",
	})

	n, err := l.CountVerticesByPrefix(ctx, "users/")
	if err != nil {
		t.Fatalf("CountVerticesByPrefix: %v", err)
	}
	if n != 3 {
		t.Errorf("count users/ = %d, want 3", n)
	}
	n2, err := l.CountVerticesByPrefix(ctx, "missing/")
	if err != nil {
		t.Fatalf("CountVerticesByPrefix missing: %v", err)
	}
	if n2 != 0 {
		t.Errorf("count missing/ = %d, want 0", n2)
	}
}

func TestSDK_ScanVertices_SinglePage(t *testing.T) {
	l, cleanup := newPrefixSDKClient(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	seedPrefixVertices(t, ctx, l, []string{"a/1", "a/2", "a/3", "b/1"})

	vs, next, err := l.ScanVertices(ctx, "a/", client.WithScanLimit(10))
	if err != nil {
		t.Fatalf("ScanVertices: %v", err)
	}
	if len(vs) != 3 {
		t.Fatalf("got %d vertices, want 3", len(vs))
	}
	if len(next) != 0 {
		t.Errorf("expected empty next cursor (single page), got %x", next)
	}
	want := []string{"a/1", "a/2", "a/3"}
	for i, v := range vs {
		if v.GetKey() != want[i] {
			t.Errorf("vs[%d].Key = %q, want %q", i, v.GetKey(), want[i])
		}
	}
}

func TestSDK_ScanVertices_PaginatedCursor(t *testing.T) {
	l, cleanup := newPrefixSDKClient(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	seed := []string{"k/1", "k/2", "k/3", "k/4", "k/5"}
	seedPrefixVertices(t, ctx, l, seed)

	got := []string{}
	var cursor []byte
	pages := 0
	for {
		pages++
		if pages > 10 {
			t.Fatalf("pagination did not terminate: got %v", got)
		}
		vs, next, err := l.ScanVertices(ctx, "k/", client.WithScanLimit(2), client.WithScanCursor(cursor))
		if err != nil {
			t.Fatalf("ScanVertices page %d: %v", pages, err)
		}
		for _, v := range vs {
			got = append(got, v.GetKey())
		}
		if len(next) == 0 {
			break
		}
		cursor = next
	}
	if len(got) != len(seed) {
		t.Fatalf("got %v, want %v", got, seed)
	}
	for i := range seed {
		if got[i] != seed[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], seed[i])
		}
	}
}

func TestSDK_ScanVerticesAll_Iterator(t *testing.T) {
	l, cleanup := newPrefixSDKClient(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	seed := []string{"u/1", "u/2", "u/3", "u/4", "u/5", "u/6", "u/7"}
	seedPrefixVertices(t, ctx, l, seed)

	got := []string{}
	batches := 0
	for batch, err := range l.ScanVerticesAll(ctx, "u/", 3) {
		if err != nil {
			t.Fatalf("iterator yielded error: %v", err)
		}
		batches++
		for _, v := range batch {
			got = append(got, v.GetKey())
		}
	}
	if len(got) != len(seed) {
		t.Fatalf("iterator yielded %v, want %v", got, seed)
	}
	for i := range seed {
		if got[i] != seed[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], seed[i])
		}
	}
	if batches < 2 {
		t.Errorf("expected >=2 batches with batchSize=3 and 7 items, got %d", batches)
	}
}

func TestSDK_ScanVerticesAll_EarlyBreak(t *testing.T) {
	l, cleanup := newPrefixSDKClient(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	seedPrefixVertices(t, ctx, l, []string{"x/1", "x/2", "x/3", "x/4", "x/5"})

	seen := 0
	for batch, err := range l.ScanVerticesAll(ctx, "x/", 2) {
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		seen += len(batch)
		break // consumer aborts after first batch
	}
	if seen == 0 {
		t.Errorf("expected at least one vertex from first batch, got 0")
	}
}

func TestSDK_DeleteVerticesByPrefix_DryRunVsReal(t *testing.T) {
	l, cleanup := newPrefixSDKClient(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	seedPrefixVertices(t, ctx, l, []string{"d/1", "d/2", "d/3"})

	matched, err := l.DeleteVerticesByPrefix(ctx, "d/", client.WithDryRun())
	if err != nil {
		t.Fatalf("DeleteVerticesByPrefix dry: %v", err)
	}
	if matched != 3 {
		t.Errorf("dry matched = %d, want 3", matched)
	}
	if n, _ := l.CountVerticesByPrefix(ctx, "d/"); n != 3 {
		t.Errorf("count after dry run = %d, want 3 (no mutation)", n)
	}

	deleted, err := l.DeleteVerticesByPrefix(ctx, "d/")
	if err != nil {
		t.Fatalf("DeleteVerticesByPrefix real: %v", err)
	}
	if deleted != 3 {
		t.Errorf("deleted = %d, want 3", deleted)
	}
	if n, _ := l.CountVerticesByPrefix(ctx, "d/"); n != 0 {
		t.Errorf("count after real delete = %d, want 0", n)
	}
}
