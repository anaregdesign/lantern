package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

type blockedDeleteSnapshotBackend struct {
	Backend
	cache   *graphcache.GraphCache[string, *pb.Vertex]
	entered chan struct{}
	release chan struct{}
}

type blockedAddSnapshotBackend struct {
	Backend
	cache   *graphcache.GraphCache[string, *pb.Vertex]
	entered chan struct{}
	release chan struct{}
}

type blockedLocalPutSnapshotBackend struct {
	Backend
	cache   *graphcache.GraphCache[string, *pb.Vertex]
	entered chan struct{}
	release chan struct{}
}

func (b *blockedLocalPutSnapshotBackend) PutVerticesWithExpirationHLCOutcomesChecked(items []graphcache.VertexItem[string, *pb.Vertex], ts hlc.Timestamp) ([]graphcache.PutOutcome, error) {
	close(b.entered)
	<-b.release
	return b.cache.PutVerticesWithExpirationHLCOutcomesChecked(items, ts)
}

func (b *blockedLocalPutSnapshotBackend) SnapshotReplication() graphcache.ReplicationSnapshot[string, *pb.Vertex] {
	return b.cache.SnapshotReplication()
}

func (b *blockedAddSnapshotBackend) AddEdgesWithExpirationContribHLC(items []graphcache.EdgeItem[string], ts hlc.Timestamp) ([]float32, int) {
	close(b.entered)
	<-b.release
	return b.cache.AddEdgesWithExpirationContribHLC(items, ts)
}

func (b *blockedAddSnapshotBackend) SnapshotReplication() graphcache.ReplicationSnapshot[string, *pb.Vertex] {
	return b.cache.SnapshotReplication()
}

func (b *blockedDeleteSnapshotBackend) DeleteVertexHLC(key string, ts hlc.Timestamp, expiration time.Time) bool {
	close(b.entered)
	<-b.release
	return b.cache.DeleteVertexHLC(key, ts, expiration)
}

func (b *blockedDeleteSnapshotBackend) SnapshotReplication() graphcache.ReplicationSnapshot[string, *pb.Vertex] {
	return b.cache.SnapshotReplication()
}

type replicationSnapshotRecorder struct {
	frames []*pb.SnapshotResponse
}

func (s *replicationSnapshotRecorder) Send(frame *pb.SnapshotResponse) error {
	s.frames = append(s.frames, frame)
	return nil
}

type replicationSnapshotCounter struct {
	frames int
	bytes  int
}

func (s *replicationSnapshotCounter) Send(frame *pb.SnapshotResponse) error {
	s.frames++
	s.bytes += proto.Size(frame)
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

func TestLanternReplicationService_SnapshotCutWaitsForRemoteDeleteCommit(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	backend := &blockedDeleteSnapshotBackend{Backend: cache, cache: cache, entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() {
		select {
		case <-backend.release:
		default:
			close(backend.release)
		}
	})
	log := mutationlog.New(mutationlog.Options{Capacity: 8, SubscriberBuffer: 8})
	t.Cleanup(func() { _ = log.Close() })
	clock := hlc.New(hlc.NodeID{0x02}, hlc.Options{})
	svc := NewLanternService(backend).WithReplication(log, clock, nil).WithTombstoneTTL(time.Hour)
	replication := NewLanternReplicationService(log, backend, clock).WithOriginStates(svc)
	origin := hlc.NodeID{0x01}
	mutation := &pb.Mutation{
		Origin: origin[:], Seq: 1,
		Hlc: &pb.HLCTimestamp{WallNs: time.Now().UnixNano(), NodeId: origin[:]},
		Op:  &pb.MutationOp{Op: &pb.MutationOp_DeleteVertex{DeleteVertex: &pb.DeleteVertexRequest{Key: "victim"}}},
	}
	applyDone := make(chan error, 1)
	go func() { applyDone <- svc.ApplyMutation(context.Background(), mutation) }()
	select {
	case <-backend.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("remote Delete did not reach the backend")
	}

	recorder := &replicationSnapshotRecorder{}
	snapshotDone := make(chan error, 1)
	go func() { snapshotDone <- replication.Snapshot(context.Background(), &pb.SnapshotRequest{}, recorder) }()
	var snapshotErr error
	snapshotCompleted := false
	select {
	case snapshotErr = <-snapshotDone:
		snapshotCompleted = true
		// A premature Snapshot is inspected below after unblocking ApplyMutation.
	case <-time.After(200 * time.Millisecond):
	}
	close(backend.release)
	if err := <-applyDone; err != nil {
		t.Fatalf("ApplyMutation: %v", err)
	}
	if !snapshotCompleted {
		snapshotErr = <-snapshotDone
	}
	if snapshotErr != nil {
		t.Fatalf("Snapshot: %v", snapshotErr)
	}
	var cutoff, tombstones uint64
	for _, frame := range recorder.frames {
		switch e := frame.GetEntry().(type) {
		case *pb.SnapshotResponse_Header:
			cutoff = e.Header.GetCutoffSeqPerOrigin()[fmt.Sprintf("%x", origin[:])]
		case *pb.SnapshotResponse_VertexTombstone:
			if e.VertexTombstone.GetKey() == "victim" {
				tombstones++
			}
		}
	}
	if cutoff != 1 || tombstones != 1 {
		t.Fatalf("Snapshot cut included Delete seq=%d but carried %d tombstones", cutoff, tombstones)
	}
}

func TestLanternReplicationService_SnapshotCutWaitsForLocalAddCommit(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	backend := &blockedAddSnapshotBackend{Backend: cache, cache: cache, entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() {
		select {
		case <-backend.release:
		default:
			close(backend.release)
		}
	})
	log := mutationlog.New(mutationlog.Options{Capacity: 8, SubscriberBuffer: 8})
	t.Cleanup(func() { _ = log.Close() })
	origin := hlc.NodeID{0x03}
	clock := hlc.New(origin, hlc.Options{})
	svc := NewLanternService(backend).WithReplication(log, clock, nil)
	replication := NewLanternReplicationService(log, backend, clock).WithOriginStates(svc)
	addDone := make(chan error, 1)
	go func() {
		_, err := svc.AddEdges(context.Background(), &pb.AddEdgesRequest{Edges: []*pb.Edge{{
			Tail: "tail", Head: "head", Weight: 1,
			Expiration: timestamppb.New(time.Now().Add(time.Hour)),
		}}})
		addDone <- err
	}()
	select {
	case <-backend.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("local Add did not reach the backend")
	}
	recorder := &replicationSnapshotRecorder{}
	snapshotDone := make(chan error, 1)
	go func() { snapshotDone <- replication.Snapshot(context.Background(), &pb.SnapshotRequest{}, recorder) }()
	var snapshotErr error
	snapshotCompleted := false
	select {
	case snapshotErr = <-snapshotDone:
		snapshotCompleted = true
	case <-time.After(200 * time.Millisecond):
	}
	close(backend.release)
	if err := <-addDone; err != nil {
		t.Fatalf("AddEdges: %v", err)
	}
	if !snapshotCompleted {
		snapshotErr = <-snapshotDone
	}
	if snapshotErr != nil {
		t.Fatalf("Snapshot: %v", snapshotErr)
	}
	var cutoff, edges uint64
	for _, frame := range recorder.frames {
		switch e := frame.GetEntry().(type) {
		case *pb.SnapshotResponse_Header:
			cutoff = e.Header.GetCutoffSeqPerOrigin()[fmt.Sprintf("%x", origin[:])]
		case *pb.SnapshotResponse_Edge:
			if e.Edge.GetTail() == "tail" && e.Edge.GetHead() == "head" {
				edges++
			}
		}
	}
	if cutoff != 1 || edges != 1 {
		t.Fatalf("Snapshot cut included Add seq=%d but carried %d edges", cutoff, edges)
	}
}

func TestLanternReplicationService_SnapshotCutWaitsForLocalPutPublication(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	backend := &blockedLocalPutSnapshotBackend{Backend: cache, cache: cache, entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() {
		select {
		case <-backend.release:
		default:
			close(backend.release)
		}
	})
	log := mutationlog.New(mutationlog.Options{Capacity: 8, SubscriberBuffer: 8})
	t.Cleanup(func() { _ = log.Close() })
	origin := hlc.NodeID{0x04}
	clock := hlc.New(origin, hlc.Options{})
	svc := NewLanternService(backend).WithReplication(log, clock, nil)
	replication := NewLanternReplicationService(log, backend, clock).WithOriginStates(svc)
	putDone := make(chan error, 1)
	go func() {
		_, err := svc.PutVertices(context.Background(), &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{
			Key: "local-put", Value: &pb.Vertex_String_{String_: "committed"}, Expiration: timestamppb.New(time.Now().Add(time.Minute)),
		}}})
		putDone <- err
	}()
	select {
	case <-backend.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("local Put did not reach the backend")
	}
	recorder := &replicationSnapshotRecorder{}
	snapshotDone := make(chan error, 1)
	go func() { snapshotDone <- replication.Snapshot(context.Background(), &pb.SnapshotRequest{}, recorder) }()
	select {
	case err := <-snapshotDone:
		t.Fatalf("Snapshot crossed an in-progress local Put: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(backend.release)
	if err := <-putDone; err != nil {
		t.Fatalf("PutVertices: %v", err)
	}
	if err := <-snapshotDone; err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	var cutoff uint64
	var live *pb.Vertex
	for _, frame := range recorder.frames {
		switch entry := frame.GetEntry().(type) {
		case *pb.SnapshotResponse_Header:
			cutoff = entry.Header.GetCutoffSeqPerOrigin()[fmt.Sprintf("%x", origin[:])]
		case *pb.SnapshotResponse_Vertex:
			if entry.Vertex.GetVertex().GetKey() == "local-put" {
				live = entry.Vertex.GetVertex()
			}
		}
	}
	if cutoff != 1 || live.GetString_() != "committed" {
		t.Fatalf("Snapshot cut = %d and local vertex = %v, want seq 1 and committed", cutoff, live)
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
		b.ReportMetric(float64(counter.bytes), "wire-bytes/op")
	}
}

// BenchmarkLanternReplicationService_SnapshotWithTombstones keeps the same
// 2,000-live-edge working set as SnapshotPutEdges and adds 2,000 retained
// Delete vertices plus 2,000 Delete edges. The paired benchmarks expose the
// extra materialization/encoding cost and exact protobuf payload size.
func BenchmarkLanternReplicationService_SnapshotWithTombstones(b *testing.B) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	items := make([]graphcache.EdgeItem[string], 2000)
	deletedVertices := make([]string, 2000)
	deletedEdges := make([]graphcache.EdgeKey[string], 2000)
	for i := range items {
		items[i] = graphcache.EdgeItem[string]{Tail: fmt.Sprintf("m-%d", i), Head: "m-h", Weight: 1}
		deletedVertices[i] = fmt.Sprintf("deleted-v-%d", i)
		deletedEdges[i] = graphcache.EdgeKey[string]{Tail: fmt.Sprintf("deleted-tail-%d", i), Head: "deleted-head"}
	}
	ts := hlc.Timestamp{WallNs: 10, NodeID: hlc.NodeID{0x01}}
	cache.PutEdgesWithExpirationHLC(items, ts)
	deadline := time.Now().Add(time.Hour)
	cache.DeleteVerticesHLC(deletedVertices, ts, deadline)
	cache.DeleteEdgesHLC(deletedEdges, ts, deadline)
	replication := NewLanternReplicationService(nil, cache, hlc.New(hlc.NodeID{0x02}, hlc.Options{}))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		counter := &replicationSnapshotCounter{}
		if err := replication.Snapshot(context.Background(), &pb.SnapshotRequest{}, counter); err != nil {
			b.Fatal(err)
		}
		if counter.frames != 10003 { // header/footer + 2001 vertices + 2000 edges + 2000 barriers + 4000 tombstones
			b.Fatalf("Snapshot emitted %d frames", counter.frames)
		}
		b.ReportMetric(float64(counter.bytes), "wire-bytes/op")
	}
}
