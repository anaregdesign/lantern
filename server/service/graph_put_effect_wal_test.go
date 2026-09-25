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

func graphPutEffectTestMutation(op *pb.MutationOp) *pb.Mutation {
	origin := bytes.Repeat([]byte{0x51}, 16)
	return &pb.Mutation{
		Origin: origin, Seq: 3,
		Hlc: &pb.HLCTimestamp{WallNs: time.Now().UnixNano(), NodeId: append([]byte(nil), origin...)},
		Op:  op,
	}
}

func graphPutEffectTestHLC(m *pb.Mutation) hlc.Timestamp {
	var node hlc.NodeID
	copy(node[:], m.GetOrigin())
	return hlc.Timestamp{WallNs: m.GetHlc().GetWallNs(), Logical: m.GetHlc().GetLogical(), NodeID: node}
}

func TestGraphPutEffectWALRoundTripPreservesReceiverDecision(t *testing.T) {
	future := timestamppb.New(time.Now().Add(time.Hour))
	past := timestamppb.New(time.Now().Add(-time.Hour))
	vertex := func(key string, exp *timestamppb.Timestamp) *pb.Vertex {
		return &pb.Vertex{Key: key, Expiration: exp}
	}
	edge := func(tail, head string, exp *timestamppb.Timestamp) *pb.Edge {
		return &pb.Edge{Tail: tail, Head: head, Weight: 2, Expiration: exp}
	}
	cases := []struct {
		name     string
		op       *pb.MutationOp
		outcomes []graphcache.PutOutcome // compact non-nil wire-slot order
		want     []graphPutAcceptedEffect
	}{
		{"singular Vertex", &pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{Vertex: vertex("v", future)}}},
			[]graphcache.PutOutcome{graphcache.PutOutcomeAppliedAndLive}, []graphPutAcceptedEffect{{0, graphPutEffectLive}}},
		{"plural Vertex with nil and rejected slots", &pb.MutationOp{Op: &pb.MutationOp_PutVertices{PutVertices: &pb.PutVerticesRequest{
			Vertices: []*pb.Vertex{nil, vertex("a", future), vertex("a", future), vertex("expired", past)},
		}}}, []graphcache.PutOutcome{graphcache.PutOutcomeAppliedAndLive, graphcache.PutOutcomeSuperseded, graphcache.PutOutcomeExpired},
			[]graphPutAcceptedEffect{{1, graphPutEffectLive}, {3, graphPutEffectBarrier}}},
		{"singular Edge barrier", &pb.MutationOp{Op: &pb.MutationOp_PutEdge{PutEdge: &pb.PutEdgeRequest{Edge: edge("t", "h", past)}}},
			[]graphcache.PutOutcome{graphcache.PutOutcomeExpired}, []graphPutAcceptedEffect{{0, graphPutEffectBarrier}}},
		{"plural Edge no effect", &pb.MutationOp{Op: &pb.MutationOp_PutEdges{PutEdges: &pb.PutEdgesRequest{
			Edges: []*pb.Edge{nil, edge("t", "h", future)},
		}}}, []graphcache.PutOutcome{graphcache.PutOutcomeSuperseded}, nil},
		{"replicated Vertex live and barrier", &pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutVertices{ReplicatedPutVertices: &pb.ReplicatedPutVertices{
			Entries: []*pb.ReplicatedPutVertex{
				{Outcome: &pb.ReplicatedPutVertex_Live{Live: vertex("v", future)}},
				{Outcome: &pb.ReplicatedPutVertex_CausalBarrier{CausalBarrier: &pb.VertexCausalBarrier{Key: "old"}}},
			},
		}}}, []graphcache.PutOutcome{graphcache.PutOutcomeAppliedAndLive, graphcache.PutOutcomeExpired},
			[]graphPutAcceptedEffect{{0, graphPutEffectLive}, {1, graphPutEffectBarrier}}},
		{"replicated Edge receiver expiry", &pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutEdges{ReplicatedPutEdges: &pb.ReplicatedPutEdges{
			Entries: []*pb.ReplicatedPutEdge{
				{Outcome: &pb.ReplicatedPutEdge_Live{Live: edge("t", "h", future)}},
				{Outcome: &pb.ReplicatedPutEdge_CausalBarrier{CausalBarrier: &pb.EdgeCausalBarrier{Tail: "x", Head: "y"}}},
			},
		}}}, []graphcache.PutOutcome{graphcache.PutOutcomeExpired, graphcache.PutOutcomeSuperseded},
			[]graphPutAcceptedEffect{{0, graphPutEffectBarrier}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := graphPutEffectTestMutation(tc.op)
			e, err := newGraphPutEffectEnvelope(m, tc.outcomes)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(e.Accepted, tc.want) {
				t.Fatalf("accepted = %+v, want %+v", e.Accepted, tc.want)
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
			if raw[8] != receiptWALUnionGraphPutEffect {
				t.Fatalf("union kind = %d", raw[8])
			}
			decoded, err := decodeReceiptWALUnion(raw)
			if err != nil {
				t.Fatal(err)
			}
			got, ok := decoded.(*graphPutEffectEnvelope)
			if !ok || !proto.Equal(got.GraphMutation(), original) || !reflect.DeepEqual(got.Accepted, tc.want) {
				t.Fatalf("round trip = %+v, want %+v", decoded, tc.want)
			}
			if projected, ok := graphMutationFromLog(got); !ok || !proto.Equal(projected, original) {
				t.Fatal("private sidecar changed Subscribe/identity projection")
			}
			if err := validateReceiptWALUnionEntry(mutationlog.Entry{Seq: 9, HLC: graphPutEffectTestHLC(m), Op: got}); err != nil {
				t.Fatal(err)
			}
			badHLC := graphPutEffectTestHLC(m)
			badHLC.WallNs++
			if err := validateReceiptWALUnionEntry(mutationlog.Entry{Seq: 9, HLC: badHLC, Op: got}); !errors.Is(err, errReceiptWALUnion) {
				t.Fatalf("mismatched FileWAL HLC accepted: %v", err)
			}
		})
	}
}

func TestGraphPutEffectWALCapturesFinalGraphCacheOutcomes(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	future := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Hour)
	var node hlc.NodeID
	copy(node[:], bytes.Repeat([]byte{0x51}, len(node)))
	older := hlc.Timestamp{WallNs: 10, NodeID: node}
	newer := hlc.Timestamp{WallNs: 20, NodeID: node}
	if !cache.PutEdgeWithExpirationHLC("stale", "head", 9, future, newer) {
		t.Fatal("failed to seed newer edge")
	}
	cache.DeleteVertices([]string{"stale"})
	cache.DeleteEdgesHLC([]graphcache.EdgeKey[string]{{Tail: "tomb", Head: "head"}}, newer, future)
	if got := cache.PutEdgesWithExpirationHLCOutcomes([]graphcache.EdgeItem[string]{{
		Tail: "barrier", Head: "head", Weight: 1, Expiration: past,
	}}, newer); len(got) != 1 || got[0] != graphcache.PutOutcomeExpired {
		t.Fatalf("failed to seed newer expired barrier: %v", got)
	}
	items := []graphcache.EdgeItem[string]{
		{Tail: "live", Head: "head", Weight: 1, Expiration: future},
		{Tail: "stale", Head: "head", Weight: 2, Expiration: future},
		{Tail: "tomb", Head: "head", Weight: 2, Expiration: future},
		{Tail: "barrier", Head: "head", Weight: 2, Expiration: future},
		{Tail: "expired", Head: "head", Weight: 3, Expiration: past},
		{Tail: "live", Head: "head", Weight: 4, Expiration: future},
	}
	outcomes := cache.PutEdgesWithExpirationHLCOutcomes(items, older)
	wantOutcomes := []graphcache.PutOutcome{
		graphcache.PutOutcomeAppliedAndLive, graphcache.PutOutcomeSuperseded,
		graphcache.PutOutcomeSuperseded, graphcache.PutOutcomeSuperseded,
		graphcache.PutOutcomeExpired, graphcache.PutOutcomeAppliedAndLive,
	}
	if !reflect.DeepEqual(outcomes, wantOutcomes) {
		t.Fatalf("GraphCache outcomes = %v, want %v", outcomes, wantOutcomes)
	}
	for _, tail := range []string{"stale", "tomb", "barrier"} {
		if _, ok := cache.GetVertex(tail); ok {
			t.Fatalf("rejected Edge Put revived %q endpoint", tail)
		}
	}
	if _, ok := cache.GetVertex("expired"); ok {
		t.Fatal("accepted-expired Edge Put created an endpoint")
	}
	wire := []*pb.Edge{nil}
	for _, item := range items {
		wire = append(wire, &pb.Edge{Tail: item.Tail, Head: item.Head, Weight: item.Weight, Expiration: timestamppb.New(item.Expiration)})
	}
	m := graphPutEffectTestMutation(&pb.MutationOp{Op: &pb.MutationOp_PutEdges{PutEdges: &pb.PutEdgesRequest{Edges: wire}}})
	m.Hlc.WallNs = older.WallNs
	e, err := newGraphPutEffectEnvelope(m, outcomes)
	if err != nil {
		t.Fatal(err)
	}
	want := []graphPutAcceptedEffect{{1, graphPutEffectLive}, {5, graphPutEffectBarrier}, {6, graphPutEffectLive}}
	if !reflect.DeepEqual(e.Accepted, want) {
		t.Fatalf("receiver-local accepted effects = %+v, want %+v", e.Accepted, want)
	}
}

func TestGraphPutEffectWALFileFrameKeepsNilSlotsAndAcceptedSubset(t *testing.T) {
	m := graphPutEffectTestMutation(&pb.MutationOp{Op: &pb.MutationOp_PutEdges{PutEdges: &pb.PutEdgesRequest{
		Edges: []*pb.Edge{nil, {Tail: "a", Head: "b"}, {Tail: "a", Head: "b"}, {Tail: "c", Head: "d"}},
	}}})
	e, err := newGraphPutEffectEnvelope(m, []graphcache.PutOutcome{
		graphcache.PutOutcomeAppliedAndLive, graphcache.PutOutcomeSuperseded, graphcache.PutOutcomeExpired,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "put-effect.wal")
	wal, err := mutationlog.CreateFileWAL(path, encodeReceiptWALUnion)
	if err != nil {
		t.Fatal(err)
	}
	if err := wal.Write(mutationlog.Entry{Seq: 1, HLC: graphPutEffectTestHLC(m), Op: e}); err != nil {
		t.Fatal(err)
	}
	if err := wal.Close(); err != nil {
		t.Fatal(err)
	}
	var seen int
	if err := mutationlog.ReplayFileWAL(path, decodeReceiptWALUnion, func(entry mutationlog.Entry) error {
		if err := validateReceiptWALUnionEntry(entry); err != nil {
			return err
		}
		got, ok := entry.Op.(*graphPutEffectEnvelope)
		if !ok || !proto.Equal(got.Mutation, e.Mutation) || !reflect.DeepEqual(got.Accepted, e.Accepted) ||
			got.Mutation.GetOp().GetPutEdges().GetEdges()[0] != nil {
			t.Fatalf("FileWAL Put effect = %+v, want %+v", entry.Op, e)
		}
		seen++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if seen != 1 {
		t.Fatalf("FileWAL replay visited %d records, want 1", seen)
	}
}

func TestGraphPutEffectWALRejectsMalformedEvidence(t *testing.T) {
	m := graphPutEffectTestMutation(&pb.MutationOp{Op: &pb.MutationOp_PutVertices{PutVertices: &pb.PutVerticesRequest{
		Vertices: []*pb.Vertex{nil, {Key: "v"}, {Key: "v"}},
	}}})
	e, err := newGraphPutEffectEnvelope(m, []graphcache.PutOutcome{graphcache.PutOutcomeAppliedAndLive, graphcache.PutOutcomeExpired})
	if err != nil {
		t.Fatal(err)
	}
	body, err := encodeGraphPutEffectWAL(e)
	if err != nil {
		t.Fatal(err)
	}
	graphEnd := graphPutEffectWALHeaderSize + int(binary.BigEndian.Uint32(body[8:12]))
	for _, tc := range []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"short", func(raw []byte) []byte { return raw[:graphPutEffectWALHeaderSize-1] }},
		{"old version", func(raw []byte) []byte { raw[4] = 0; return raw }},
		{"header reserved", func(raw []byte) []byte { raw[5] = 1; return raw }},
		{"graph length", func(raw []byte) []byte { binary.BigEndian.PutUint32(raw[8:12], 1); return raw }},
		{"effect count", func(raw []byte) []byte { binary.BigEndian.PutUint32(raw[12:16], 3); return raw }},
		{"nil accepted slot", func(raw []byte) []byte { binary.BigEndian.PutUint32(raw[graphEnd:graphEnd+4], 0); return raw }},
		{"duplicate accepted index", func(raw []byte) []byte { binary.BigEndian.PutUint32(raw[graphEnd+8:graphEnd+12], 1); return raw }},
		{"out of range index", func(raw []byte) []byte { binary.BigEndian.PutUint32(raw[graphEnd:graphEnd+4], 3); return raw }},
		{"unknown effect", func(raw []byte) []byte { raw[graphEnd+4] = 3; return raw }},
		{"effect reserved", func(raw []byte) []byte { raw[graphEnd+5] = 1; return raw }},
		{"trailing byte", func(raw []byte) []byte { return append(raw, 1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := tc.mutate(append([]byte(nil), body...))
			if _, err := decodeGraphPutEffectWAL(raw); !errors.Is(err, errReceiptWALUnion) {
				t.Fatalf("malformed graph Put effect accepted: %v", err)
			}
		})
	}
	if _, err := newGraphPutEffectEnvelope(m, []graphcache.PutOutcome{graphcache.PutOutcomeAppliedAndLive}); !errors.Is(err, errReceiptWALUnion) {
		t.Fatalf("outcome count drift accepted: %v", err)
	}
	if _, err := newGraphPutEffectEnvelope(m, []graphcache.PutOutcome{0, graphcache.PutOutcomeExpired}); !errors.Is(err, errReceiptWALUnion) {
		t.Fatalf("unknown non-nil outcome accepted: %v", err)
	}
	for _, tc := range []struct {
		name     string
		mutate   func(*pb.Mutation)
		outcomes int
	}{
		{"zero origin sequence", func(m *pb.Mutation) { m.Seq = 0 }, 2},
		{"mismatched origin and HLC", func(m *pb.Mutation) { m.Origin[0] ^= 1 }, 2},
		{"unknown protobuf field", func(m *pb.Mutation) { m.ProtoReflect().SetUnknown([]byte{0x98, 0x06, 0x01}) }, 2},
		{"conditional plural Put", func(m *pb.Mutation) { m.GetOp().GetPutVertices().IfAbsent = true }, 2},
		{"conditional singular Put", func(m *pb.Mutation) {
			m.Op = &pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "v"}, IfAbsent: true}}}
		}, 1},
		{"nil replicated live payload", func(m *pb.Mutation) {
			m.Op = &pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutVertices{ReplicatedPutVertices: &pb.ReplicatedPutVertices{
				Entries: []*pb.ReplicatedPutVertex{{Outcome: &pb.ReplicatedPutVertex_Live{}}},
			}}}
		}, 1},
		{"nil replicated Vertex entry", func(m *pb.Mutation) {
			m.Op = &pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutVertices{ReplicatedPutVertices: &pb.ReplicatedPutVertices{
				Entries: []*pb.ReplicatedPutVertex{nil},
			}}}
		}, 0},
		{"missing replicated Vertex outcome", func(m *pb.Mutation) {
			m.Op = &pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutVertices{ReplicatedPutVertices: &pb.ReplicatedPutVertices{
				Entries: []*pb.ReplicatedPutVertex{{}},
			}}}
		}, 1},
		{"nil replicated Edge entry", func(m *pb.Mutation) {
			m.Op = &pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutEdges{ReplicatedPutEdges: &pb.ReplicatedPutEdges{
				Entries: []*pb.ReplicatedPutEdge{nil},
			}}}
		}, 0},
		{"missing replicated Edge outcome", func(m *pb.Mutation) {
			m.Op = &pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutEdges{ReplicatedPutEdges: &pb.ReplicatedPutEdges{
				Entries: []*pb.ReplicatedPutEdge{{}},
			}}}
		}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invalid := proto.Clone(m).(*pb.Mutation)
			tc.mutate(invalid)
			outcomes := make([]graphcache.PutOutcome, tc.outcomes)
			for i := range outcomes {
				outcomes[i] = graphcache.PutOutcomeAppliedAndLive
			}
			if _, err := newGraphPutEffectEnvelope(invalid, outcomes); !errors.Is(err, errReceiptWALUnion) {
				t.Fatalf("invalid Put mutation created evidence: %v", err)
			}
		})
	}
	notPut := graphPutEffectTestMutation(&pb.MutationOp{Op: &pb.MutationOp_AddEdge{AddEdge: &pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "t", Head: "h"}}}})
	if _, err := newGraphPutEffectEnvelope(notPut, nil); !errors.Is(err, errReceiptWALUnion) {
		t.Fatalf("non-Put arm accepted: %v", err)
	}
	barrier := graphPutEffectTestMutation(&pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutEdges{ReplicatedPutEdges: &pb.ReplicatedPutEdges{
		Entries: []*pb.ReplicatedPutEdge{{Outcome: &pb.ReplicatedPutEdge_CausalBarrier{CausalBarrier: &pb.EdgeCausalBarrier{Tail: "t", Head: "h"}}}},
	}}})
	if _, err := newGraphPutEffectEnvelope(barrier, []graphcache.PutOutcome{graphcache.PutOutcomeAppliedAndLive}); !errors.Is(err, errReceiptWALUnion) {
		t.Fatalf("live effect for authoritative barrier accepted: %v", err)
	}
}

func BenchmarkGraphPutEffectWAL(b *testing.B) {
	edges := make([]*pb.Edge, 128)
	outcomes := make([]graphcache.PutOutcome, len(edges))
	for i := range edges {
		edges[i] = &pb.Edge{Tail: "tail", Head: "head", Weight: float32(i)}
		switch i % 3 {
		case 0:
			outcomes[i] = graphcache.PutOutcomeAppliedAndLive
		case 1:
			outcomes[i] = graphcache.PutOutcomeExpired
		default:
			outcomes[i] = graphcache.PutOutcomeSuperseded
		}
	}
	m := graphPutEffectTestMutation(&pb.MutationOp{Op: &pb.MutationOp_PutEdges{PutEdges: &pb.PutEdgesRequest{Edges: edges}}})
	e, err := newGraphPutEffectEnvelope(m, outcomes)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("capture", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := newGraphPutEffectEnvelope(m, outcomes); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("codec", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			raw, err := encodeReceiptWALUnion(e)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := decodeReceiptWALUnion(raw); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("capture_codec", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			current, err := newGraphPutEffectEnvelope(m, outcomes)
			if err != nil {
				b.Fatal(err)
			}
			raw, err := encodeReceiptWALUnion(current)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := decodeReceiptWALUnion(raw); err != nil {
				b.Fatal(err)
			}
		}
	})
}
