package dataruntime

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/query"
	"github.com/anaregdesign/tarp"
)

func TestTarpSearchAdmissionUsesStagedGraphTimeAndDoesNotPublishRejectedRows(t *testing.T) {
	ctx := t.Context()
	authority, err := graphstore.Open(ctx, t.TempDir(), hlc.NodeID{1})
	if err != nil {
		t.Fatal(err)
	}
	graph := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](0)
	stage, err := RestoreGraphStorage(ctx, graph, authority)
	if err != nil {
		_ = authority.Close()
		t.Fatal(err)
	}
	runtime, err := NewTarpGraphServingRuntime(ctx, graph, authority, mutationlog.Options{Capacity: 16}, stage, "")
	if err != nil {
		_ = authority.Close()
		t.Fatal(err)
	}
	defer func() { _ = runtime.Close() }()
	r := runtime.tarp
	derived, err := tarp.Open(t.TempDir(), tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = derived.Close() }()
	config := tarpSearchStartup{
		corpus: "test", fingerprint: "project/v1",
		project:        func(_ string, v *pb.Vertex) search.Document { return search.Text(v.GetString_()) },
		analyzer:       search.NewAnalyzer(nil, search.WhitespaceTokenizer{}, nil),
		maxChangeBytes: 1 << 20, maxChangeKeys: 64,
		options: []search.IndexOption{search.WithAnalysisLimits(search.SearchAnalysisLimits{MaxLiveTerms: 1})},
	}
	if _, err := r.openDiskSearch(ctx, derived, config); err != nil {
		t.Fatal(err)
	}
	r.diskAdmission = true
	write := func(seq uint64, key, text string, expiry time.Time) error {
		t.Helper()
		node := r.db.NodeID()
		clock := r.clock.Now()
		vertex := &pb.Vertex{Key: key, Value: &pb.Vertex_String_{String_: text}}
		tx, err := runtime.graph.BeginVertexPut([]graphcache.VertexItem[string, *pb.Vertex]{
			{Key: key, Value: vertex, Expiration: expiry},
		}, clock, false)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Abort()
		entry := mutationlog.Entry{Seq: seq, HLC: clock, Op: &pb.Mutation{
			Seq: seq, Origin: node[:], Hlc: hlcToProto(clock),
			Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertices{PutVertices: &pb.PutVerticesRequest{Vertices: []*pb.Vertex{vertex}}}},
		}}
		if err := r.write(entry, tx); err != nil {
			return err
		}
		tx.Commit()
		return nil
	}
	if err := write(1, "first", "alpha", time.Time{}); err != nil {
		t.Fatal(err)
	}
	before, err := r.db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = before.Close() }()
	// The worker may hold its reader gate while it validates a future Graph
	// selector. Admission under the Graph mutation lock must still finish.
	r.diskSearch.mu.Lock()
	err = write(2, "second", "beta", time.Time{})
	r.diskSearch.mu.Unlock()
	var definite *mutationlog.DefiniteWALAbort
	var limit *search.AnalysisLimitError
	if !errors.As(err, &definite) || !errors.As(err, &limit) || limit.Kind != search.LimitLiveTerms || limit.Got != 2 {
		t.Fatalf("staged rejection did not preserve its typed reason: %v", err)
	}
	after, err := r.db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = after.Close() }()
	if after.Revision != before.Revision || after.Generation != before.Generation {
		t.Fatal("rejected admission committed a Graph revision")
	}
	if cut, err := readTarpSearchCut(ctx, after.Snapshot, after.Generation); err != nil || cut.last != 1 {
		t.Fatalf("rejected admission changed the Search journal: %+v, %v", cut, err)
	}
	rows, err := diskRows()
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := rows.GetVertex(after.Snapshot, after.Generation, "second"); err != nil || found {
		t.Fatalf("rejected Vertex reached Tarp: %v %v", found, err)
	}
	if _, found := runtime.graph.GetVertex("second"); found {
		t.Fatal("rejected Vertex escaped staged rollback")
	}
	if err := write(2, "second", "beta", time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("born-expired postimage consumed live budget: %v", err)
	}
	if err := write(3, "first", "beta", time.Time{}); err != nil {
		t.Fatalf("replacing the only live term did not free its previous capacity: %v", err)
	}
}

func TestTarpSearchAdmissionPreservesReceiptPutRejectionAndRetry(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	graphDir, searchDir := filepath.Join(root, "graph"), filepath.Join(root, "search")
	cfg := durableRuntimeTestConfig("")
	cfg.ConfigureGraph = func(graph *graphcache.GraphCache[string, *pb.Vertex]) error {
		graph.EnableStorageKeyPrefixIndex(func(key string) string { return key })
		return nil
	}
	plan := tarpSearchStartup{
		corpus: "test", fingerprint: "project/v1",
		project:        func(_ string, v *pb.Vertex) search.Document { return search.Text(v.GetString_()) },
		analyzer:       search.NewAnalyzer(nil, search.WhitespaceTokenizer{}, nil),
		maxChangeBytes: 1 << 20, maxChangeKeys: 64,
		options: []search.IndexOption{search.WithAnalysisLimits(search.SearchAnalysisLimits{MaxLiveTerms: 1})},
	}
	runtime, executor := openTarpReceiptTestRuntime(t, graphDir, cfg, true)
	derived, err := tarp.Open(searchDir, tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = derived.Close() })
	id, err := runtime.tarp.openDiskSearch(ctx, derived, plan)
	if err != nil {
		t.Fatal(err)
	}
	runtime.tarp.diskAdmission = true

	request := func(seed byte, key, text string) *pb.PutVerticesRequest {
		return &pb.PutVerticesRequest{
			Vertices:       []*pb.Vertex{{Key: key, Value: &pb.Vertex_String_{String_: text}}},
			ReceiptContext: publicReceiptContext(t, runtime, seed, 1),
		}
	}
	if _, err := executor.PutVertices(ctx, request(0x21, "first", "alpha")); err != nil {
		t.Fatal(err)
	}
	witness := func() (cut tarpSearchCut, durable TarpBackupWitness, revision uint64) {
		t.Helper()
		view, err := runtime.GraphStore().Read()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = view.Close() }()
		cut, err = readTarpSearchCut(ctx, view.Snapshot, view.Generation)
		if err != nil {
			t.Fatal(err)
		}
		durable, err = runtime.TarpBackupWitness(view.Snapshot, view.Generation)
		if err != nil {
			t.Fatal(err)
		}
		return cut, durable, view.Revision
	}
	cut, durable, revision := witness()
	proposed := request(0x31, "second", "beta")
	result, err := executor.PutVertices(ctx, proposed)
	if result != nil || connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("receipt Put admission = %v, %v", result, err)
	}
	detail := searchErrorDetail(t, err)
	if detail.GetReason() != pb.SearchErrorReason_SEARCH_INDEX_BUDGET_EXHAUSTED ||
		detail.GetWorkKind() != string(search.LimitLiveTerms) {
		t.Fatalf("receipt Put lost the exact capacity failure: %v", detail)
	}
	cutAfter, durableAfter, revisionAfter := witness()
	wantRevision := revision
	if durableAfter.ReceiptHighWaterMillis != durable.ReceiptHighWaterMillis {
		// Receipt admission advances its durable monotone clock independently
		// of whether the attempted mutation eventually commits or aborts.
		if durableAfter.ReceiptHighWaterMillis < durable.ReceiptHighWaterMillis {
			t.Fatal("rejection moved the receipt clock backwards")
		}
		wantRevision++
		durableAfter.ReceiptHighWaterMillis = durable.ReceiptHighWaterMillis
	}
	if cutAfter != cut || durableAfter != durable || revisionAfter != wantRevision ||
		executor.LocalSeq(cfg.NodeID) != 1 || executor.receiptCommitFaulted {
		t.Fatal("definite Search abort changed graph, relay, or receipt data")
	}
	if _, live := runtime.graph.GetVertex("second"); live {
		t.Fatal("deferred Search rejection escaped graph rollback")
	}
	status, err := executor.GetReceiptStatuses(ctx, &pb.GetReceiptStatusesRequest{OperationIds: proposed.ReceiptContext.OperationIds})
	if err != nil || len(status.GetStatuses()) != 1 || status.GetStatuses()[0].GetReceipt() != nil {
		t.Fatalf("rejected receipt became durable: %v, %v", status, err)
	}
	// Replace the only live term to release capacity. Reuse the exact request
	// and operation ID that were previously rejected.
	if _, err := executor.PutVertices(ctx, request(0x41, "first", "beta")); err != nil {
		t.Fatal(err)
	}
	accepted, err := executor.PutVertices(ctx, proposed)
	if err != nil || len(accepted.GetOutcomes()) != 1 || accepted.GetOutcomes()[0] != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
		t.Fatalf("retry = %v, %v", accepted, err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if err := derived.Close(); err != nil {
		t.Fatal(err)
	}
	runtime, executor = openTarpReceiptTestRuntime(t, graphDir, cfg, false)
	derived, err = tarp.OpenExisting(ctx, searchDir, tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := runtime.tarp.openDiskSearch(ctx, derived, plan)
	if err != nil || reopened != id {
		t.Fatalf("could not reopen the graph-selected Search base: %d, %v", reopened, err)
	}
	runtime.tarp.diskAdmission = true
	repeated, err := executor.PutVertices(ctx, proposed)
	if err != nil || !proto.Equal(accepted, repeated) || executor.LocalSeq(cfg.NodeID) != 3 {
		t.Fatalf("reopen changed receipt retry: %v, %v", repeated, err)
	}
	vertex, err := executor.GetVertex(ctx, &pb.GetVertexRequest{Key: "second"})
	if err != nil || vertex.GetVertex().GetString_() != "beta" {
		t.Fatalf("reopen lost the accepted Vertex: %v, %v", vertex, err)
	}
	// A local search budget cannot invalidate a peer's already accepted Graph
	// effect. The graph converges and Search reports its exact over-limit state.
	peer := hlc.NodeID{0x45}
	stamp := hlc.Timestamp{WallNs: time.Now().UnixNano(), NodeID: peer}
	if err := executor.ApplyMutation(ctx, &pb.Mutation{
		Origin: peer[:], Seq: 1, Hlc: hlcToProto(stamp),
		Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{
			Vertex: &pb.Vertex{Key: "peer", Value: &pb.Vertex_String_{String_: "gamma"}},
		}}},
	}); err != nil {
		t.Fatalf("local Search admission rejected a committed peer effect: %v", err)
	}
	if got, err := executor.GetVertex(ctx, &pb.GetVertexRequest{Key: "peer"}); err != nil || got.GetVertex().GetString_() != "gamma" {
		t.Fatalf("peer effect did not reach Graph: %v, %v", got, err)
	}
	captured, err := (searchReader{service: executor}).Capture(ctx, query.Spec{Query: "beta", Limit: 5})
	if err != nil || !errors.Is(captured.BackendError, search.ErrIndexIncomplete) || len(captured.Hits) != 0 {
		t.Fatalf("over-budget peer cut emitted Search results: %+v, %v", captured, err)
	}

}
