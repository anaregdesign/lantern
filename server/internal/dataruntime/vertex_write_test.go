package dataruntime

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/graphstorage"
	"github.com/anaregdesign/tarp"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestGraphFirstVertexWritesUseCommittedCut(t *testing.T) {
	ctx := t.Context()
	rt, s := openTarpTestRuntime(t, t.TempDir(), 16)
	rows, err := graphstorage.Rows()
	if err != nil {
		t.Fatal(err)
	}
	payload := func(key, value string) *pb.Vertex {
		return &pb.Vertex{Key: key, Value: &pb.Vertex_String_{String_: value}}
	}
	const onlyStored, current = "disk-only", "peer-floor"
	if _, err := s.PutVertex(ctx, &pb.PutVertexRequest{Vertex: payload(current, "original")}); err != nil {
		t.Fatal(err)
	}
	peer := hlc.NodeID{7}
	peerWall := rt.clock.Now().WallNs + int64(time.Second)
	// Deliberately separate these planning inputs from the GraphCache
	// mirror to prove the public local and peer paths consult Tarp.
	if err := rt.GraphStore().UpdateRows(ctx, func(gen uint64, _ *tarp.Snapshot, w graphstore.Writer) error {
		return errors.Join(
			rows.PutVertex(w, gen, onlyStored, graphstore.Vertex[*pb.Vertex]{Value: payload(onlyStored, "original")}),
			rows.PutVertex(w, gen, current, graphstore.Vertex[*pb.Vertex]{Value: payload(current, "original"), HLC: hlc.Timestamp{WallNs: peerWall + 2, NodeID: peer}}),
		)
	}); err != nil {
		t.Fatal(err)
	}
	out, err := s.PutVertex(ctx, &pb.PutVertexRequest{Vertex: payload(onlyStored, "replacement"), IfAbsent: true})
	if err != nil || out.GetOutcome() != pb.PutOutcome_PUT_OUTCOME_CONDITION_NOT_MET {
		t.Fatalf("conditional Put = %v, %v", out, err)
	}
	if err := s.ApplyMutation(ctx, &pb.Mutation{
		Origin: peer[:], Seq: 1, Hlc: newHLC(peerWall, peer),
		Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{Vertex: payload(current, "stale")}}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{onlyStored, current} {
		got, err := s.GetVertex(ctx, &pb.GetVertexRequest{Key: key})
		if err != nil || got.GetVertex().GetString_() != "original" {
			t.Fatalf("Vertex %q = %v, %v", key, got, err)
		}
	}
	if s.LocalSeq(peer) != 1 {
		t.Fatal("a superseded peer Put did not advance its origin")
	}
	s.WithTombstoneTTL(time.Hour)
	if err := s.ApplyMutation(ctx, &pb.Mutation{
		Origin: peer[:], Seq: 2, Hlc: newHLC(peerWall+1, peer),
		TombstoneExpiration: timestamppb.New(time.Unix(0, peerWall+1).Add(time.Minute)),
		Op:                  &pb.MutationOp{Op: &pb.MutationOp_DeleteVertex{DeleteVertex: &pb.DeleteVertexRequest{Key: current}}},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetVertex(ctx, &pb.GetVertexRequest{Key: current})
	if err != nil || got.GetVertex().GetString_() != "original" || s.LocalSeq(peer) != 2 {
		t.Fatalf("superseded peer Delete altered the Vertex or skipped origin publication: %v, %v", got, err)
	}
	deleted, err := s.DeleteVertices(ctx, &pb.DeleteVerticesRequest{Keys: []string{onlyStored, onlyStored}})
	if err != nil || deleted.GetDeleted() != 1 || !slices.Equal(deleted.GetExisted(), []bool{true, false}) {
		t.Fatalf("duplicate Delete from the committed cut = %v, %v", deleted, err)
	}
	if _, err := s.GetVertex(ctx, &pb.GetVertexRequest{Key: onlyStored}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("deleted Vertex remains present: %v", err)
	}
}

func TestGraphFirstVertexPutPreservesAppliedPendingEffect(t *testing.T) {
	ctx := t.Context()
	rt, s := openTarpTestRuntime(t, t.TempDir(), 16)
	stage := rt.tarp.stage
	rt.tarp.stage = func(context.Context, uint64, *tarp.Snapshot, graphstore.Writer, graphcache.StorageKeys[string], graphcache.StorageRowWriter[string, *pb.Vertex]) error {
		return &tarp.OpError{Operation: "apply", Phase: "prepare", Outcome: tarp.NotApplied, Err: errors.New("injected write rejection")}
	}
	const key = "pending"
	peer := hlc.NodeID{7}
	remote := &pb.Mutation{
		Origin: peer[:], Seq: 1, Hlc: newHLC(time.Now().UnixNano(), peer),
		Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{
			Vertex: &pb.Vertex{Key: key, Value: &pb.Vertex_String_{String_: "peer"}},
		}}},
	}
	if err := s.ApplyMutation(ctx, remote); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("failed publication = %v", err)
	}
	rt.tarp.stage = stage
	if p := s.pendingMutations[peer][1]; p == nil || !p.applied || s.publicationFaultCount != 1 || s.LocalSeq(peer) != 0 {
		t.Fatalf("original peer effect was not retained for repair: %+v faults=%d seq=%d", p, s.publicationFaultCount, s.LocalSeq(peer))
	}
	rows, err := graphstorage.Rows()
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.GraphStore().WithCommittedSnapshot(func(gen uint64, snap *tarp.Snapshot) error {
		if _, found, err := rows.GetVertexMetadata(snap, gen, key); found || err != nil {
			t.Fatalf("pending key in disk cut: found=%t err=%v", found, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	out, err := s.PutVertex(ctx, &pb.PutVertexRequest{IfAbsent: true, Vertex: &pb.Vertex{
		Key: key, Value: &pb.Vertex_String_{String_: "local"},
	}})
	if err != nil || out.GetOutcome() != pb.PutOutcome_PUT_OUTCOME_CONDITION_NOT_MET || s.LocalSeq(rt.clock.NodeID()) != 0 {
		t.Fatalf("conditional Put over the pending effect = %v, %v", out, err)
	}
	if err := s.ApplyMutation(ctx, remote); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetVertex(ctx, &pb.GetVertexRequest{Key: key})
	if err != nil || got.GetVertex().GetString_() != "peer" || s.LocalSeq(peer) != 1 || s.publicationFaultCount != 0 {
		t.Fatalf("exact peer repair = %v, %v", got, err)
	}
}

func newStagedGraphFirstVertexRuntime(t *testing.T, dir string) (*ServingRuntime, *Executor) {
	t.Helper()
	graph := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](time.Hour)
	rt, err := OpenGraphDatabase(t.Context(), dir, hlc.NodeID{1}, graph, mutationlog.Options{Capacity: 16}, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt, rt.NewExecutor(nil)
}

func TestStagedGraphFirstVertexRepairWithNewerPendingPeer(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	rt, s := newStagedGraphFirstVertexRuntime(t, dir)
	stage := rt.tarp.stage
	fail := &tarp.OpError{Operation: "apply", Phase: "prepare", Outcome: tarp.NotApplied, Err: errors.New("injected write rejection")}
	var approved *localVertexWALApproval
	var repaired bool
	rt.tarp.stage = func(ctx context.Context, gen uint64, snap *tarp.Snapshot, batch graphstore.Writer,
		keys graphcache.StorageKeys[string], writer graphcache.StorageRowWriter[string, *pb.Vertex],
	) error {
		if err := stage(ctx, gen, snap, batch, keys, writer); err != nil {
			return err
		}
		local, ok := writer.(*localVertexWALWriter)
		if !ok {
			// The peer applies its effect while the local append is pending.
			return fail
		}
		if local.repairing {
			if local.approval != approved || !local.matches(approved.effect) || !local.approval.diskApproved {
				t.Fatal("repair lacks the original Graph/Search approval")
			}
			copy := *approved.effect
			if local.matches(&copy) {
				t.Fatal("a separate write borrowed the original approval")
			}
			repaired = true
			return nil
		}
		// Model successful admission at the original stage, then a definite
		// failure before graph, relay or origin rows reach disk.
		if approved != nil {
			t.Fatal("a retry re-ran the original admission")
		}
		approved = local.approval
		approved.diskRequired, approved.diskApproved = true, true
		return fail
	}
	const key = "item"
	local := &pb.PutVertexRequest{IfAbsent: true, Vertex: &pb.Vertex{
		Key: key, Value: &pb.Vertex_String_{String_: "local"},
	}}
	if _, err := s.PutVertex(ctx, local); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("failed local publication = %v", err)
	}
	pending := s.pendingLocalMutation
	if pending == nil || approved == nil || pending.walOp != approved.effect || pending.vertexApproval != approved {
		t.Fatal("definite failure did not retain the approved original operation")
	}
	origin := rt.clock.NodeID()
	peer := hlc.NodeID{7}
	remote := &pb.Mutation{
		Origin: peer[:], Seq: 1, Hlc: newHLC(pending.mutation.GetHlc().GetWallNs()+1, peer),
		Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{
			Vertex: &pb.Vertex{Key: key, Value: &pb.Vertex_String_{String_: "peer"}},
		}}},
	}
	if err := s.ApplyMutation(ctx, remote); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("failed peer publication = %v", err)
	}
	out, err := s.PutVertex(ctx, local)
	if err != nil || out.GetOutcome() != pb.PutOutcome_PUT_OUTCOME_CONDITION_NOT_MET || !repaired {
		t.Fatalf("retry = %v, %v, repaired=%t", out, err, repaired)
	}
	if s.pendingLocalMutation != nil || s.LocalSeq(origin) != 1 || s.LocalSeq(peer) != 0 || s.publicationFaultCount != 1 {
		t.Fatal("local repair published the wrong origin or cleared the peer's pending fault")
	}
	rows, err := graphstorage.Rows()
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.GraphStore().WithCommittedSnapshot(func(gen uint64, snap *tarp.Snapshot) error {
		stored, found, err := rows.GetVertex(snap, gen, key)
		if err != nil || !found || stored.Value.GetString_() != "peer" {
			t.Fatalf("repair overwrote the newer applied postimage: %+v, %v", stored, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rt.tarp.stage = stage
	if err := s.ApplyMutation(ctx, remote); err != nil || s.LocalSeq(peer) != 1 || s.publicationFaultCount != 0 {
		t.Fatalf("peer repair = %v, seq=%d faults=%d", err, s.LocalSeq(peer), s.publicationFaultCount)
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	_, reopened := newStagedGraphFirstVertexRuntime(t, dir)
	stored, err := reopened.GetVertex(ctx, &pb.GetVertexRequest{Key: key})
	if err != nil || stored.GetVertex().GetString_() != "peer" || reopened.LocalSeq(origin) != 1 || reopened.LocalSeq(peer) != 1 {
		t.Fatalf("reopen = %v, %v", stored, err)
	}
}

type rejectedLocalGraphWrite struct{}

func (rejectedLocalGraphWrite) Error() string { return "injected admission rejection" }
func (e rejectedLocalGraphWrite) graphWriteRejection() error {
	return connect.NewError(connect.CodeResourceExhausted, e)
}

func TestStagedGraphFirstVertexAdmissionRejectionAbortsGraph(t *testing.T) {
	ctx := t.Context()
	rt, s := newStagedGraphFirstVertexRuntime(t, t.TempDir())
	stage := rt.tarp.stage
	rt.tarp.stage = func(ctx context.Context, gen uint64, snap *tarp.Snapshot, batch graphstore.Writer,
		keys graphcache.StorageKeys[string], writer graphcache.StorageRowWriter[string, *pb.Vertex],
	) error {
		local, ok := writer.(*localVertexWALWriter)
		if !ok || local.repairing {
			t.Fatal("fresh write did not supply a reversible staged writer")
		}
		if err := stage(ctx, gen, snap, batch, keys, writer); err != nil {
			return err
		}
		local.approval.diskRequired = true
		return &mutationlog.DefiniteWALAbort{Cause: rejectedLocalGraphWrite{}}
	}
	request := &pb.PutVertexRequest{IfAbsent: true, Vertex: &pb.Vertex{
		Key: "blocked", Value: &pb.Vertex_String_{String_: "value"},
	}}
	if _, err := s.PutVertex(ctx, request); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("admission rejection = %v", err)
	}
	if s.pendingLocalMutation != nil || s.publicationFaultCount != 0 || s.LocalSeq(rt.clock.NodeID()) != 0 {
		t.Fatal("an unaccepted write became repairable or advanced its origin")
	}
	if _, live := rt.graph.GetVertex("blocked"); live {
		t.Fatal("admission rejection left the staged GraphCache candidate visible")
	}
	rt.tarp.stage = stage
	if out, err := s.PutVertex(ctx, request); err != nil || out.GetOutcome() != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
		t.Fatalf("retry after rejected admission = %v, %v", out, err)
	}
}
