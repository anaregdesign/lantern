package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestGraphDeleteEffectWALRoundTripKeepsOriginalMutationAndDecision(t *testing.T) {
	past := time.Now().Add(-time.Hour).UTC().Truncate(time.Nanosecond)
	for _, tc := range []struct {
		name string
		op   *pb.MutationOp
		want []int
	}{
		{"vertices", &pb.MutationOp{Op: &pb.MutationOp_DeleteVertices{DeleteVertices: &pb.DeleteVerticesRequest{
			Keys: []string{"protected", "absent", "protected", "live", "live"},
		}}}, []int{1, 3, 4}},
		{"edges with nil slots", &pb.MutationOp{Op: &pb.MutationOp_DeleteEdges{DeleteEdges: &pb.DeleteEdgesRequest{
			Edges: []*pb.EdgeKey{nil, {Tail: "a", Head: "b"}, {Tail: "a", Head: "b"}},
		}}}, []int{0, 1, 2}},
		{"all rejected", &pb.MutationOp{Op: &pb.MutationOp_DeleteVertices{DeleteVertices: &pb.DeleteVerticesRequest{
			Keys: []string{"protected"},
		}}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutation := receiptWALUnionGraphFixture(tc.op)
			mutation.TombstoneExpiration = timestamppb.New(past)
			want := cloneQueuedMutation(mutation)
			envelope, err := newGraphDeleteEffectEnvelope(mutation, tc.want)
			if err != nil {
				t.Fatal(err)
			}
			// The WAL payload owns the original decision even if the caller
			// mutates the request and result slices after envelope creation.
			mutation.Seq++
			if len(tc.want) != 0 {
				tc.want[0]++
			}
			raw, err := encodeReceiptWALUnion(envelope)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := decodeReceiptWALUnion(raw)
			if err != nil {
				t.Fatal(err)
			}
			got, ok := decoded.(*graphDeleteEffectEnvelope)
			if !ok || !proto.Equal(got.Mutation, want) ||
				!got.Mutation.GetTombstoneExpiration().AsTime().Equal(past) ||
				!slices.Equal(got.AcceptedIndexes, envelope.AcceptedIndexes) {
				t.Fatalf("decoded Delete effect = %+v, want %+v", decoded, envelope)
			}
			if tc.name == "edges with nil slots" && got.Mutation.GetOp().GetDeleteEdges().Edges[0] != nil {
				t.Fatal("nil edge request slot was normalized")
			}
			projected, ok := graphMutationFromLog(got)
			if !ok || projected != got.Mutation || !proto.Equal(projected, want) {
				t.Fatalf("Subscribe projection = %v, %v", projected, ok)
			}
			frame := mutationlog.Entry{Seq: 91, HLC: receiptWALUnionGraphHLC(want), Op: got}
			if err := validateReceiptWALUnionEntry(frame); err != nil {
				t.Fatalf("valid Delete effect frame: %v", err)
			}
			frame.HLC.Logical++
			if err := validateReceiptWALUnionEntry(frame); !errors.Is(err, errReceiptWALUnion) {
				t.Fatalf("mismatched frame HLC = %v", err)
			}
		})
	}
}

func TestGraphDeleteEffectWALReplaysEveryExactArmWithAndWithoutTombstones(t *testing.T) {
	tests := []struct {
		name      string
		op        *pb.MutationOp
		seed      func(*graphcache.GraphCache[string, *pb.Vertex])
		stillLive func(*graphcache.GraphCache[string, *pb.Vertex]) bool
		itemCount int
	}{
		{
			name: "vertex",
			op:   &pb.MutationOp{Op: &pb.MutationOp_DeleteVertex{DeleteVertex: &pb.DeleteVertexRequest{Key: "v"}}},
			seed: func(graph *graphcache.GraphCache[string, *pb.Vertex]) {
				graph.PutVertexWithExpirationHLC("v", &pb.Vertex{Key: "v"}, time.Now().Add(time.Hour), hlc.Timestamp{WallNs: 1})
			},
			stillLive: func(graph *graphcache.GraphCache[string, *pb.Vertex]) bool {
				_, ok := graph.GetVertex("v")
				return ok
			},
			itemCount: 1,
		},
		{
			name: "vertices",
			op:   &pb.MutationOp{Op: &pb.MutationOp_DeleteVertices{DeleteVertices: &pb.DeleteVerticesRequest{Keys: []string{"v", "v"}}}},
			seed: func(graph *graphcache.GraphCache[string, *pb.Vertex]) {
				graph.PutVertexWithExpirationHLC("v", &pb.Vertex{Key: "v"}, time.Now().Add(time.Hour), hlc.Timestamp{WallNs: 1})
			},
			stillLive: func(graph *graphcache.GraphCache[string, *pb.Vertex]) bool {
				_, ok := graph.GetVertex("v")
				return ok
			},
			itemCount: 2,
		},
		{
			name: "edge",
			op:   &pb.MutationOp{Op: &pb.MutationOp_DeleteEdge{DeleteEdge: &pb.DeleteEdgeRequest{Tail: "t", Head: "h"}}},
			seed: func(graph *graphcache.GraphCache[string, *pb.Vertex]) {
				graph.PutEdgeWithExpirationHLC("t", "h", 1, time.Now().Add(time.Hour), hlc.Timestamp{WallNs: 1})
			},
			stillLive: func(graph *graphcache.GraphCache[string, *pb.Vertex]) bool {
				_, ok := graph.GetWeight("t", "h")
				return ok
			},
			itemCount: 1,
		},
		{
			name: "edges",
			op: &pb.MutationOp{Op: &pb.MutationOp_DeleteEdges{DeleteEdges: &pb.DeleteEdgesRequest{
				Edges: []*pb.EdgeKey{{Tail: "t", Head: "h"}, {Tail: "t", Head: "h"}},
			}}},
			seed: func(graph *graphcache.GraphCache[string, *pb.Vertex]) {
				graph.PutEdgeWithExpirationHLC("t", "h", 1, time.Now().Add(time.Hour), hlc.Timestamp{WallNs: 1})
			},
			stillLive: func(graph *graphcache.GraphCache[string, *pb.Vertex]) bool {
				_, ok := graph.GetWeight("t", "h")
				return ok
			},
			itemCount: 2,
		},
	}
	for _, tc := range tests {
		for _, tombstones := range []bool{false, true} {
			name := "physical"
			if tombstones {
				name = "tombstone"
			}
			t.Run(tc.name+"/"+name, func(t *testing.T) {
				mutation := receiptWALUnionGraphFixture(tc.op)
				if !tombstones {
					mutation.TombstoneExpiration = nil
				}
				envelope, err := newGraphDeleteEffectEnvelope(mutation, allAcceptedIndexes(tc.itemCount))
				if err != nil {
					t.Fatal(err)
				}
				raw, err := encodeReceiptWALUnion(envelope)
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := decodeReceiptWALUnion(raw)
				if err != nil {
					t.Fatal(err)
				}
				graph := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](time.Hour)
				tc.seed(graph)
				if err := replayGraphDeleteEffect(graph, decoded.(*graphDeleteEffectEnvelope)); err != nil {
					t.Fatal(err)
				}
				if tc.stillLive(graph) {
					t.Fatal("accepted Delete effect remained live after replay")
				}
			})
		}
	}
}

func TestGraphDeleteEffectWALRejectsMalformedDecisionAndOldKind(t *testing.T) {
	base := receiptWALUnionGraphFixture(&pb.MutationOp{Op: &pb.MutationOp_DeleteVertices{
		DeleteVertices: &pb.DeleteVerticesRequest{Keys: []string{"a", "b", "a"}},
	}})
	for _, tc := range []struct {
		name    string
		indexes []int
	}{
		{"duplicate", []int{0, 0}},
		{"descending", []int{2, 1}},
		{"out of range", []int{3}},
		{"negative", []int{-1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := newGraphDeleteEffectEnvelope(base, tc.indexes); !errors.Is(err, errReceiptWALUnion) {
				t.Fatalf("accepted indexes %v = %v", tc.indexes, err)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		mutate func(*pb.Mutation)
	}{
		{"missing deadline", func(m *pb.Mutation) { m.TombstoneExpiration = nil }},
		{"invalid deadline", func(m *pb.Mutation) { m.TombstoneExpiration = &timestamppb.Timestamp{Seconds: 253402300800} }},
		{"zero sequence", func(m *pb.Mutation) { m.Seq = 0 }},
		{"mismatched origin", func(m *pb.Mutation) { m.Origin[0] ^= 1 }},
		{"bad HLC", func(m *pb.Mutation) { m.Hlc.NodeId[0] ^= 1 }},
		{"non-Delete", func(m *pb.Mutation) {
			m.Op = &pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{}}}
			m.TombstoneExpiration = nil
		}},
		{"predicate Delete", func(m *pb.Mutation) {
			m.Op = &pb.MutationOp{Op: &pb.MutationOp_DeleteVerticesByPrefix{DeleteVerticesByPrefix: &pb.DeleteVerticesByPrefixRequest{Prefix: "a"}}}
			m.TombstoneExpiration = nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := cloneQueuedMutation(base)
			tc.mutate(m)
			if _, err := newGraphDeleteEffectEnvelope(m, nil); !errors.Is(err, errReceiptWALUnion) {
				t.Fatalf("invalid Mutation created envelope: %v", err)
			}
		})
	}

	valid, err := newGraphDeleteEffectEnvelope(base, []int{0, 2})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := encodeReceiptWALUnion(valid)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func([]byte)
	}{
		{"old union v2", func(b []byte) { b[4] = 2 }},
		{"wrong graph length", func(b []byte) { binary.BigEndian.PutUint32(b[16:20], uint32(len(b))) }},
		{"wrong accepted count", func(b []byte) {
			graphSize := int(binary.BigEndian.Uint32(b[16:20]))
			binary.BigEndian.PutUint32(b[20+graphSize:], 3)
		}},
		{"duplicate accepted index", func(b []byte) {
			graphSize := int(binary.BigEndian.Uint32(b[16:20]))
			first := 24 + graphSize
			copy(b[first+4:first+8], b[first:first+4])
		}},
		{"out of range accepted index", func(b []byte) {
			graphSize := int(binary.BigEndian.Uint32(b[16:20]))
			binary.BigEndian.PutUint32(b[24+graphSize:], 3)
		}},
		{"sidecar-less graph kind", func(b []byte) { b[8] = receiptWALUnionGraph }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := bytes.Clone(raw)
			tc.mutate(bad)
			if _, err := decodeReceiptWALUnion(bad); !errors.Is(err, errReceiptWALUnion) {
				t.Fatalf("malformed Delete effect decoded: %v", err)
			}
		})
	}
	// A v3 graph-only kind with the same complete protobuf still lacks the
	// accepted-index proof and must not bypass the new Delete kind.
	graphBody, err := encodeReceiptWALGraph(base)
	if err != nil {
		t.Fatal(err)
	}
	oldKind := make([]byte, receiptWALUnionHeaderSize+len(graphBody))
	copy(oldKind[:8], receiptWALUnionMagic)
	oldKind[8] = receiptWALUnionGraph
	binary.BigEndian.PutUint32(oldKind[12:16], uint32(len(graphBody)))
	copy(oldKind[16:], graphBody)
	if _, err := decodeReceiptWALUnion(oldKind); !errors.Is(err, errReceiptWALUnion) {
		t.Fatalf("sidecar-less graph Delete decoded: %v", err)
	}
	if _, err := encodeReceiptWALUnion(base); !errors.Is(err, errReceiptWALUnion) {
		t.Fatalf("sidecar-less graph Delete encoded: %v", err)
	}
}

func TestGraphDeleteEffectWALPrefixExactVictimsStayBounded(t *testing.T) {
	expiration := time.Now().Add(time.Hour)
	older := hlc.Timestamp{WallNs: 10}
	newer := hlc.Timestamp{WallNs: 20}
	vertex := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	vertex.EnablePrefixIndex(func(key string) string { return key })
	for _, key := range []string{"p/1", "p/2", "p/3", "p/4"} {
		ts := older
		if key == "p/1" {
			ts = newer
		}
		if !vertex.PutVertexWithExpirationHLC(key, &pb.Vertex{Key: key}, expiration, ts) {
			t.Fatal("seed vertex Put rejected")
		}
	}
	victims, err := vertex.DeleteByPrefixHLCCheckedKeys(context.Background(), "p/", 3, older, expiration)
	if err != nil || !slices.Equal(victims, []string{"p/2", "p/3"}) {
		t.Fatalf("capped vertex victims = %v, %v", victims, err)
	}
	vertexMutation := receiptWALUnionGraphFixture(&pb.MutationOp{Op: &pb.MutationOp_DeleteVertices{
		DeleteVertices: &pb.DeleteVerticesRequest{Keys: victims},
	}})
	vertexEnvelope, err := newGraphDeleteEffectEnvelope(vertexMutation, []int{0, 1})
	if err != nil {
		t.Fatalf("projected vertex Delete: %v", err)
	}

	edge := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	edge.EnablePrefixIndex(func(key string) string { return key })
	for _, head := range []string{"p/1", "p/2", "p/3", "p/4"} {
		ts := older
		if head == "p/1" {
			ts = newer
		}
		if !edge.PutEdgeWithExpirationHLC("tail", head, 1, expiration, ts) {
			t.Fatal("seed edge Put rejected")
		}
	}
	edgeVictims, err := edge.DeleteEdgesByPrefixHLCCheckedKeys(context.Background(), "tail", "p/", 3, older, expiration)
	if err != nil || !slices.Equal(edgeVictims, []graphcache.EdgeKey[string]{{Tail: "tail", Head: "p/2"}, {Tail: "tail", Head: "p/3"}}) {
		t.Fatalf("capped edge victims = %v, %v", edgeVictims, err)
	}
	exact := make([]*pb.EdgeKey, len(edgeVictims))
	for i, key := range edgeVictims {
		exact[i] = &pb.EdgeKey{Tail: key.Tail, Head: key.Head}
	}
	edgeMutation := receiptWALUnionGraphFixture(&pb.MutationOp{Op: &pb.MutationOp_DeleteEdges{
		DeleteEdges: &pb.DeleteEdgesRequest{Edges: exact},
	}})
	edgeEnvelope, err := newGraphDeleteEffectEnvelope(edgeMutation, []int{0, 1})
	if err != nil {
		t.Fatalf("projected edge Delete: %v", err)
	}
	for _, envelope := range []*graphDeleteEffectEnvelope{vertexEnvelope, edgeEnvelope} {
		raw, err := encodeReceiptWALUnion(envelope)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeReceiptWALUnion(raw)
		if err != nil {
			t.Fatal(err)
		}
		got := decoded.(*graphDeleteEffectEnvelope)
		if !slices.Equal(got.AcceptedIndexes, []uint32{0, 1}) || !proto.Equal(got.GraphMutation(), envelope.GraphMutation()) {
			t.Fatalf("capped prefix exact victim changed: %+v", got)
		}
	}
}

func TestGraphDeleteEffectWALFileRoundTripBeforeRecoveryGate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "delete-effect.wal")
	wal, err := mutationlog.CreateFileWAL(path, encodeReceiptWALUnion)
	if err != nil {
		t.Fatal(err)
	}
	m := receiptWALUnionGraphFixture(&pb.MutationOp{Op: &pb.MutationOp_DeleteVertices{
		DeleteVertices: &pb.DeleteVerticesRequest{Keys: []string{"absent", "rejected"}},
	}})
	m.TombstoneExpiration = timestamppb.New(time.Now().Add(-time.Hour))
	e, err := newGraphDeleteEffectEnvelope(m, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	if err := wal.Write(mutationlog.Entry{Seq: 1, HLC: receiptWALUnionGraphHLC(m), Op: e}); err != nil {
		t.Fatal(err)
	}
	if err := wal.Close(); err != nil {
		t.Fatal(err)
	}
	visits := 0
	if err := mutationlog.ReplayFileWAL(path, decodeReceiptWALUnion, func(entry mutationlog.Entry) error {
		visits++
		if err := validateReceiptWALUnionEntry(entry); err != nil {
			return err
		}
		got, ok := entry.Op.(*graphDeleteEffectEnvelope)
		if !ok || !slices.Equal(got.AcceptedIndexes, []uint32{0}) ||
			!got.Mutation.GetTombstoneExpiration().AsTime().Equal(m.GetTombstoneExpiration().AsTime()) {
			return errors.New("FileWAL changed the original Delete decision or deadline")
		}
		return nil
	}); err != nil || visits != 1 {
		t.Fatalf("Delete effect FileWAL replay = %v, visits %d", err, visits)
	}
}
