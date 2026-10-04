package service

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestEdgeCreateWALRestoresOriginalDecisionsWithoutResurrection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "create.wal")
	wal, err := mutationlog.CreateFileWAL(path, encodeReceiptWALUnion)
	if err != nil {
		t.Fatal(err)
	}
	f := newReceiptEdgeDeleteFixture(t, wal)
	runtime := bindPublicReceiptFixtureForConcurrencyTest(t, f)
	future := timestamppb.New(time.Now().Add(time.Hour))
	if _, err := f.service.PutVertices(t.Context(), &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "a", Expiration: future}, {Key: "b", Expiration: future}, {Key: "c", Expiration: future}}}); err != nil {
		t.Fatal(err)
	}
	req := &pb.CreateEdgesRequest{Edges: []*pb.Edge{{Tail: "a", Head: "b", Weight: 2}, {Tail: "a", Head: "b", Weight: 9}, {Tail: "a", Head: "missing", Weight: 2}, {Tail: "a", Head: "c", Weight: 3}}, ReceiptContext: publicReceiptContext(t, runtime, 0x51, 4)}
	first, err := f.service.CreateEdges(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.DeleteEdge(t.Context(), &pb.DeleteEdgeRequest{Tail: "a", Head: "b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.PutVertex(t.Context(), &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "missing", Expiration: future}}); err != nil {
		t.Fatal(err)
	}
	if err := f.log.Close(); err != nil {
		t.Fatal(err)
	}
	if err := wal.Close(); err != nil {
		t.Fatal(err)
	}
	lease, err := mutationlog.AcquireFileWALLease(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	config := runtime.receipt.policy
	candidate, err := stageEffectCompleteReceiptWALCandidate(lease, config, time.Now(), mutationlog.Options{Capacity: 32}, time.Hour, func(g *graphcache.GraphCache[string, *pb.Vertex]) error {
		g.EnablePrefixIndex(func(key string) string { return key })
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, live := candidate.graph.GetWeight("a", "b"); live {
		t.Fatal("restore resurrected later-deleted Create")
	}
	if _, live := candidate.graph.GetWeight("a", "missing"); live {
		t.Fatal("restore re-evaluated an originally rejected item")
	}
	if weight, live := candidate.graph.GetWeight("a", "c"); !live || weight != 3 {
		t.Fatal("accepted live Create lost", weight, live)
	}
	ids := make([]mutationreceipt.ID, len(req.ReceiptContext.OperationIds))
	for i, raw := range req.ReceiptContext.OperationIds {
		ids[i], err = mutationreceipt.DecodeID(raw)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, observations, err := candidate.receipts.ObserveMany(ids, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for i, observation := range observations {
		if observation.Status != mutationreceipt.Confirmed || observation.Receipt.Kind != mutationreceipt.CreateEdge || len(observation.Receipt.Result) != 1 || pb.CreateEdgeOutcome(observation.Receipt.Result[0]) != first.GetOutcomes()[i] {
			t.Fatal(i, observation)
		}
	}
}

func TestEdgeCreateWALRejectsMalformedOrDowngradedEvidence(t *testing.T) {
	f := newReceiptEdgeDeleteFixture(t, nil)
	runtime := bindPublicReceiptFixtureForConcurrencyTest(t, f)
	for _, key := range []string{"a", "b"} {
		f.cache.PutVertex(key, &pb.Vertex{Key: key})
	}
	req := &pb.CreateEdgesRequest{Edges: []*pb.Edge{{Tail: "a", Head: "b", Weight: 2}}, ReceiptContext: publicReceiptContext(t, runtime, 0x61, 1)}
	if _, err := f.service.CreateEdges(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	entries := f.log.RetainedEntries()
	if len(entries) != 1 {
		t.Fatal(entries)
	}
	e, ok := entries[0].Op.(*edgeCreateEnvelope)
	if !ok {
		t.Fatalf("unexpected payload %T", entries[0].Op)
	}
	raw, err := encodeReceiptWALUnion(e)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeReceiptWALUnion(raw)
	if err != nil {
		t.Fatal(err)
	}
	copy := decoded.(*edgeCreateEnvelope)
	if !proto.Equal(e.Mutation, copy.Mutation) || !reflect.DeepEqual(e.Receipts, copy.Receipts) {
		t.Fatal("Create evidence roundtrip drift")
	}
	if _, err := encodeReceiptWALUnion(e.Mutation); !errors.Is(err, errReceiptWALUnion) {
		t.Fatal("raw graph downgraded Create", err)
	}
	bad := append([]byte(nil), raw...)
	bad[8] = receiptWALUnionGraph
	if _, err := decodeReceiptWALUnion(bad); !errors.Is(err, errReceiptWALUnion) {
		t.Fatal("graph decoder accepted Create arm", err)
	}
	for name, mutate := range map[string]func(*pb.Mutation){
		"unspecified": func(m *pb.Mutation) { m.GetOp().GetEdgeCreateEffect().Items[0].Outcome = 0 },
		"result drift": func(m *pb.Mutation) {
			m.GetOp().GetEdgeCreateEffect().Items[0].Receipt.OriginalResult = &pb.ReceiptResult{Result: &pb.ReceiptResult_CreateEdgeOutcome{CreateEdgeOutcome: pb.CreateEdgeOutcome_CREATE_EDGE_OUTCOME_EDGE_EXISTS}}
		},
		"partial receipt": func(m *pb.Mutation) { m.GetOp().GetEdgeCreateEffect().PolicyFingerprint = nil },
		"invalid source":  func(m *pb.Mutation) { m.GetOp().GetEdgeCreateEffect().Items[0].Original.Weight = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			m := proto.Clone(e.Mutation).(*pb.Mutation)
			mutate(m)
			if _, err := decodeEdgeCreateMutation(m); err == nil {
				t.Fatal("invalid Create WAL accepted")
			}
		})
	}
	before := f.service.origins.States()
	if err := f.service.ApplyMutation(t.Context(), proto.Clone(e.Mutation).(*pb.Mutation)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("peer accepted local absence proof", err)
	}
	if !reflect.DeepEqual(before, f.service.origins.States()) {
		t.Fatal("rejected HA apply advanced origin")
	}
}
