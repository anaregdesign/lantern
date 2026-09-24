package service

import (
	"bytes"
	"encoding/binary"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func addEffectMutation(op *pb.MutationOp) *pb.Mutation {
	origin := bytes.Repeat([]byte{0x61}, len(hlc.NodeID{}))
	return &pb.Mutation{
		Origin: origin, Seq: 3,
		Hlc: &pb.HLCTimestamp{WallNs: time.Now().UnixNano(), NodeId: append([]byte(nil), origin...)},
		Op:  op,
	}
}

func addEffectHLC(m *pb.Mutation) hlc.Timestamp {
	var node hlc.NodeID
	copy(node[:], m.GetOrigin())
	return hlc.Timestamp{WallNs: m.GetHlc().GetWallNs(), Logical: m.GetHlc().GetLogical(), NodeID: node}
}

func TestGraphAddEffectWALRoundTripPreservesReceiverDecision(t *testing.T) {
	edge := func(tail string) *pb.Edge {
		return &pb.Edge{Tail: tail, Head: "head", Weight: 2, Expiration: timestamppb.New(time.Now().Add(time.Hour))}
	}
	for _, tc := range []struct {
		name     string
		op       *pb.MutationOp
		accepted []bool // compact non-nil wire-slot order
		want     []uint32
	}{
		{"singular accepted", &pb.MutationOp{Op: &pb.MutationOp_AddEdge{AddEdge: &pb.AddEdgeRequest{Edge: edge("live")}}}, []bool{true}, []uint32{0}},
		{"singular rejected", &pb.MutationOp{Op: &pb.MutationOp_AddEdge{AddEdge: &pb.AddEdgeRequest{Edge: edge("fenced")}}}, []bool{false}, nil},
		{"singular nil", &pb.MutationOp{Op: &pb.MutationOp_AddEdge{AddEdge: &pb.AddEdgeRequest{}}}, nil, nil},
		{"plural mixed and nil", &pb.MutationOp{Op: &pb.MutationOp_AddEdges{AddEdges: &pb.AddEdgesRequest{
			Edges: []*pb.Edge{nil, edge("live"), edge("live"), edge("fenced"), edge("next")},
		}}}, []bool{true, false, false, true}, []uint32{1, 4}},
		{"plural zero accepted", &pb.MutationOp{Op: &pb.MutationOp_AddEdges{AddEdges: &pb.AddEdgesRequest{
			Edges: []*pb.Edge{nil, edge("fenced")},
		}}}, []bool{false}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := addEffectMutation(tc.op)
			e, err := newGraphAddEffectEnvelope(m, tc.accepted)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(e.AcceptedIndexes, tc.want) {
				t.Fatalf("accepted = %v, want %v", e.AcceptedIndexes, tc.want)
			}
			original := proto.Clone(m)
			m.Seq++
			if !proto.Equal(e.GraphMutation(), original) {
				t.Fatal("envelope aliased caller mutation")
			}
			raw, err := encodeReceiptWALUnion(e)
			if err != nil {
				t.Fatal(err)
			}
			if raw[8] != receiptWALUnionGraphAddEffect {
				t.Fatalf("union kind = %d", raw[8])
			}
			decoded, err := decodeReceiptWALUnion(raw)
			if err != nil {
				t.Fatal(err)
			}
			got, ok := decoded.(*graphAddEffectEnvelope)
			if !ok || !proto.Equal(got.GraphMutation(), original) || !reflect.DeepEqual(got.AcceptedIndexes, tc.want) {
				t.Fatalf("round trip = %+v, want %v", decoded, tc.want)
			}
			if projected, ok := graphMutationFromLog(got); !ok || !proto.Equal(projected, original) {
				t.Fatal("private sidecar changed Subscribe/identity projection")
			}
			if err := validateReceiptWALUnionEntry(mutationlog.Entry{Seq: 9, HLC: addEffectHLC(got.Mutation), Op: got}); err != nil {
				t.Fatal(err)
			}
			badHLC := addEffectHLC(got.Mutation)
			badHLC.WallNs++
			if err := validateReceiptWALUnionEntry(mutationlog.Entry{Seq: 9, HLC: badHLC, Op: got}); !errors.Is(err, errReceiptWALUnion) {
				t.Fatalf("mismatched FileWAL HLC accepted: %v", err)
			}
		})
	}
}

func TestGraphAddEffectWALCapturesFinalGraphCacheOutcomes(t *testing.T) {
	c := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	future := time.Now().Add(time.Hour)
	var node hlc.NodeID
	copy(node[:], bytes.Repeat([]byte{0x61}, len(node)))
	older := hlc.Timestamp{WallNs: time.Now().UnixNano(), NodeID: node}
	newer := older
	newer.WallNs += 10
	c.DeleteEdgeHLC("fenced", "head", newer, future)
	if !c.PutEdgeWithExpirationHLC("put", "head", 7, future, newer) {
		t.Fatal("newer Put was rejected")
	}
	items := []graphcache.EdgeItem[string]{
		{Tail: "live", Head: "head", Weight: 1, Expiration: future, ContribID: graphcache.ContribID{1}},
		{Tail: "live", Head: "head", Weight: 1, Expiration: future, ContribID: graphcache.ContribID{1}},
		{Tail: "fenced", Head: "head", Weight: 2, Expiration: future, ContribID: graphcache.ContribID{2}},
		{Tail: "put", Head: "head", Weight: 2, Expiration: future, ContribID: graphcache.ContribID{3}},
		{Tail: "live", Head: "head", Weight: 3, Expiration: future, ContribID: graphcache.ContribID{4}},
	}
	weights, accepted, deduped := c.AddEdgesWithExpirationContribHLCResults(items, older)
	if !reflect.DeepEqual(accepted, []bool{true, false, false, false, true}) || deduped != 3 ||
		!reflect.DeepEqual(weights, []float32{1, 1, 0, 7, 4}) {
		t.Fatalf("receiver decisions = %v/%v/%d", weights, accepted, deduped)
	}
	if _, ok := c.GetVertex("fenced"); ok {
		t.Fatal("rejected Add revived its endpoint")
	}
	// The server compacts nil wire slots before GraphCache; the envelope
	// restores original indexes (also used by synthesized ContribIDs).
	wire := []*pb.Edge{nil}
	for _, item := range items {
		wire = append(wire, &pb.Edge{
			Tail: item.Tail, Head: item.Head, Weight: item.Weight,
			Expiration: timestamppb.New(item.Expiration),
		})
	}
	m := addEffectMutation(&pb.MutationOp{Op: &pb.MutationOp_AddEdges{AddEdges: &pb.AddEdgesRequest{Edges: wire}}})
	m.Hlc.WallNs = older.WallNs
	e, err := newGraphAddEffectEnvelope(m, accepted)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(e.AcceptedIndexes, []uint32{1, 5}) {
		t.Fatalf("accepted wire indexes = %v, want [1 5]", e.AcceptedIndexes)
	}
	path := filepath.Join(t.TempDir(), "add.wal")
	wal, err := mutationlog.CreateFileWAL(path, encodeReceiptWALUnion)
	if err != nil {
		t.Fatal(err)
	}
	if err := wal.Write(mutationlog.Entry{Seq: 1, HLC: addEffectHLC(m), Op: e}); err != nil {
		t.Fatal(err)
	}
	if err := wal.Close(); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := mutationlog.ReplayFileWAL(path, decodeReceiptWALUnion, func(entry mutationlog.Entry) error {
		rows++
		if err := validateReceiptWALUnionEntry(entry); err != nil {
			return err
		}
		got, ok := entry.Op.(*graphAddEffectEnvelope)
		if !ok || !reflect.DeepEqual(got.AcceptedIndexes, []uint32{1, 5}) || got.Mutation.GetOp().GetAddEdges().GetEdges()[0] != nil {
			t.Fatalf("FileWAL Add effect = %+v", entry.Op)
		}
		return nil
	}); err != nil || rows != 1 {
		t.Fatalf("FileWAL replay = %d rows, %v", rows, err)
	}
}

func TestGraphAddEffectWALRejectsMalformedEvidence(t *testing.T) {
	m := addEffectMutation(&pb.MutationOp{Op: &pb.MutationOp_AddEdges{AddEdges: &pb.AddEdgesRequest{
		Edges: []*pb.Edge{nil, {Tail: "tail", Head: "head"}, {Tail: "other", Head: "head"}},
	}}})
	if _, err := newGraphAddEffectEnvelope(m, []bool{true}); !errors.Is(err, errReceiptWALUnion) {
		t.Fatalf("outcome count drift accepted: %v", err)
	}
	if _, err := newGraphAddEffectEnvelope(addEffectMutation(&pb.MutationOp{Op: &pb.MutationOp_PutEdge{PutEdge: &pb.PutEdgeRequest{}}}), nil); !errors.Is(err, errReceiptWALUnion) {
		t.Fatalf("non-Add arm accepted: %v", err)
	}
	e, err := newGraphAddEffectEnvelope(m, []bool{true, false})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]uint32{{0}, {3}, {1, 1}, {2, 1}} {
		copyOf := *e
		copyOf.AcceptedIndexes = bad
		if _, err := encodeReceiptWALUnion(&copyOf); !errors.Is(err, errReceiptWALUnion) {
			t.Fatalf("invalid index set %v accepted: %v", bad, err)
		}
	}
	raw, err := encodeReceiptWALUnion(e)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func([]byte){
		func(b []byte) { b[receiptWALUnionHeaderSize+4]++ },       // inner version
		func(b []byte) { b[receiptWALUnionHeaderSize+5] = 1 },     // reserved
		func(b []byte) { b[receiptWALUnionHeaderSize+8] = 0xff },  // graph length
		func(b []byte) { b[receiptWALUnionHeaderSize+12] = 0xff }, // count
		func(b []byte) { b[len(b)-4] = 0xff },                     // accepted index
	} {
		corrupt := bytes.Clone(raw)
		mutate(corrupt)
		if _, err := decodeReceiptWALUnion(corrupt); !errors.Is(err, errReceiptWALUnion) {
			t.Fatalf("corrupt Add effect accepted: %v", err)
		}
	}
	truncated := bytes.Clone(raw[:len(raw)-1])
	binary.BigEndian.PutUint32(truncated[12:16], uint32(len(truncated)-receiptWALUnionHeaderSize))
	if _, err := decodeReceiptWALUnion(truncated); !errors.Is(err, errReceiptWALUnion) {
		t.Fatalf("truncated Add effect accepted: %v", err)
	}
}

func BenchmarkGraphAddEffectWAL(b *testing.B) {
	edges := make([]*pb.Edge, 64)
	accepted := make([]bool, len(edges))
	for i := range edges {
		edges[i] = &pb.Edge{Tail: "tail", Head: "head", Weight: 1}
		accepted[i] = i%3 == 0
	}
	m := addEffectMutation(&pb.MutationOp{Op: &pb.MutationOp_AddEdges{AddEdges: &pb.AddEdgesRequest{Edges: edges}}})
	e, err := newGraphAddEffectEnvelope(m, accepted)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("capture", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := newGraphAddEffectEnvelope(m, accepted); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("codec", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := encodeReceiptWALUnion(e); err != nil {
				b.Fatal(err)
			}
		}
	})
}
