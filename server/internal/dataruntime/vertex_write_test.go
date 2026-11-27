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
