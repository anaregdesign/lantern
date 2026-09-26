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
	getVertexFn              func(ctx context.Context, key string) (*Vertex, error)
	putVertexAtFn            func(ctx context.Context, key string, value any, expiration time.Time) (PutOutcome, error)
	putVertexIfAbsentAtFn    func(ctx context.Context, key string, value any, expiration time.Time) (PutOutcome, error)
	putVerticesFn            func(ctx context.Context, inputs []VertexInput) ([]VertexPutResult, error)
	putVerticesIfAbsentFn    func(ctx context.Context, inputs []VertexInput) ([]VertexPutResult, error)
	deleteVertexFn           func(ctx context.Context, key string) (bool, error)
	deleteVerticesFn         func(ctx context.Context, keys []string) (int, error)
	deleteEdgeFn             func(ctx context.Context, tail, head string) (bool, error)
	deleteEdgesFn            func(ctx context.Context, refs []EdgeRef) (int, error)
	deleteVerticesByPrefixFn func(ctx context.Context, prefix string, opts ...DeleteByPrefixOption) (uint64, error)
	deleteEdgesByPrefixFn    func(ctx context.Context, opts ...DeleteEdgesByPrefixOption) (uint64, error)
	putEdgeAtFn              func(ctx context.Context, tail, head string, weight float32, expiration time.Time) (PutOutcome, error)
	putEdgesFn               func(ctx context.Context, inputs []EdgeInput) ([]EdgePutResult, error)
	searchPageFn             func(ctx context.Context, query string, opts ...SearchOption) (SearchPage, error)
	// addEdgeAtWithIDsFn / addEdgesWithIDsFn drive the id-accepting seams the
	// failover ring actually calls for additive writes (#916); the failover
	// AddEdge/AddEdgeAt/AddEdges methods route through these, so tests wire
	// them to observe the contrib ids passed down.
	addEdgeAtWithIDsFn               func(ctx context.Context, tail, head string, weight float32, expiration time.Time, ids [][]byte) (float32, error)
	addEdgesWithIDsFn                func(ctx context.Context, inputs []EdgeInput, ids [][]byte) ([]float32, error)
	getReceiptCapabilityFn           func(ctx context.Context) (ReceiptCapability, error)
	getReceiptStatusesFn             func(ctx context.Context, ids []ReceiptOperationID) ([]ReceiptStatus, error)
	putVerticesWithReceiptFn         func(ctx context.Context, inputs []VertexInput, receiptContext ReceiptContext) ([]VertexPutReceiptResult, error)
	putVerticesIfAbsentWithReceiptFn func(ctx context.Context, inputs []VertexInput, receiptContext ReceiptContext) ([]VertexPutReceiptResult, error)
	deleteVerticesWithReceiptFn      func(ctx context.Context, keys []string, receiptContext ReceiptContext) ([]VertexDeleteReceiptResult, error)
	deleteEdgesWithReceiptFn         func(ctx context.Context, refs []EdgeRef, receiptContext ReceiptContext) ([]EdgeDeleteReceiptResult, error)
	addEdgesWithReceiptFn            func(ctx context.Context, inputs []EdgeAddReceiptInput, receiptContext ReceiptContext) ([]EdgeAddReceiptResult, error)
	pingErr                          error
	closed                           int
}

func (f *fakeNode) PutVertex(context.Context, string, any, time.Duration) (PutOutcome, error) {
	return PutOutcomeAppliedAndLive, nil
}

func (f *fakeNode) PutVertexAt(ctx context.Context, key string, value any, expiration time.Time) (PutOutcome, error) {
	if f.putVertexAtFn != nil {
		return f.putVertexAtFn(ctx, key, value, expiration)
	}
	return PutOutcomeAppliedAndLive, nil
}
func (f *fakeNode) PutVertices(ctx context.Context, inputs []VertexInput) ([]VertexPutResult, error) {
	if f.putVerticesFn != nil {
		return f.putVerticesFn(ctx, inputs)
	}
	return nil, nil
}
func (f *fakeNode) PutVertexIfAbsent(context.Context, string, any, time.Duration) (PutOutcome, error) {
	return PutOutcomeAppliedAndLive, nil
}
func (f *fakeNode) PutVertexIfAbsentAt(ctx context.Context, key string, value any, expiration time.Time) (PutOutcome, error) {
	if f.putVertexIfAbsentAtFn != nil {
		return f.putVertexIfAbsentAtFn(ctx, key, value, expiration)
	}
	return PutOutcomeAppliedAndLive, nil
}
func (f *fakeNode) PutVerticesIfAbsent(ctx context.Context, inputs []VertexInput) ([]VertexPutResult, error) {
	if f.putVerticesIfAbsentFn != nil {
		return f.putVerticesIfAbsentFn(ctx, inputs)
	}
	return nil, nil
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
func (f *fakeNode) DeleteVertex(ctx context.Context, key string) (bool, error) {
	if f.deleteVertexFn != nil {
		return f.deleteVertexFn(ctx, key)
	}
	return false, nil
}
func (f *fakeNode) DeleteVertices(ctx context.Context, keys []string) (int, error) {
	if f.deleteVerticesFn != nil {
		return f.deleteVerticesFn(ctx, keys)
	}
	return 0, nil
}
func (f *fakeNode) ScanVertices(context.Context, string, ...ScanOption) ([]*Vertex, []byte, error) {
	return nil, nil, nil
}
func (f *fakeNode) ScanVertexKeys(context.Context, string, ...ScanOption) ([]string, []byte, error) {
	return nil, nil, nil
}
func (f *fakeNode) SearchVertices(context.Context, string, ...SearchOption) ([]SearchHit, error) {
	return nil, nil
}
func (f *fakeNode) SearchVerticesPage(ctx context.Context, query string, opts ...SearchOption) (SearchPage, error) {
	if f.searchPageFn != nil {
		return f.searchPageFn(ctx, query, opts...)
	}
	return SearchPage{}, nil
}
func (f *fakeNode) CountVerticesByPrefix(context.Context, string) (uint64, error) { return 0, nil }
func (f *fakeNode) DeleteVerticesByPrefix(ctx context.Context, prefix string, opts ...DeleteByPrefixOption) (uint64, error) {
	if f.deleteVerticesByPrefixFn != nil {
		return f.deleteVerticesByPrefixFn(ctx, prefix, opts...)
	}
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
func (f *fakeNode) PutEdge(context.Context, string, string, float32, time.Duration) (PutOutcome, error) {
	return PutOutcomeAppliedAndLive, nil
}
func (f *fakeNode) PutEdgeAt(ctx context.Context, tail, head string, weight float32, expiration time.Time) (PutOutcome, error) {
	if f.putEdgeAtFn != nil {
		return f.putEdgeAtFn(ctx, tail, head, weight, expiration)
	}
	return PutOutcomeAppliedAndLive, nil
}
func (f *fakeNode) PutEdges(ctx context.Context, inputs []EdgeInput) ([]EdgePutResult, error) {
	if f.putEdgesFn != nil {
		return f.putEdgesFn(ctx, inputs)
	}
	return nil, nil
}
func (f *fakeNode) GetEdge(context.Context, string, string) (*Edge, error) { return nil, nil }
func (f *fakeNode) GetEdges(context.Context, []EdgeRef) ([]*Edge, []EdgeRef, error) {
	return nil, nil, nil
}
func (f *fakeNode) ScanEdges(context.Context, ...EdgeScanOption) ([]*Edge, []byte, error) {
	return nil, nil, nil
}
func (f *fakeNode) DeleteEdgesByPrefix(ctx context.Context, opts ...DeleteEdgesByPrefixOption) (uint64, error) {
	if f.deleteEdgesByPrefixFn != nil {
		return f.deleteEdgesByPrefixFn(ctx, opts...)
	}
	return 0, nil
}
func (f *fakeNode) DeleteEdge(ctx context.Context, tail, head string) (bool, error) {
	if f.deleteEdgeFn != nil {
		return f.deleteEdgeFn(ctx, tail, head)
	}
	return false, nil
}
func (f *fakeNode) DeleteEdges(ctx context.Context, refs []EdgeRef) (int, error) {
	if f.deleteEdgesFn != nil {
		return f.deleteEdgesFn(ctx, refs)
	}
	return 0, nil
}
func (f *fakeNode) GetReceiptCapability(ctx context.Context) (ReceiptCapability, error) {
	if f.getReceiptCapabilityFn != nil {
		return f.getReceiptCapabilityFn(ctx)
	}
	return ReceiptCapability{}, nil
}
func (f *fakeNode) GetReceiptStatuses(ctx context.Context, ids []ReceiptOperationID) ([]ReceiptStatus, error) {
	if f.getReceiptStatusesFn != nil {
		return f.getReceiptStatusesFn(ctx, ids)
	}
	return nil, nil
}
func (f *fakeNode) PutVerticesWithReceipt(
	ctx context.Context,
	inputs []VertexInput,
	receiptContext ReceiptContext,
) ([]VertexPutReceiptResult, error) {
	if f.putVerticesWithReceiptFn != nil {
		return f.putVerticesWithReceiptFn(ctx, inputs, receiptContext)
	}
	return nil, nil
}
func (f *fakeNode) PutVerticesIfAbsentWithReceipt(
	ctx context.Context,
	inputs []VertexInput,
	receiptContext ReceiptContext,
) ([]VertexPutReceiptResult, error) {
	if f.putVerticesIfAbsentWithReceiptFn != nil {
		return f.putVerticesIfAbsentWithReceiptFn(ctx, inputs, receiptContext)
	}
	return nil, nil
}
func (f *fakeNode) DeleteVerticesWithReceipt(
	ctx context.Context,
	keys []string,
	receiptContext ReceiptContext,
) ([]VertexDeleteReceiptResult, error) {
	if f.deleteVerticesWithReceiptFn != nil {
		return f.deleteVerticesWithReceiptFn(ctx, keys, receiptContext)
	}
	return nil, nil
}
func (f *fakeNode) DeleteEdgesWithReceipt(ctx context.Context, refs []EdgeRef, receiptContext ReceiptContext) ([]EdgeDeleteReceiptResult, error) {
	if f.deleteEdgesWithReceiptFn != nil {
		return f.deleteEdgesWithReceiptFn(ctx, refs, receiptContext)
	}
	return nil, nil
}
func (f *fakeNode) AddEdgesWithReceipt(ctx context.Context, inputs []EdgeAddReceiptInput, receiptContext ReceiptContext) ([]EdgeAddReceiptResult, error) {
	if f.addEdgesWithReceiptFn != nil {
		return f.addEdgesWithReceiptFn(ctx, inputs, receiptContext)
	}
	return nil, nil
}
func (f *fakeNode) Illuminate(context.Context, string, ...IlluminateOption) (*Graph, error) {
	return nil, nil
}
func (f *fakeNode) Ping(context.Context) error { return f.pingErr }
func (f *fakeNode) Close() error               { f.closed++; return nil }

func TestFailoverReceiptDeleteNeverRotates(t *testing.T) {
	capability := testReceiptCapability(0x70)
	receiptContext := testReceiptContext(t, capability, 1, 0x71)
	firstCalls, secondCalls := 0, 0
	first := &fakeNode{
		getReceiptCapabilityFn: func(context.Context) (ReceiptCapability, error) {
			return capability, nil
		},
		deleteEdgesWithReceiptFn: func(
			context.Context,
			[]EdgeRef,
			ReceiptContext,
		) ([]EdgeDeleteReceiptResult, error) {
			firstCalls++
			return nil, wrapConnectErr(connect.NewError(connect.CodeUnavailable, errors.New("response lost")))
		},
	}
	second := &fakeNode{deleteEdgesWithReceiptFn: func(
		context.Context,
		[]EdgeRef,
		ReceiptContext,
	) ([]EdgeDeleteReceiptResult, error) {
		secondCalls++
		return []EdgeDeleteReceiptResult{{Existed: true}}, nil
	}}
	f := &Failover{
		nodes: []failoverNode{first, second},
		retry: testRetryPolicy(3),
	}
	if _, err := f.DeleteEdgeWithReceipt(context.Background(), "tail", "head", receiptContext); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
	if firstCalls != 3 || secondCalls != 0 {
		t.Fatalf("receipt attempts rotated: first=%d second=%d", firstCalls, secondCalls)
	}
}

func TestFailoverReceiptDeleteFindsAndPinsPersistedEndpoint(t *testing.T) {
	capability := testReceiptCapability(0x72)
	other := testReceiptCapability(0x73)
	receiptContext := testReceiptContext(t, capability, 1, 0x74)
	firstCapabilityCalls, secondCapabilityCalls := 0, 0
	firstDeleteCalls, secondDeleteCalls := 0, 0
	first := &fakeNode{
		getReceiptCapabilityFn: func(context.Context) (ReceiptCapability, error) {
			firstCapabilityCalls++
			return other, nil
		},
		deleteEdgesWithReceiptFn: func(
			context.Context,
			[]EdgeRef,
			ReceiptContext,
		) ([]EdgeDeleteReceiptResult, error) {
			firstDeleteCalls++
			return nil, errors.New("wrong endpoint received mutation")
		},
	}
	second := &fakeNode{
		getReceiptCapabilityFn: func(context.Context) (ReceiptCapability, error) {
			secondCapabilityCalls++
			return capability, nil
		},
		deleteEdgesWithReceiptFn: func(
			_ context.Context,
			refs []EdgeRef,
			got ReceiptContext,
		) ([]EdgeDeleteReceiptResult, error) {
			secondDeleteCalls++
			if got.Continuity != receiptContext.Continuity {
				t.Fatalf("continuity = %+v, want %+v", got.Continuity, receiptContext.Continuity)
			}
			if secondDeleteCalls == 1 {
				return nil, wrapConnectErr(connect.NewError(connect.CodeUnavailable, errors.New("response lost")))
			}
			return []EdgeDeleteReceiptResult{{
				Edge:        refs[0],
				OperationID: got.OperationIDs[0],
				Existed:     true,
			}}, nil
		},
	}
	f := &Failover{
		nodes: []failoverNode{first, second},
		retry: testRetryPolicy(2),
	}

	result, err := f.DeleteEdgeWithReceipt(context.Background(), "tail", "head", receiptContext)
	if err != nil || !result.Existed {
		t.Fatalf("result = (%+v, %v)", result, err)
	}
	if firstCapabilityCalls != 1 || secondCapabilityCalls != 1 {
		t.Fatalf("capability calls first=%d second=%d, want 1/1", firstCapabilityCalls, secondCapabilityCalls)
	}
	if firstDeleteCalls != 0 || secondDeleteCalls != 2 {
		t.Fatalf("delete calls first=%d second=%d, want 0/2", firstDeleteCalls, secondDeleteCalls)
	}
}

func TestFailoverReceiptDeleteRejectsMalformedContextBeforeDiscovery(t *testing.T) {
	capabilityCalls := 0
	node := &fakeNode{getReceiptCapabilityFn: func(context.Context) (ReceiptCapability, error) {
		capabilityCalls++
		return ReceiptCapability{}, nil
	}}
	f := &Failover{nodes: []failoverNode{node}}

	if _, err := f.DeleteEdgeWithReceipt(
		context.Background(),
		"tail",
		"head",
		ReceiptContext{},
	); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatalf("error = %v", err)
	}
	if capabilityCalls != 0 {
		t.Fatalf("capability calls = %d, want 0", capabilityCalls)
	}
	if f.cur.Load() != 0 {
		t.Fatalf("cursor moved to %d", f.cur.Load())
	}
}

func TestFailoverReceiptVertexPutFindsAndPinsPersistedEndpoint(t *testing.T) {
	capability := testReceiptCapability(0x75)
	other := testReceiptCapability(0x76)
	receiptContext := testReceiptContextForMutation(
		t,
		capability,
		ReceiptMutationPutVertex,
		1,
		0x77,
	)
	firstCalls, secondCalls := 0, 0
	first := &fakeNode{
		getReceiptCapabilityFn: func(context.Context) (ReceiptCapability, error) {
			return other, nil
		},
		putVerticesWithReceiptFn: func(
			context.Context,
			[]VertexInput,
			ReceiptContext,
		) ([]VertexPutReceiptResult, error) {
			firstCalls++
			return nil, errors.New("wrong endpoint received mutation")
		},
	}
	second := &fakeNode{
		getReceiptCapabilityFn: func(context.Context) (ReceiptCapability, error) {
			return capability, nil
		},
		putVerticesWithReceiptFn: func(
			_ context.Context,
			inputs []VertexInput,
			got ReceiptContext,
		) ([]VertexPutReceiptResult, error) {
			secondCalls++
			if secondCalls == 1 {
				return nil, wrapConnectErr(connect.NewError(
					connect.CodeUnavailable,
					errors.New("response lost"),
				))
			}
			return []VertexPutReceiptResult{{
				Key:         inputs[0].Key,
				OperationID: got.OperationIDs[0],
				Outcome:     PutOutcomeAppliedAndLive,
			}}, nil
		},
	}
	f := &Failover{
		nodes: []failoverNode{first, second},
		retry: testRetryPolicy(2),
	}

	result, err := f.PutVertexWithReceipt(
		context.Background(),
		"vertex",
		"value",
		time.Minute,
		receiptContext,
	)
	if err != nil || result.Outcome != PutOutcomeAppliedAndLive {
		t.Fatalf("result = (%+v, %v)", result, err)
	}
	if firstCalls != 0 || secondCalls != 2 {
		t.Fatalf("put calls first=%d second=%d, want 0/2", firstCalls, secondCalls)
	}
}

func TestFailoverReceiptVertexDeleteRejectsUnsupportedEndpoint(t *testing.T) {
	capability := testReceiptCapability(0x78)
	receiptContext := testReceiptContextForMutation(
		t,
		capability,
		ReceiptMutationDeleteVertex,
		1,
		0x79,
	)
	capability.SupportedMutations = []ReceiptMutationKind{
		ReceiptMutationPutVertex,
		ReceiptMutationDeleteEdge,
	}
	mutationCalls := 0
	node := &fakeNode{
		getReceiptCapabilityFn: func(context.Context) (ReceiptCapability, error) {
			return capability, nil
		},
		deleteVerticesWithReceiptFn: func(
			context.Context,
			[]string,
			ReceiptContext,
		) ([]VertexDeleteReceiptResult, error) {
			mutationCalls++
			return nil, nil
		},
	}
	f := &Failover{nodes: []failoverNode{node}}

	_, err := f.DeleteVertexWithReceipt(
		context.Background(),
		"vertex",
		receiptContext,
	)
	if !errors.Is(err, ErrReceiptMutationUnsupported) ||
		!errors.Is(err, ErrReceiptReconciliationRequired) {
		t.Fatalf("error = %v", err)
	}
	if mutationCalls != 0 {
		t.Fatalf("mutation calls = %d, want 0", mutationCalls)
	}
}

func TestFailoverReceiptAddFindsAndPinsPersistedEndpoint(t *testing.T) {
	capability := testReceiptCapability(0x7a)
	other := testReceiptCapability(0x7b)
	receiptContext := testReceiptContextForMutation(
		t,
		capability,
		ReceiptMutationAddEdge,
		1,
		0x7c,
	)
	input := EdgeAddReceiptInput{
		Edge: EdgeInput{
			Tail: "tail", Head: "head", Weight: 2,
			Expiration: capability.ServerTime.Add(time.Hour),
		},
		ContribID: testContribID(0x81),
	}
	firstCalls, secondCalls := 0, 0
	first := &fakeNode{
		getReceiptCapabilityFn: func(context.Context) (ReceiptCapability, error) {
			return other, nil
		},
		addEdgesWithReceiptFn: func(
			context.Context,
			[]EdgeAddReceiptInput,
			ReceiptContext,
		) ([]EdgeAddReceiptResult, error) {
			firstCalls++
			return nil, errors.New("wrong endpoint received mutation")
		},
	}
	second := &fakeNode{
		getReceiptCapabilityFn: func(context.Context) (ReceiptCapability, error) {
			return capability, nil
		},
		addEdgesWithReceiptFn: func(
			_ context.Context,
			inputs []EdgeAddReceiptInput,
			got ReceiptContext,
		) ([]EdgeAddReceiptResult, error) {
			secondCalls++
			if secondCalls == 1 {
				return nil, wrapConnectErr(connect.NewError(
					connect.CodeUnavailable,
					errors.New("committed response lost"),
				))
			}
			return []EdgeAddReceiptResult{{
				Edge: EdgeRef{
					Tail: inputs[0].Edge.Tail,
					Head: inputs[0].Edge.Head,
				},
				ContribID:       inputs[0].ContribID,
				OperationID:     got.OperationIDs[0],
				EffectiveWeight: 2,
			}}, nil
		},
	}
	f := &Failover{
		nodes: []failoverNode{first, second},
		retry: testRetryPolicy(2),
	}

	result, err := f.AddEdgeAtWithReceipt(
		context.Background(),
		input.Edge.Tail,
		input.Edge.Head,
		input.Edge.Weight,
		input.Edge.Expiration,
		input.ContribID,
		receiptContext,
	)
	if err != nil || result.EffectiveWeight != 2 {
		t.Fatalf("result = (%+v, %v)", result, err)
	}
	if firstCalls != 0 || secondCalls != 2 {
		t.Fatalf("Add calls first=%d second=%d, want 0/2", firstCalls, secondCalls)
	}
}

func TestFailoverReceiptAddNeverRotatesAfterAmbiguousResponse(t *testing.T) {
	capability := testReceiptCapability(0x7d)
	receiptContext := testReceiptContextForMutation(
		t,
		capability,
		ReceiptMutationAddEdge,
		1,
		0x7e,
	)
	firstCalls, secondCalls := 0, 0
	first := &fakeNode{
		getReceiptCapabilityFn: func(context.Context) (ReceiptCapability, error) {
			return capability, nil
		},
		addEdgesWithReceiptFn: func(
			context.Context,
			[]EdgeAddReceiptInput,
			ReceiptContext,
		) ([]EdgeAddReceiptResult, error) {
			firstCalls++
			return nil, wrapConnectErr(connect.NewError(
				connect.CodeUnavailable,
				errors.New("committed response lost"),
			))
		},
	}
	second := &fakeNode{
		addEdgesWithReceiptFn: func(
			context.Context,
			[]EdgeAddReceiptInput,
			ReceiptContext,
		) ([]EdgeAddReceiptResult, error) {
			secondCalls++
			return []EdgeAddReceiptResult{{EffectiveWeight: 2}}, nil
		},
	}
	f := &Failover{
		nodes: []failoverNode{first, second},
		retry: testRetryPolicy(3),
	}

	_, err := f.AddEdgeWithReceipt(
		context.Background(),
		"tail",
		"head",
		2,
		time.Hour,
		testContribID(0x91),
		receiptContext,
	)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
	if firstCalls != 3 || secondCalls != 0 {
		t.Fatalf("receipt attempts rotated: first=%d second=%d", firstCalls, secondCalls)
	}
}

func TestFailoverSearchPageCursorIsEndpointSticky(t *testing.T) {
	firstCalls, secondCalls := 0, 0
	first := &fakeNode{searchPageFn: func(context.Context, string, ...SearchOption) (SearchPage, error) {
		firstCalls++
		return SearchPage{}, ErrUnavailable
	}}
	second := &fakeNode{searchPageFn: func(_ context.Context, _ string, opts ...SearchOption) (SearchPage, error) {
		secondCalls++
		configured := searchOptions{}
		for _, apply := range opts {
			apply(&configured)
		}
		if len(configured.cursor) == 0 {
			return SearchPage{Hits: []SearchHit{{Key: "a"}}, NextCursor: []byte("sticky")}, nil
		}
		return SearchPage{}, ErrUnavailable
	}}
	f := &Failover{nodes: []failoverNode{first, second}}
	page, err := f.SearchVerticesPage(context.Background(), "alpha")
	if err != nil || string(page.NextCursor) != "sticky" {
		t.Fatalf("first page = %+v, err=%v", page, err)
	}
	if firstCalls != 1 || secondCalls != 1 || f.cur.Load() != 1 {
		t.Fatalf("first page calls = %d/%d current=%d", firstCalls, secondCalls, f.cur.Load())
	}
	_, err = f.SearchVerticesPage(context.Background(), "alpha", WithSearchCursor(page.NextCursor))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("continuation err = %v, want ErrUnavailable", err)
	}
	if firstCalls != 1 || secondCalls != 2 {
		t.Fatalf("cursor rotated endpoints: calls=%d/%d", firstCalls, secondCalls)
	}
}

func TestFailoverSearchIteratorHonorsInitialCursor(t *testing.T) {
	wantCursor := []byte("resume")
	node := &fakeNode{searchPageFn: func(_ context.Context, _ string, opts ...SearchOption) (SearchPage, error) {
		configured := searchOptions{}
		for _, apply := range opts {
			apply(&configured)
		}
		if !bytes.Equal(configured.cursor, wantCursor) {
			t.Fatalf("cursor = %q, want %q", configured.cursor, wantCursor)
		}
		return SearchPage{Hits: []SearchHit{{Key: "resumed"}}}, nil
	}}
	f := &Failover{nodes: []failoverNode{node}}

	var keys []string
	for hit, err := range f.SearchVerticesIter(context.Background(), "alpha", WithSearchCursor(wantCursor)) {
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, hit.Key)
	}
	if len(keys) != 1 || keys[0] != "resumed" {
		t.Fatalf("keys = %v", keys)
	}
}

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

func TestFailover_ResponseLossAfterAddAndInterveningDelete(t *testing.T) {
	var firstCalls, secondCalls int
	var live bool
	node0 := &fakeNode{addEdgeAtWithIDsFn: func(_ context.Context, _, _ string, _ float32, _ time.Time, ids [][]byte) (float32, error) {
		firstCalls++
		if len(ids) != 1 || len(ids[0]) != ContribIDSize {
			t.Fatalf("ContribIDs = %x, want one %d-byte ID", ids, ContribIDSize)
		}
		live = true  // Add committed before its response was lost.
		live = false // An intervening Delete removed the contribution and its ID.
		return 0, unavailableErr()
	}}
	node1 := &fakeNode{addEdgeAtWithIDsFn: func(context.Context, string, string, float32, time.Time, [][]byte) (float32, error) {
		secondCalls++
		live = true // A ContribID-only replay would recreate the deleted edge.
		return 1, nil
	}}
	f := &Failover{nodes: []failoverNode{node0, node1}, retry: testRetryPolicy(3), contribIDs: &contribIDGen{}}

	if weight, err := f.AddEdge(context.Background(), "a", "b", 1, time.Minute); !errors.Is(err, ErrUnavailable) || weight != 0 {
		t.Fatalf("AddEdge = (%v, %v), want ambiguous ErrUnavailable", weight, err)
	}
	if firstCalls != 1 || secondCalls != 0 || live || f.cur.Load() != 0 {
		t.Fatalf("response loss replayed Add: calls=%d/%d live=%t current=%d", firstCalls, secondCalls, live, f.cur.Load())
	}
}

func TestFailoverIfAbsentDoesNotReplayAmbiguousOutcome(t *testing.T) {
	firstCalls, secondCalls := 0, 0
	first := &fakeNode{putVertexIfAbsentAtFn: func(context.Context, string, any, time.Time) (PutOutcome, error) {
		firstCalls++
		return 0, unavailableErr()
	}}
	second := &fakeNode{putVertexIfAbsentAtFn: func(context.Context, string, any, time.Time) (PutOutcome, error) {
		secondCalls++
		return PutOutcomeConditionNotMet, nil
	}}
	f := &Failover{nodes: []failoverNode{first, second}, retry: testRetryPolicy(3)}

	if outcome, err := f.PutVertexIfAbsent(context.Background(), "k", "v", time.Minute); !errors.Is(err, ErrUnavailable) || outcome != 0 {
		t.Fatalf("PutVertexIfAbsent = (%v, %v), want ambiguous ErrUnavailable", outcome, err)
	}
	if firstCalls != 1 || secondCalls != 0 {
		t.Fatalf("call counts first=%d second=%d, want 1/0 (no replay or rotation)", firstCalls, secondCalls)
	}
}

func TestFailoverRelativePutTTLResolvedOnceBeforeRingWalk(t *testing.T) {
	const ttl = time.Minute

	t.Run("vertex", func(t *testing.T) {
		var expirations []time.Time
		record := func(outcome PutOutcome, err error) *fakeNode {
			return &fakeNode{putVertexAtFn: func(_ context.Context, _ string, _ any, expiration time.Time) (PutOutcome, error) {
				expirations = append(expirations, expiration)
				time.Sleep(time.Millisecond)
				return outcome, err
			}}
		}
		before := time.Now().Add(ttl)
		f := &Failover{nodes: []failoverNode{
			record(0, unavailableErr()),
			record(PutOutcomeAppliedAndLive, nil),
		}}
		outcome, err := f.PutVertex(context.Background(), "k", "v", ttl)
		after := time.Now().Add(ttl)
		if err != nil || outcome != PutOutcomeAppliedAndLive {
			t.Fatalf("PutVertex = (%s, %v), want APPLIED_AND_LIVE, nil", outcome, err)
		}
		if len(expirations) != 2 || !expirations[0].Equal(expirations[1]) {
			t.Fatalf("ring attempts saw expirations %v, want one identical absolute expiration", expirations)
		}
		if expirations[0].Before(before) || expirations[0].After(after) {
			t.Fatalf("resolved expiration %v outside [%v, %v]", expirations[0], before, after)
		}
	})

	t.Run("conditional vertex", func(t *testing.T) {
		var expiration time.Time
		f := &Failover{nodes: []failoverNode{&fakeNode{
			putVertexIfAbsentAtFn: func(_ context.Context, _ string, _ any, observed time.Time) (PutOutcome, error) {
				expiration = observed
				return PutOutcomeAppliedAndLive, nil
			},
		}}}
		before := time.Now().Add(ttl)
		outcome, err := f.PutVertexIfAbsent(context.Background(), "k", "v", ttl)
		after := time.Now().Add(ttl)
		if err != nil || outcome != PutOutcomeAppliedAndLive {
			t.Fatalf("PutVertexIfAbsent = (%s, %v), want APPLIED_AND_LIVE, nil", outcome, err)
		}
		if expiration.Before(before) || expiration.After(after) {
			t.Fatalf("resolved expiration %v outside [%v, %v]", expiration, before, after)
		}
	})

	t.Run("edge", func(t *testing.T) {
		var expirations []time.Time
		record := func(outcome PutOutcome, err error) *fakeNode {
			return &fakeNode{putEdgeAtFn: func(_ context.Context, _, _ string, _ float32, expiration time.Time) (PutOutcome, error) {
				expirations = append(expirations, expiration)
				time.Sleep(time.Millisecond)
				return outcome, err
			}}
		}
		before := time.Now().Add(ttl)
		f := &Failover{nodes: []failoverNode{
			record(0, unavailableErr()),
			record(PutOutcomeAppliedAndLive, nil),
		}}
		outcome, err := f.PutEdge(context.Background(), "a", "b", 1, ttl)
		after := time.Now().Add(ttl)
		if err != nil || outcome != PutOutcomeAppliedAndLive {
			t.Fatalf("PutEdge = (%s, %v), want APPLIED_AND_LIVE, nil", outcome, err)
		}
		if len(expirations) != 2 || !expirations[0].Equal(expirations[1]) {
			t.Fatalf("ring attempts saw expirations %v, want one identical absolute expiration", expirations)
		}
		if expirations[0].Before(before) || expirations[0].After(after) {
			t.Fatalf("resolved expiration %v outside [%v, %v]", expirations[0], before, after)
		}
	})
}

func TestFailoverPutOutcomeClockRollbackPreservesInitialLiveness(t *testing.T) {
	base := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
	expiration := base.Add(time.Second)
	clockCalls := 0
	f := &Failover{
		nodes: []failoverNode{&fakeNode{}},
		clock: func() time.Time {
			clockCalls++
			if clockCalls == 1 {
				return expiration.Add(time.Second)
			}
			return base
		},
	}
	outcome, err := f.PutVertexAt(context.Background(), "k", "v", expiration)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != PutOutcomeExpired {
		t.Fatalf("PutVertexAt outcome = %s, want EXPIRED after clock rollback", outcome)
	}
	if clockCalls != 2 {
		t.Fatalf("clock calls = %d, want request and final samples", clockCalls)
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

func TestFailover_RetryEligibleReadWithoutAddKeys(t *testing.T) {
	mk := func(c *int) *fakeNode {
		return &fakeNode{getVertexFn: func(context.Context, string) (*Vertex, error) {
			*c++
			return nil, unavailableErr()
		}}
	}
	var a, b int
	f := &Failover{nodes: []failoverNode{mk(&a), mk(&b)}, retry: testRetryPolicy(3)}

	if _, err := f.GetVertex(context.Background(), "k"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	// 3 ring walks × 1 hit per node each.
	if a != 3 || b != 3 {
		t.Fatalf("counts a=%d b=%d, want 3 and 3 (MaxAttempts ring walks)", a, b)
	}
}

func TestFailover_ResultBearingWritesNeverReplay(t *testing.T) {
	node := func(calls *int, result error) *fakeNode {
		record := func() { *calls++ }
		return &fakeNode{
			deleteVertexFn: func(context.Context, string) (bool, error) {
				record()
				return false, result
			},
			deleteVerticesFn: func(context.Context, []string) (int, error) {
				record()
				return 0, result
			},
			deleteEdgeFn: func(context.Context, string, string) (bool, error) {
				record()
				return false, result
			},
			deleteEdgesFn: func(context.Context, []EdgeRef) (int, error) {
				record()
				return 0, result
			},
			deleteVerticesByPrefixFn: func(context.Context, string, ...DeleteByPrefixOption) (uint64, error) {
				record()
				return 0, result
			},
			deleteEdgesByPrefixFn: func(context.Context, ...DeleteEdgesByPrefixOption) (uint64, error) {
				record()
				return 0, result
			},
			addEdgeAtWithIDsFn: func(context.Context, string, string, float32, time.Time, [][]byte) (float32, error) {
				record()
				return 0, result
			},
			addEdgesWithIDsFn: func(context.Context, []EdgeInput, [][]byte) ([]float32, error) {
				record()
				return nil, result
			},
		}
	}
	expiration := time.Now().Add(time.Hour)
	cases := []struct {
		name string
		call func(*Failover) error
	}{
		{"DeleteVertex", func(f *Failover) error { _, err := f.DeleteVertex(context.Background(), "k"); return err }},
		{"DeleteVertices", func(f *Failover) error { _, err := f.DeleteVertices(context.Background(), []string{"k"}); return err }},
		{"DeleteEdge", func(f *Failover) error { _, err := f.DeleteEdge(context.Background(), "a", "b"); return err }},
		{"DeleteEdges", func(f *Failover) error {
			_, err := f.DeleteEdges(context.Background(), []EdgeRef{{Tail: "a", Head: "b"}})
			return err
		}},
		{"DeleteVerticesByPrefix capped", func(f *Failover) error {
			_, err := f.DeleteVerticesByPrefix(context.Background(), "k/", WithDeleteByPrefixLimit(1))
			return err
		}},
		{"DeleteVerticesByPrefix dry run", func(f *Failover) error {
			_, err := f.DeleteVerticesByPrefix(context.Background(), "k/", WithDryRun())
			return err
		}},
		{"DeleteEdgesByPrefix capped", func(f *Failover) error {
			_, err := f.DeleteEdgesByPrefix(context.Background(), WithEdgeDeleteTailPrefix("a/"), WithEdgeDeleteLimit(1))
			return err
		}},
		{"DeleteEdgesByPrefix dry run", func(f *Failover) error {
			_, err := f.DeleteEdgesByPrefix(context.Background(), WithEdgeDeleteTailPrefix("a/"), WithEdgeDeleteDryRun())
			return err
		}},
		{"AddEdge with ContribID", func(f *Failover) error {
			_, err := f.AddEdge(context.Background(), "a", "b", 1, time.Hour)
			return err
		}},
		{"AddEdgeAt with ContribID", func(f *Failover) error {
			_, err := f.AddEdgeAt(context.Background(), "a", "b", 1, expiration)
			return err
		}},
		{"AddEdges with ContribIDs", func(f *Failover) error {
			_, err := f.AddEdges(context.Background(), []EdgeInput{{Tail: "a", Head: "b", Weight: 1, Expiration: expiration}})
			return err
		}},
		{"AddDecayingEdge with ContribIDs", func(f *Failover) error {
			_, err := f.AddDecayingEdge(context.Background(), "a", "b", DecayOpts{
				InitialWeight: 1, Ratio: 0.5, Steps: 2, Interval: time.Minute,
			})
			return err
		}},
	}
	for _, tc := range cases {
		for _, policy := range []struct {
			name  string
			retry *RetryPolicy
		}{
			{"zero config", nil},
			{"WithRetry", testRetryPolicy(3)},
		} {
			t.Run(tc.name+"/"+policy.name, func(t *testing.T) {
				var first, second int
				f := &Failover{
					nodes:      []failoverNode{node(&first, unavailableErr()), node(&second, nil)},
					retry:      policy.retry,
					contribIDs: &contribIDGen{},
				}
				if err := tc.call(f); !errors.Is(err, ErrUnavailable) {
					t.Fatalf("error = %v, want ambiguous ErrUnavailable", err)
				}
				if first != 1 || second != 0 || f.cur.Load() != 0 {
					t.Fatalf("attempts=%d/%d current=%d, want one pinned attempt", first, second, f.cur.Load())
				}
			})
		}
	}
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
	// The failover-level ContribID generator must be armed when idempotent
	// adds are on, so each Add receives stable per-call contribution IDs.
	// This does not enable automatic retry/failover after response loss.
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
		// ...while idempotent-adds still flows to the nodes so each Add
		// stamps ContribIDs on its single attempt.
		if !l.opts.idempotentAdds {
			t.Fatalf("node %d idempotentAdds not propagated", i)
		}
	}
}

// TestFailover_AddDecayingEdge covers the staircase's effective weight and
// ensures one failed attempt does not replay the expanded contributions.
func TestFailover_AddDecayingEdge(t *testing.T) {
	opts := DecayOpts{InitialWeight: 16, Ratio: 0.5, Steps: 5, Interval: time.Second}

	t.Run("returns post-add live weight on success", func(t *testing.T) {
		node := &fakeNode{addEdgesWithIDsFn: func(_ context.Context, inputs []EdgeInput, _ [][]byte) ([]float32, error) {
			eff := make([]float32, len(inputs))
			var running float32
			for i, in := range inputs {
				running += in.Weight
				eff[i] = running
			}
			return eff, nil
		}}
		f := &Failover{nodes: []failoverNode{node}}

		got, err := f.AddDecayingEdge(context.Background(), "a", "b", opts)
		if err != nil {
			t.Fatalf("AddDecayingEdge err = %v", err)
		}
		if d := got - 16; d < -1e-4 || d > 1e-4 {
			t.Fatalf("post-add live weight = %v, want 16", got)
		}
	})

	t.Run("response loss stops without replaying contributions", func(t *testing.T) {
		var firstCalls, secondCalls int
		var inputs []EdgeInput
		var ids [][]byte
		first := &fakeNode{addEdgesWithIDsFn: func(_ context.Context, gotInputs []EdgeInput, gotIDs [][]byte) ([]float32, error) {
			firstCalls++
			inputs = append([]EdgeInput(nil), gotInputs...)
			ids = gotIDs
			return nil, unavailableErr()
		}}
		second := &fakeNode{addEdgesWithIDsFn: func(context.Context, []EdgeInput, [][]byte) ([]float32, error) {
			secondCalls++
			return []float32{16}, nil
		}}
		f := &Failover{
			nodes:      []failoverNode{first, second},
			retry:      testRetryPolicy(3),
			contribIDs: &contribIDGen{},
		}
		if _, err := f.AddDecayingEdge(context.Background(), "a", "b", opts); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable", err)
		}
		if firstCalls != 1 || secondCalls != 0 {
			t.Fatalf("attempts first=%d second=%d, want 1/0", firstCalls, secondCalls)
		}
		if len(inputs) != 5 || len(ids) != 5 {
			t.Fatalf("single attempt = %d inputs / %d ids, want 5 and 5", len(inputs), len(ids))
		}
		for i := range ids {
			if len(ids[i]) != ContribIDSize {
				t.Fatalf("id[%d] has %d bytes, want %d", i, len(ids[i]), ContribIDSize)
			}
			for j := 0; j < i; j++ {
				if bytes.Equal(ids[i], ids[j]) {
					t.Fatalf("id[%d] duplicated id[%d]", i, j)
				}
			}
		}
	})
}
