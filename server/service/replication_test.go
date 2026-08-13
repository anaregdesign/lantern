package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

type replicationSnapshotRecorder struct {
	frames []*pb.SnapshotResponse
}

func (s *replicationSnapshotRecorder) Send(frame *pb.SnapshotResponse) error {
	s.frames = append(s.frames, frame)
	return nil
}

type replicationSnapshotCounter struct {
	frames int
}

func (s *replicationSnapshotCounter) Send(*pb.SnapshotResponse) error {
	s.frames++
	return nil
}

func TestLanternReplicationService_SnapshotCanonicalizesImplicitVertices(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	expiration := time.Now().Add(time.Hour).Round(0)
	ts := hlc.Timestamp{WallNs: 10, NodeID: hlc.NodeID{0x01}}
	if !cache.PutEdgeWithExpirationHLC("implicit-tail", "implicit-head", 2, expiration, ts) {
		t.Fatal("PutEdgeWithExpirationHLC rejected seed edge")
	}

	replication := NewLanternReplicationService(nil, cache, hlc.New(hlc.NodeID{0x02}, hlc.Options{}))
	recorder := &replicationSnapshotRecorder{}
	if err := replication.Snapshot(context.Background(), &pb.SnapshotRequest{}, recorder); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	vertices := make(map[string]*pb.Vertex)
	var edges int
	for _, frame := range recorder.frames {
		switch entry := frame.GetEntry().(type) {
		case *pb.SnapshotResponse_Vertex:
			if entry.Vertex == nil || entry.Vertex.GetVertex() == nil {
				t.Fatalf("Snapshot emitted nil vertex payload: %+v", entry.Vertex)
			}
			vertex := entry.Vertex.GetVertex()
			vertices[vertex.GetKey()] = vertex
		case *pb.SnapshotResponse_Edge:
			edges++
		}
	}
	for _, key := range []string{"implicit-tail", "implicit-head"} {
		vertex := vertices[key]
		if vertex == nil {
			t.Fatalf("Snapshot omitted implicit endpoint %q", key)
		}
		if !vertex.GetNil() {
			t.Fatalf("implicit endpoint %q value = %T, want Vertex.nil", key, vertex.GetValue())
		}
		if got := vertex.GetExpiration().AsTime(); !got.Equal(expiration) {
			t.Fatalf("implicit endpoint %q expiration = %v, want %v", key, got, expiration)
		}
	}
	if edges != 1 {
		t.Fatalf("Snapshot edge frames = %d, want 1", edges)
	}
}

// BenchmarkLanternReplicationService_SnapshotPutEdges is the focused local
// counterpart to broad_mutate: it materializes the same bounded 2,000-edge
// Put-only working set with implicit endpoints and drains a full replication
// Snapshot without retaining response frames.
func BenchmarkLanternReplicationService_SnapshotPutEdges(b *testing.B) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	items := make([]graphcache.EdgeItem[string], 2000)
	for i := range items {
		items[i] = graphcache.EdgeItem[string]{
			Tail: fmt.Sprintf("m-%d", i), Head: "m-h", Weight: 1,
		}
	}
	cache.PutEdgesWithExpirationHLC(items, hlc.Timestamp{WallNs: 10, NodeID: hlc.NodeID{0x01}})
	replication := NewLanternReplicationService(nil, cache, hlc.New(hlc.NodeID{0x02}, hlc.Options{}))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		counter := &replicationSnapshotCounter{}
		if err := replication.Snapshot(context.Background(), &pb.SnapshotRequest{}, counter); err != nil {
			b.Fatal(err)
		}
		if counter.frames == 0 {
			b.Fatal("Snapshot emitted no frames")
		}
	}
}
