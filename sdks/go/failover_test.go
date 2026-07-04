package client

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
)

// fakeNode is a failoverNode whose behaviour is driven by per-method
// function fields. Unset methods return zero values, so each test wires
// only the methods it exercises. It mirrors the fake used by the MCP
// failover tests before the logic moved into the SDK (#592).
type fakeNode struct {
	getVertexFn func(ctx context.Context, key string) (*Vertex, error)
	// addEdgeAtWithIDsFn / addEdgesWithIDsFn drive the id-accepting seams the
	// failover ring actually calls for additive writes (#916); the failover
	// AddEdge/AddEdgeAt/AddEdges methods route through these, so tests wire
	// them to observe the contrib ids passed down.
	addEdgeAtWithIDsFn func(ctx context.Context, tail, head string, weight float32, expiration time.Time, ids [][]byte) (float32, error)
	addEdgesWithIDsFn  func(ctx context.Context, inputs []EdgeInput, ids [][]byte) ([]float32, error)
	pingErr            error
	closed             int
}

func (f *fakeNode) PutVertex(context.Context, string, any, time.Duration) error { return nil }
func (f *fakeNode) PutVertexAt(context.Context, string, any, time.Time) error   { return nil }
func (f *fakeNode) PutVertices(context.Context, []VertexInput) error            { return nil }
func (f *fakeNode) PutVertexIfAbsent(context.Context, string, any, time.Duration) (bool, error) {
	return true, nil
}
func (f *fakeNode) PutVertexIfAbsentAt(context.Context, string, any, time.Time) (bool, error) {
	return true, nil
}
func (f *fakeNode) PutVerticesIfAbsent(context.Context, []VertexInput) (int, []string, error) {
	return 0, nil, nil
}
func (f *fakeNode) GetVertex(ctx context.Context, key string) (*Vertex, error) {
	if f.getVertexFn != nil {
		return f.getVertexFn(ctx, key)
	}
	return nil, nil
}
func (f *fakeNode) GetVertices(context.Context, []string) ([]*Vertex, []string, error) {
	return nil, nil, nil
}
func (f *fakeNode) DeleteVertex(context.Context, string) (bool, error)    { return false, nil }
func (f *fakeNode) DeleteVertices(context.Context, []string) (int, error) { return 0, nil }
func (f *fakeNode) ScanVertices(context.Context, string, ...ScanOption) ([]*Vertex, []byte, error) {
	return nil, nil, nil
}
func (f *fakeNode) ScanVertexKeys(context.Context, string, ...ScanOption) ([]string, []byte, error) {
	return nil, nil, nil
}
func (f *fakeNode) SearchVertices(context.Context, string, ...SearchOption) ([]SearchHit, error) {
	return nil, nil
}
func (f *fakeNode) CountVerticesByPrefix(context.Context, string) (uint64, error) { return 0, nil }
func (f *fakeNode) DeleteVerticesByPrefix(context.Context, string, ...DeleteByPrefixOption) (uint64, error) {
	return 0, nil
}
func (f *fakeNode) AddEdge(context.Context, string, string, float32, time.Duration) (float32, error) {
	return 0, nil
}
func (f *fakeNode) AddEdgeAt(context.Context, string, string, float32, time.Time) (float32, error) {
	return 0, nil
}
func (f *fakeNode) AddEdges(context.Context, []EdgeInput) ([]float32, error) { return nil, nil }
func (f *fakeNode) addEdgeAtWithIDs(ctx context.Context, tail, head string, weight float32, expiration time.Time, ids [][]byte) (float32, error) {
	if f.addEdgeAtWithIDsFn != nil {
		return f.addEdgeAtWithIDsFn(ctx, tail, head, weight, expiration, ids)
	}
	return 0, nil
}
func (f *fakeNode) addEdgesWithIDs(ctx context.Context, inputs []EdgeInput, ids [][]byte) ([]float32, error) {
	if f.addEdgesWithIDsFn != nil {
		return f.addEdgesWithIDsFn(ctx, inputs, ids)
	}
	return nil, nil
}
func (f *fakeNode) PutEdge(context.Context, string, string, float32, time.Duration) error {
	return nil
}
func (f *fakeNode) PutEdgeAt(context.Context, string, string, float32, time.Time) error { return nil }
func (f *fakeNode) PutEdges(context.Context, []EdgeInput) error                         { return nil }
func (f *fakeNode) GetEdge(context.Context, string, string) (*Edge, error)              { return nil, nil }
func (f *fakeNode) GetEdges(context.Context, []EdgeRef) ([]*Edge, []EdgeRef, error) {
	return nil, nil, nil
}
func (f *fakeNode) ScanEdges(context.Context, ...EdgeScanOption) ([]*Edge, []byte, error) {
	return nil, nil, nil
}
func (f *fakeNode) DeleteEdgesByPrefix(context.Context, ...DeleteEdgesByPrefixOption) (uint64, error) {
	return 0, nil
}
func (f *fakeNode) DeleteEdge(context.Context, string, string) (bool, error) { return false, nil }
func (f *fakeNode) DeleteEdges(context.Context, []EdgeRef) (int, error)      { return 0, nil }
func (f *fakeNode) Illuminate(context.Context, string, ...IlluminateOption) (*Graph, error) {
	return nil, nil
}
func (f *fakeNode) Ping(context.Context) error { return f.pingErr }
func (f *fakeNode) Close() error               { f.closed++; return nil }

// unavailableErr returns the error a real *Lantern endpoint surfaces when a
// node is unreachable (connection refused) or replies CodeUnavailable: the
// ErrUnavailable sentinel joined with the underlying connect error. The
// failover ring keys on errors.Is(err, ErrUnavailable).
func unavailableErr() error {
	return wrapConnectErr(connect.NewError(connect.CodeUnavailable, errors.New("node down")))
}

func TestWrapConnectErr_Unavailable(t *testing.T) {
	err := unavailableErr()
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("CodeUnavailable must join ErrUnavailable; got %v", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatal("CodeUnavailable must not match ErrNotFound")
	}
}

func TestNewLanternFailover_RejectsEmpty(t *testing.T) {
	if _, err := NewLanternFailover(nil); err == nil {
		t.Fatal("NewLanternFailover(nil) returned nil error")
	}
}

func TestNewLanternFailover_DialsAllAddrs(t *testing.T) {
	// NewLantern is lazy (no connection established until first RPC), so
	// this constructs two endpoints without a live server.
	f, err := NewLanternFailover([]string{"http://127.0.0.1:6380", "http://127.0.0.1:6381"})
	if err != nil {
		t.Fatalf("NewLanternFailover err = %v", err)
	}
	defer func() { _ = f.Close() }()
	if len(f.nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(f.nodes))
	}
}

func TestNewLanternFailover_RejectsBadAddr(t *testing.T) {
	if _, err := NewLanternFailover([]string{"http://ok:6380", "no-scheme"}); err == nil {
		t.Fatal("expected error for schemeless address")
	}
}

func TestFailover_RotatesOnUnavailableReadAndSticks(t *testing.T) {
	var n0, n1 int
	node0 := &fakeNode{getVertexFn: func(context.Context, string) (*Vertex, error) {
		n0++
		return nil, unavailableErr()
	}}
	node1 := &fakeNode{getVertexFn: func(_ context.Context, key string) (*Vertex, error) {
		n1++
		return &Vertex{Key: key}, nil
	}}
	f := &Failover{nodes: []failoverNode{node0, node1}}

	v, err := f.GetVertex(context.Background(), "k")
	if err != nil {
		t.Fatalf("GetVertex err = %v", err)
	}
	if v == nil || v.Key != "k" {
		t.Fatalf("GetVertex returned %+v, want vertex with key k", v)
	}
	if n0 != 1 || n1 != 1 {
		t.Fatalf("call counts n0=%d n1=%d, want 1 and 1", n0, n1)
	}

	// After rotating to node1 the wrapper is sticky: a second call starts
	// at node1 and never touches the known-dead node0.
	if _, err := f.GetVertex(context.Background(), "k2"); err != nil {
		t.Fatalf("second GetVertex err = %v", err)
	}
	if n0 != 1 {
		t.Fatalf("node0 retried after rotation: n0=%d, want 1", n0)
	}
	if n1 != 2 {
		t.Fatalf("node1 not sticky: n1=%d, want 2", n1)
	}
}

func TestFailover_DoesNotRotateOnAppError(t *testing.T) {
	var n0, n1 int
	node0 := &fakeNode{getVertexFn: func(context.Context, string) (*Vertex, error) {
		n0++
		return nil, ErrNotFound
	}}
	node1 := &fakeNode{getVertexFn: func(context.Context, string) (*Vertex, error) {
		n1++
		return nil, nil
	}}
	f := &Failover{nodes: []failoverNode{node0, node1}}

	_, err := f.GetVertex(context.Background(), "k")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if n0 != 1 || n1 != 0 {
		t.Fatalf("call counts n0=%d n1=%d, want 1 and 0 (no rotation on app error)", n0, n1)
	}
}

func TestFailover_AllNodesUnavailableReturnsError(t *testing.T) {
	mk := func(c *int) *fakeNode {
		return &fakeNode{getVertexFn: func(context.Context, string) (*Vertex, error) {
			*c++
			return nil, unavailableErr()
		}}
	}
	var a, b, c int
	f := &Failover{nodes: []failoverNode{mk(&a), mk(&b), mk(&c)}}

	_, err := f.GetVertex(context.Background(), "k")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if a != 1 || b != 1 || c != 1 {
		t.Fatalf("each node should be tried exactly once: a=%d b=%d c=%d", a, b, c)
	}
}

func TestFailover_PingTriesAllNodesUntilHealthy(t *testing.T) {
	node0 := &fakeNode{pingErr: errors.New("connection refused")}
	node1 := &fakeNode{pingErr: nil}
	f := &Failover{nodes: []failoverNode{node0, node1}}

	if err := f.Ping(context.Background()); err != nil {
		t.Fatalf("Ping err = %v, want nil (node1 healthy)", err)
	}

	// Ping should leave the wrapper sticky to the healthy node1, so a
	// subsequent data call starts there.
	var n0, n1 int
	node0.getVertexFn = func(context.Context, string) (*Vertex, error) { n0++; return nil, nil }
	node1.getVertexFn = func(context.Context, string) (*Vertex, error) { n1++; return nil, nil }
	if _, err := f.GetVertex(context.Background(), "k"); err != nil {
		t.Fatalf("GetVertex err = %v", err)
	}
	if n0 != 0 || n1 != 1 {
		t.Fatalf("after Ping stickiness n0=%d n1=%d, want 0 and 1", n0, n1)
	}
}

func TestFailover_PingAllNodesFailReturnsError(t *testing.T) {
	f := &Failover{nodes: []failoverNode{
		&fakeNode{pingErr: errors.New("down0")},
		&fakeNode{pingErr: errors.New("down1")},
	}}
	if err := f.Ping(context.Background()); err == nil {
		t.Fatal("Ping returned nil, want error when all nodes are down")
	}
}

func TestFailover_AdditiveWriteRotatesOnUnavailable(t *testing.T) {
	var n0, n1 int
	node0 := &fakeNode{addEdgeAtWithIDsFn: func(context.Context, string, string, float32, time.Time, [][]byte) (float32, error) {
		n0++
		return 0, unavailableErr()
	}}
	node1 := &fakeNode{addEdgeAtWithIDsFn: func(context.Context, string, string, float32, time.Time, [][]byte) (float32, error) {
		n1++
		return 0, nil
	}}
	f := &Failover{nodes: []failoverNode{node0, node1}}

	if _, err := f.AddEdge(context.Background(), "a", "b", 1.0, time.Minute); err != nil {
		t.Fatalf("AddEdge err = %v", err)
	}
	if n0 != 1 || n1 != 1 {
		t.Fatalf("call counts n0=%d n1=%d, want 1 and 1 (rotate on unavailable)", n0, n1)
	}
}

func TestFailover_CloseClosesAllNodes(t *testing.T) {
	node0 := &fakeNode{}
	node1 := &fakeNode{}
	f := &Failover{nodes: []failoverNode{node0, node1}}
	if err := f.Close(); err != nil {
		t.Fatalf("Close err = %v", err)
	}
	if node0.closed != 1 || node1.closed != 1 {
		t.Fatalf("close counts n0=%d n1=%d, want 1 and 1", node0.closed, node1.closed)
	}
}

// testRetryPolicy is a normalised policy whose backoff sleeps are instant so
// the failover retry tests never wait real time. noSleep lives in retry_test.go
// (same package).
func testRetryPolicy(maxAttempts int) *RetryPolicy {
	p := RetryPolicy{MaxAttempts: maxAttempts, sleepFn: noSleep}.normalized()
	return &p
}

func TestFailover_RetryRecoversAcrossRingWalks(t *testing.T) {
	var n0, n1 int
	node0 := &fakeNode{getVertexFn: func(context.Context, string) (*Vertex, error) {
		n0++
		return nil, unavailableErr()
	}}
	node1 := &fakeNode{getVertexFn: func(_ context.Context, key string) (*Vertex, error) {
		n1++
		if n1 < 2 { // dead on the first ring walk, healthy on the second
			return nil, unavailableErr()
		}
		return &Vertex{Key: key}, nil
	}}
	f := &Failover{nodes: []failoverNode{node0, node1}, retry: testRetryPolicy(3)}

	v, err := f.GetVertex(context.Background(), "k")
	if err != nil {
		t.Fatalf("GetVertex err = %v, want nil (retry recovers)", err)
	}
	if v == nil || v.Key != "k" {
		t.Fatalf("GetVertex = %+v, want vertex key k", v)
	}
	// Ring walk 1: node0 + node1 both Unavailable → backoff. Ring walk 2:
	// node0 Unavailable, node1 succeeds. Each node is hit once per walk.
	if n0 != 2 || n1 != 2 {
		t.Fatalf("counts n0=%d n1=%d, want 2 and 2", n0, n1)
	}
}

func TestFailover_NoRetryFailsOnFirstRingWalk(t *testing.T) {
	var n0, n1 int
	node0 := &fakeNode{getVertexFn: func(context.Context, string) (*Vertex, error) {
		n0++
		return nil, unavailableErr()
	}}
	node1 := &fakeNode{getVertexFn: func(context.Context, string) (*Vertex, error) {
		n1++
		return nil, unavailableErr()
	}}
	// No retry policy: zero-config failover behaves exactly as before — one
	// ring walk, then surface the error.
	f := &Failover{nodes: []failoverNode{node0, node1}}

	if _, err := f.GetVertex(context.Background(), "k"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if n0 != 1 || n1 != 1 {
		t.Fatalf("counts n0=%d n1=%d, want 1 and 1 (no retry)", n0, n1)
	}
}

func TestFailover_RetryAlwaysReadIgnoresIdempotencySetting(t *testing.T) {
	mk := func(c *int) *fakeNode {
		return &fakeNode{getVertexFn: func(context.Context, string) (*Vertex, error) {
			*c++
			return nil, unavailableErr()
		}}
	}
	var a, b int
	// idempotentAdds=false must NOT gate a retryAlways read.
	f := &Failover{nodes: []failoverNode{mk(&a), mk(&b)}, retry: testRetryPolicy(3), idempotentAdds: false}

	if _, err := f.GetVertex(context.Background(), "k"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	// 3 ring walks × 1 hit per node each.
	if a != 3 || b != 3 {
		t.Fatalf("counts a=%d b=%d, want 3 and 3 (MaxAttempts ring walks)", a, b)
	}
}

func TestFailover_RetryGatesAdditiveWritesByIdempotency(t *testing.T) {
	// Failover.AddEdge routes through AddEdgeAt → node.addEdgeAtWithIDs, so the
	// counting fakes wire the addEdgeAtWithIDs seam.
	mk := func(c *int) *fakeNode {
		return &fakeNode{addEdgeAtWithIDsFn: func(context.Context, string, string, float32, time.Time, [][]byte) (float32, error) {
			*c++
			return 0, unavailableErr()
		}}
	}

	t.Run("without idempotent adds a single ring walk, no backoff-retry", func(t *testing.T) {
		var a, b int
		f := &Failover{nodes: []failoverNode{mk(&a), mk(&b)}, retry: testRetryPolicy(3), idempotentAdds: false}
		if _, err := f.AddEdge(context.Background(), "a", "b", 1, time.Minute); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable", err)
		}
		if a != 1 || b != 1 {
			t.Fatalf("counts a=%d b=%d, want 1 and 1 (additive write not retried)", a, b)
		}
	})

	t.Run("with idempotent adds the ring walk repeats MaxAttempts times", func(t *testing.T) {
		var a, b int
		f := &Failover{nodes: []failoverNode{mk(&a), mk(&b)}, retry: testRetryPolicy(3), idempotentAdds: true, contribIDs: &contribIDGen{}}
		if _, err := f.AddEdge(context.Background(), "a", "b", 1, time.Minute); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable", err)
		}
		if a != 3 || b != 3 {
			t.Fatalf("counts a=%d b=%d, want 3 and 3 (retry armed for idempotent adds)", a, b)
		}
	})

	t.Run("every retry attempt carries the SAME contrib ids", func(t *testing.T) {
		// got collects the ids observed per attempt, across BOTH ring nodes —
		// so this also pins id reuse across a node switch (a re-mint through
		// node B double-counts exactly like a re-mint through node A: the two
		// replicas converge via replication).
		var got [][][]byte
		record := func() *fakeNode {
			return &fakeNode{addEdgeAtWithIDsFn: func(_ context.Context, _, _ string, _ float32, _ time.Time, ids [][]byte) (float32, error) {
				cp := make([][]byte, len(ids))
				for i, id := range ids {
					cp[i] = append([]byte(nil), id...)
				}
				got = append(got, cp)
				return 0, unavailableErr()
			}}
		}
		f := &Failover{
			nodes:          []failoverNode{record(), record()},
			retry:          testRetryPolicy(3),
			idempotentAdds: true,
			contribIDs:     &contribIDGen{},
		}
		if _, err := f.AddEdge(context.Background(), "a", "b", 1, time.Minute); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable", err)
		}
		if len(got) < 4 {
			t.Fatalf("attempts observed = %d, want >= 4 (retry × ring walk must have fired)", len(got))
		}
		first := got[0]
		if len(first) != 1 || len(first[0]) != 24 {
			t.Fatalf("attempt 0 ids = %v, want exactly one 24-byte id", first)
		}
		for i, ids := range got[1:] {
			if len(ids) != 1 || !bytes.Equal(ids[0], first[0]) {
				t.Fatalf("attempt %d re-minted its contrib id:\n  got  %x\n  want %x\n(a fresh id per attempt is exactly the double-count bug)", i+1, ids, first[0])
			}
		}
	})

	t.Run("AddEdges pre-mints one id per input, index-aligned, reused across attempts", func(t *testing.T) {
		var calls [][][]byte
		fake := &fakeNode{addEdgesWithIDsFn: func(_ context.Context, inputs []EdgeInput, ids [][]byte) ([]float32, error) {
			if len(ids) != len(inputs) {
				t.Fatalf("ids/inputs misaligned: %d ids for %d inputs", len(ids), len(inputs))
			}
			cp := make([][]byte, len(ids))
			for i, id := range ids {
				cp[i] = append([]byte(nil), id...)
			}
			calls = append(calls, cp)
			return nil, unavailableErr()
		}}
		f := &Failover{nodes: []failoverNode{fake}, retry: testRetryPolicy(2), idempotentAdds: true, contribIDs: &contribIDGen{}}
		inputs := []EdgeInput{
			{Tail: "a", Head: "x", Weight: 1, Expiration: time.Now().Add(time.Minute)},
			{Tail: "a", Head: "y", Weight: 1, Expiration: time.Now().Add(time.Minute)},
			{Tail: "a", Head: "z", Weight: 1, Expiration: time.Now().Add(time.Minute)},
		}
		if _, err := f.AddEdges(context.Background(), inputs); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable", err)
		}
		if len(calls) < 2 {
			t.Fatalf("attempts = %d, want >= 2", len(calls))
		}
		for atI := 1; atI < len(calls); atI++ {
			for i := range calls[0] {
				if !bytes.Equal(calls[atI][i], calls[0][i]) {
					t.Fatalf("attempt %d id[%d] differs from attempt 0 — batch retry re-minted", atI, i)
				}
			}
		}
		// Distinctness within one call still holds (per-index packing).
		if bytes.Equal(calls[0][0], calls[0][1]) || bytes.Equal(calls[0][1], calls[0][2]) {
			t.Fatal("ids within one batch must be pairwise distinct")
		}
	})
}

func TestFailover_RetryStopsOnNonUnavailable(t *testing.T) {
	var n0, n1 int
	node0 := &fakeNode{getVertexFn: func(context.Context, string) (*Vertex, error) {
		n0++
		return nil, ErrNotFound // deterministic: neither rotate nor retry
	}}
	node1 := &fakeNode{getVertexFn: func(context.Context, string) (*Vertex, error) {
		n1++
		return nil, nil
	}}
	f := &Failover{nodes: []failoverNode{node0, node1}, retry: testRetryPolicy(5)}

	if _, err := f.GetVertex(context.Background(), "k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if n0 != 1 || n1 != 0 {
		t.Fatalf("counts n0=%d n1=%d, want 1 and 0 (deterministic error, no retry/rotation)", n0, n1)
	}
}

func TestNewLanternFailover_RetryExtractedAndNodesNeutralised(t *testing.T) {
	f, err := NewLanternFailover(
		[]string{"http://127.0.0.1:6380", "http://127.0.0.1:6381"},
		WithRetry(RetryPolicy{MaxAttempts: 4}),
		WithIdempotentAdds(),
	)
	if err != nil {
		t.Fatalf("NewLanternFailover err = %v", err)
	}
	defer func() { _ = f.Close() }()

	if f.retry == nil || f.retry.MaxAttempts != 4 {
		t.Fatalf("failover retry policy = %+v, want MaxAttempts 4", f.retry)
	}
	if !f.idempotentAdds {
		t.Fatal("failover idempotentAdds not extracted from opts")
	}
	// The failover-level ContribID generator must be armed when idempotent
	// adds are on, so ids are minted once per call and reused across retries
	// and node switches (#916). Without it every attempt would re-mint and
	// double-count.
	if f.contribIDs == nil {
		t.Fatal("failover contribIDs generator not seeded under WithIdempotentAdds")
	}
	for i, n := range f.nodes {
		l, ok := n.(*Lantern)
		if !ok {
			t.Fatalf("node %d is %T, want *Lantern", i, n)
		}
		// clearNodeRetry must neutralise per-node retry so the failover loop
		// is the sole retry driver (no nested MaxAttempts² backoff)...
		if l.opts.retry != nil {
			t.Fatalf("node %d retry not stripped: %+v", i, l.opts.retry)
		}
		// ...while idempotent-adds still flows to the nodes so AddEdges stamps
		// ContribIDs on every attempt.
		if !l.opts.idempotentAdds {
			t.Fatalf("node %d idempotentAdds not propagated", i)
		}
	}
}
