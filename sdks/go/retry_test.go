package client

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

// noSleep is a RetryPolicy.sleepFn seam that returns instantly so tests never
// wait real backoff. It still honours a cancelled ctx so ctx-abort tests stay
// meaningful.
func noSleep(ctx context.Context, _ time.Duration) error { return ctx.Err() }

// fixedRand returns a RetryPolicy.randFn seam that always yields v — used to
// pin the full-jitter draw to a known point in [0,1).
func fixedRand(v float64) func() float64 { return func() float64 { return v } }

func TestRetryPolicy_Normalized(t *testing.T) {
	t.Run("zero value gets defaults", func(t *testing.T) {
		p := RetryPolicy{}.normalized()
		if p.MaxAttempts != 3 {
			t.Errorf("MaxAttempts = %d, want 3", p.MaxAttempts)
		}
		if p.BaseDelay != 100*time.Millisecond {
			t.Errorf("BaseDelay = %v, want 100ms", p.BaseDelay)
		}
		if p.MaxDelay != 2*time.Second {
			t.Errorf("MaxDelay = %v, want 2s", p.MaxDelay)
		}
		if len(p.RetryableCodes) != 1 || p.RetryableCodes[0] != connect.CodeUnavailable {
			t.Errorf("RetryableCodes = %v, want {Unavailable}", p.RetryableCodes)
		}
		if p.sleepFn == nil || p.randFn == nil {
			t.Error("normalized must install default sleepFn/randFn")
		}
	})

	t.Run("negative MaxAttempts normalises to 3", func(t *testing.T) {
		if got := (RetryPolicy{MaxAttempts: -5}).normalized().MaxAttempts; got != 3 {
			t.Errorf("MaxAttempts = %d, want 3", got)
		}
	})

	t.Run("explicit values are preserved", func(t *testing.T) {
		p := RetryPolicy{
			MaxAttempts:    7,
			BaseDelay:      5 * time.Millisecond,
			MaxDelay:       time.Minute,
			RetryableCodes: []connect.Code{connect.CodeResourceExhausted},
		}.normalized()
		if p.MaxAttempts != 7 || p.BaseDelay != 5*time.Millisecond || p.MaxDelay != time.Minute {
			t.Errorf("explicit knobs mutated: %+v", p)
		}
		if len(p.RetryableCodes) != 1 || p.RetryableCodes[0] != connect.CodeResourceExhausted {
			t.Errorf("RetryableCodes = %v, want {ResourceExhausted}", p.RetryableCodes)
		}
	})
}

func TestRetryPolicy_RetryableErr(t *testing.T) {
	def := RetryPolicy{}.normalized()
	tests := []struct {
		name string
		p    RetryPolicy
		err  error
		want bool
	}{
		{"nil is never retryable", def, nil, false},
		{"unavailable retryable by default", def, connect.NewError(connect.CodeUnavailable, errors.New("x")), true},
		{"wrapped unavailable retryable", def, wrapConnectErr(connect.NewError(connect.CodeUnavailable, errors.New("x"))), true},
		{"notfound not retryable", def, connect.NewError(connect.CodeNotFound, errors.New("x")), false},
		{"invalid argument not retryable", def, connect.NewError(connect.CodeInvalidArgument, errors.New("x")), false},
		{"deadline exceeded never retryable", def, connect.NewError(connect.CodeDeadlineExceeded, errors.New("x")), false},
		{"canceled never retryable", def, connect.NewError(connect.CodeCanceled, errors.New("x")), false},
		{"resource exhausted opt-out by default", def, connect.NewError(connect.CodeResourceExhausted, errors.New("x")), false},
		{
			"resource exhausted opt-in",
			RetryPolicy{RetryableCodes: []connect.Code{connect.CodeUnavailable, connect.CodeResourceExhausted}}.normalized(),
			connect.NewError(connect.CodeResourceExhausted, errors.New("x")),
			true,
		},
		{
			"deadline stays excluded even if listed",
			RetryPolicy{RetryableCodes: []connect.Code{connect.CodeDeadlineExceeded}}.normalized(),
			connect.NewError(connect.CodeDeadlineExceeded, errors.New("x")),
			false,
		},
		{"plain error not retryable", def, errors.New("not a connect error"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.retryableErr(tt.err); got != tt.want {
				t.Errorf("retryableErr(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestRetryPolicy_Delay(t *testing.T) {
	base := 100 * time.Millisecond
	max := 2 * time.Second

	t.Run("full-jitter draws in (0, ceil]", func(t *testing.T) {
		// randFn=1 lands on the ceiling; randFn=0 lands on 0. The ceiling
		// is BaseDelay<<attempt until it saturates at MaxDelay.
		wantCeil := []time.Duration{base, 2 * base, 4 * base, 8 * base, 16 * base}
		for attempt, ceil := range wantCeil {
			p := RetryPolicy{BaseDelay: base, MaxDelay: max, randFn: fixedRand(1)}.normalized()
			if got := p.delay(attempt); got != ceil {
				t.Errorf("delay(%d) at rand=1 = %v, want ceil %v", attempt, got, ceil)
			}
			pz := RetryPolicy{BaseDelay: base, MaxDelay: max, randFn: fixedRand(0)}.normalized()
			if got := pz.delay(attempt); got != 0 {
				t.Errorf("delay(%d) at rand=0 = %v, want 0", attempt, got)
			}
		}
	})

	t.Run("ceiling saturates at MaxDelay", func(t *testing.T) {
		p := RetryPolicy{BaseDelay: base, MaxDelay: max, randFn: fixedRand(1)}.normalized()
		// 100ms<<5 = 3.2s > 2s cap.
		if got := p.delay(5); got != max {
			t.Errorf("delay(5) = %v, want capped %v", got, max)
		}
	})

	t.Run("huge attempt overflow guards to MaxDelay", func(t *testing.T) {
		p := RetryPolicy{BaseDelay: base, MaxDelay: max, randFn: fixedRand(1)}.normalized()
		if got := p.delay(100); got != max {
			t.Errorf("delay(100) overflow = %v, want %v", got, max)
		}
	})
}

func TestRetryPolicy_Run(t *testing.T) {
	unavailable := func() error { return connect.NewError(connect.CodeUnavailable, errors.New("down")) }

	t.Run("succeeds on first attempt", func(t *testing.T) {
		p := RetryPolicy{MaxAttempts: 3, sleepFn: noSleep, randFn: fixedRand(0)}.normalized()
		calls := 0
		err := p.run(context.Background(), func() error { calls++; return nil })
		if err != nil || calls != 1 {
			t.Fatalf("calls=%d err=%v, want 1/nil", calls, err)
		}
	})

	t.Run("fail-N-then-succeed retries then wins", func(t *testing.T) {
		p := RetryPolicy{MaxAttempts: 4, sleepFn: noSleep, randFn: fixedRand(0)}.normalized()
		calls := 0
		err := p.run(context.Background(), func() error {
			calls++
			if calls < 3 {
				return unavailable()
			}
			return nil
		})
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if calls != 3 {
			t.Fatalf("calls = %d, want 3 (2 failures + success)", calls)
		}
	})

	t.Run("always-fail exhausts MaxAttempts and returns last error", func(t *testing.T) {
		p := RetryPolicy{MaxAttempts: 3, sleepFn: noSleep, randFn: fixedRand(0)}.normalized()
		calls := 0
		err := p.run(context.Background(), func() error { calls++; return unavailable() })
		if calls != 3 {
			t.Fatalf("calls = %d, want 3", calls)
		}
		if connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatalf("final err code = %v, want Unavailable", connect.CodeOf(err))
		}
	})

	t.Run("non-retryable error stops immediately", func(t *testing.T) {
		p := RetryPolicy{MaxAttempts: 5, sleepFn: noSleep, randFn: fixedRand(0)}.normalized()
		calls := 0
		err := p.run(context.Background(), func() error {
			calls++
			return connect.NewError(connect.CodeNotFound, errors.New("nope"))
		})
		if calls != 1 {
			t.Fatalf("calls = %d, want 1 (no retry on NotFound)", calls)
		}
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("err code = %v, want NotFound", connect.CodeOf(err))
		}
	})

	t.Run("ctx cancel mid-backoff aborts and joins ctx error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		sleepCalls := 0
		p := RetryPolicy{
			MaxAttempts: 5,
			randFn:      fixedRand(1),
			sleepFn: func(c context.Context, _ time.Duration) error {
				sleepCalls++
				cancel()
				return c.Err()
			},
		}.normalized()
		calls := 0
		err := p.run(ctx, func() error { calls++; return unavailable() })
		if calls != 1 || sleepCalls != 1 {
			t.Fatalf("calls=%d sleepCalls=%d, want 1/1 (abort during first backoff)", calls, sleepCalls)
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want joined context.Canceled", err)
		}
		if connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatalf("err must retain the last attempt's Unavailable; code = %v", connect.CodeOf(err))
		}
	})
}

func TestCtxSleep(t *testing.T) {
	t.Run("non-positive duration returns immediately", func(t *testing.T) {
		if err := ctxSleep(context.Background(), 0); err != nil {
			t.Fatalf("ctxSleep(bg, 0) = %v, want nil", err)
		}
		if err := ctxSleep(context.Background(), -time.Second); err != nil {
			t.Fatalf("ctxSleep(bg, -1s) = %v, want nil", err)
		}
	})

	t.Run("positive duration elapses", func(t *testing.T) {
		if err := ctxSleep(context.Background(), time.Millisecond); err != nil {
			t.Fatalf("ctxSleep(bg, 1ms) = %v, want nil", err)
		}
	})

	t.Run("cancelled ctx aborts before the timer fires", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := ctxSleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
			t.Fatalf("ctxSleep(cancelled, 1h) = %v, want context.Canceled", err)
		}
	})
}

func TestRequestRetryable(t *testing.T) {
	contrib := []byte("000000000000000000000001") // 24-byte canonical id shape

	t.Run("reads and idempotent writes are retryable", func(t *testing.T) {
		reqs := []any{
			&pb.GetVertexRequest{}, &pb.GetVerticesRequest{}, &pb.GetEdgeRequest{}, &pb.GetEdgesRequest{},
			&pb.PutVertexRequest{}, &pb.PutVerticesRequest{}, &pb.PutEdgeRequest{}, &pb.PutEdgesRequest{},
			&pb.DeleteVertexRequest{}, &pb.DeleteVerticesRequest{}, &pb.DeleteEdgeRequest{}, &pb.DeleteEdgesRequest{},
			&pb.DeleteVerticesByPrefixRequest{}, &pb.ScanVerticesRequest{}, &pb.ScanVertexKeysRequest{},
			&pb.ScanEdgesRequest{}, &pb.CountVerticesByPrefixRequest{}, &pb.SearchVerticesRequest{},
			&pb.IlluminateRequest{}, &pb.GetServerStatusRequest{}, &pb.GetReplicationStatusRequest{},
		}
		for _, r := range reqs {
			if !requestRetryable(r) {
				t.Errorf("requestRetryable(%T) = false, want true", r)
			}
		}
	})

	t.Run("unknown request type fails closed", func(t *testing.T) {
		if requestRetryable(&pb.SubscribeRequest{}) {
			t.Error("streaming SubscribeRequest must not be retryable")
		}
		if requestRetryable("not a proto") {
			t.Error("unclassified type must not be retryable")
		}
	})

	t.Run("AddEdge retryable only with a contrib id", func(t *testing.T) {
		if requestRetryable(&pb.AddEdgeRequest{}) {
			t.Error("AddEdge without contrib_id must not be retryable")
		}
		if !requestRetryable(&pb.AddEdgeRequest{ContribId: contrib}) {
			t.Error("AddEdge with contrib_id must be retryable")
		}
	})

	t.Run("AddEdges retryable only when every edge carries a contrib id", func(t *testing.T) {
		edges := []*pb.Edge{{Tail: "a", Head: "b"}, {Tail: "c", Head: "d"}}
		cases := []struct {
			name string
			req  *pb.AddEdgesRequest
			want bool
		}{
			{"no ids", &pb.AddEdgesRequest{Edges: edges}, false},
			{"count mismatch", &pb.AddEdgesRequest{Edges: edges, ContribIds: [][]byte{contrib}}, false},
			{"one empty id", &pb.AddEdgesRequest{Edges: edges, ContribIds: [][]byte{contrib, {}}}, false},
			{"all ids present", &pb.AddEdgesRequest{Edges: edges, ContribIds: [][]byte{contrib, contrib}}, true},
			{"empty edges", &pb.AddEdgesRequest{}, false},
		}
		for _, tc := range cases {
			if got := requestRetryable(tc.req); got != tc.want {
				t.Errorf("%s: requestRetryable = %v, want %v", tc.name, got, tc.want)
			}
		}
	})
}

func TestRetryableMethod(t *testing.T) {
	cases := []struct {
		method         string
		idempotentAdds bool
		want           bool
	}{
		{"GetVertex", false, true},
		{"PutVertices", false, true},
		{"DeleteEdges", false, true},
		{"AddEdges", false, false},  // additive write, no idempotency
		{"AddEdges", true, true},    // idempotency armed
		{"AddEdge", true, true},     // idempotency armed
		{"AddEdgeAt", false, false}, // additive write, no idempotency
		{"Subscribe", true, false},  // streaming never retries
		{"Backup", true, false},     // io stream never retries
		{"NotAMethod", true, false}, // unknown fails closed
	}
	for _, c := range cases {
		if got := retryableMethod(c.method, c.idempotentAdds); got != c.want {
			t.Errorf("retryableMethod(%q, idempotent=%v) = %v, want %v", c.method, c.idempotentAdds, got, c.want)
		}
	}
}

// TestRetryEligibilityMatrix_CoversEveryRPC is the forced-decision guard: it
// walks every exported context-first method on *Lantern and fails when one is
// missing from methodRetryClasses, and conversely fails on any phantom map key
// that is not a real method. A new RPC (or a rename) cannot land without a
// deliberate retry classification.
func TestRetryEligibilityMatrix_CoversEveryRPC(t *testing.T) {
	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()
	rt := reflect.TypeOf((*Lantern)(nil))

	rpcMethods := map[string]bool{}
	for i := 0; i < rt.NumMethod(); i++ {
		m := rt.Method(i)
		// RPC-shaped: first parameter after the receiver is context.Context.
		// Close() (no ctx) and any non-ctx method are excluded.
		if m.Type.NumIn() < 2 || m.Type.In(1) != ctxType {
			continue
		}
		rpcMethods[m.Name] = true
		if _, ok := methodRetryClasses[m.Name]; !ok {
			t.Errorf("*Lantern.%s has no methodRetryClasses entry (forced-decision rule)", m.Name)
		}
	}

	for name := range methodRetryClasses {
		if !rpcMethods[name] {
			t.Errorf("methodRetryClasses has phantom key %q with no matching *Lantern RPC method", name)
		}
	}
}

// scriptedClient is a fake LanternServiceClient that scripts per-method
// success/failure sequences and records the requests it receives, so the
// single-endpoint unary retry path can be exercised without a live server.
// It embeds the interface (left nil) to satisfy the unused surface.
type scriptedClient struct {
	graphv1connect.LanternServiceClient

	getVertexErrs []error
	getVertexN    int

	addEdgesErrs []error
	addEdgesN    int
	addEdgesReqs []*pb.AddEdgesRequest
}

func (c *scriptedClient) GetVertex(_ context.Context, req *connect.Request[pb.GetVertexRequest]) (*connect.Response[pb.GetVertexResponse], error) {
	i := c.getVertexN
	c.getVertexN++
	if i < len(c.getVertexErrs) && c.getVertexErrs[i] != nil {
		return nil, c.getVertexErrs[i]
	}
	return connect.NewResponse(&pb.GetVertexResponse{Vertex: &pb.Vertex{Key: req.Msg.GetKey()}}), nil
}

func (c *scriptedClient) AddEdges(_ context.Context, req *connect.Request[pb.AddEdgesRequest]) (*connect.Response[pb.AddEdgesResponse], error) {
	c.addEdgesReqs = append(c.addEdgesReqs, req.Msg)
	i := c.addEdgesN
	c.addEdgesN++
	if i < len(c.addEdgesErrs) && c.addEdgesErrs[i] != nil {
		return nil, c.addEdgesErrs[i]
	}
	return connect.NewResponse(&pb.AddEdgesResponse{}), nil
}

func TestUnaryRetry_SingleEndpoint(t *testing.T) {
	unavailable := connect.NewError(connect.CodeUnavailable, errors.New("down"))

	t.Run("retries a read until it succeeds", func(t *testing.T) {
		l := mustLantern(t, WithRetry(RetryPolicy{MaxAttempts: 3, sleepFn: noSleep}))
		capt := &scriptedClient{getVertexErrs: []error{unavailable, unavailable, nil}}
		l.client = capt
		v, err := l.GetVertex(context.Background(), "k")
		if err != nil {
			t.Fatalf("GetVertex: %v", err)
		}
		if v.GetKey() != "k" {
			t.Fatalf("vertex key = %q, want k", v.GetKey())
		}
		if capt.getVertexN != 3 {
			t.Fatalf("attempts = %d, want 3", capt.getVertexN)
		}
	})

	t.Run("exhausts attempts then surfaces Unavailable", func(t *testing.T) {
		l := mustLantern(t, WithRetry(RetryPolicy{MaxAttempts: 2, sleepFn: noSleep}))
		capt := &scriptedClient{getVertexErrs: []error{unavailable, unavailable}}
		l.client = capt
		_, err := l.GetVertex(context.Background(), "k")
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable", err)
		}
		if capt.getVertexN != 2 {
			t.Fatalf("attempts = %d, want 2", capt.getVertexN)
		}
	})

	t.Run("does not retry a deterministic error", func(t *testing.T) {
		l := mustLantern(t, WithRetry(RetryPolicy{MaxAttempts: 5, sleepFn: noSleep}))
		capt := &scriptedClient{getVertexErrs: []error{connect.NewError(connect.CodeNotFound, errors.New("gone"))}}
		l.client = capt
		_, err := l.GetVertex(context.Background(), "k")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if capt.getVertexN != 1 {
			t.Fatalf("attempts = %d, want 1 (NotFound is deterministic)", capt.getVertexN)
		}
	})

	t.Run("zero-config client never retries", func(t *testing.T) {
		l := mustLantern(t) // no WithRetry
		capt := &scriptedClient{getVertexErrs: []error{unavailable}}
		l.client = capt
		_, err := l.GetVertex(context.Background(), "k")
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable", err)
		}
		if capt.getVertexN != 1 {
			t.Fatalf("attempts = %d, want 1 (retry not armed)", capt.getVertexN)
		}
	})

	t.Run("idempotent AddEdges retries with identical ContribIDs", func(t *testing.T) {
		l := mustLantern(t, WithIdempotentAdds(), WithRetry(RetryPolicy{MaxAttempts: 3, sleepFn: noSleep}))
		capt := &scriptedClient{addEdgesErrs: []error{unavailable, nil}}
		l.client = capt
		if _, err := l.AddEdges(context.Background(), []EdgeInput{{Tail: "a", Head: "b", Weight: 1}}); err != nil {
			t.Fatalf("AddEdges: %v", err)
		}
		if capt.addEdgesN != 2 {
			t.Fatalf("attempts = %d, want 2", capt.addEdgesN)
		}
		if len(capt.addEdgesReqs) != 2 {
			t.Fatalf("recorded %d requests, want 2", len(capt.addEdgesReqs))
		}
		first, second := capt.addEdgesReqs[0].GetContribIds(), capt.addEdgesReqs[1].GetContribIds()
		if len(first) != 1 || len(second) != 1 {
			t.Fatalf("contrib id counts = %d,%d, want 1,1", len(first), len(second))
		}
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("retried request must carry identical ContribIDs; got %x vs %x", first, second)
		}
	})

	t.Run("non-idempotent AddEdges is never retried", func(t *testing.T) {
		l := mustLantern(t, WithRetry(RetryPolicy{MaxAttempts: 5, sleepFn: noSleep})) // no WithIdempotentAdds
		capt := &scriptedClient{addEdgesErrs: []error{unavailable, nil}}
		l.client = capt
		_, err := l.AddEdges(context.Background(), []EdgeInput{{Tail: "a", Head: "b", Weight: 1}})
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable (additive write not retried)", err)
		}
		if capt.addEdgesN != 1 {
			t.Fatalf("attempts = %d, want 1 (double-count hazard blocks retry)", capt.addEdgesN)
		}
	})
}
