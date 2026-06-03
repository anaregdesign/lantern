package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/cache/graph"
	coregraph "github.com/anaregdesign/lantern/core/graph"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeBackend is a hand-rolled Backend stub exercised by tests that want to
// drive specific success/error paths without standing up a real GraphCache.
// Unset fields default to zero/empty so each test fills only what it needs.
type fakeBackend struct {
	vertices    map[string]*pb.Vertex
	edges       map[string]map[string]float32
	neighborErr error

	putVerticesCalls int
	deleteVertices   int
	addEdgesCalls    int
	putEdgesCalls    int
	deleteEdges      int
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		vertices: map[string]*pb.Vertex{},
		edges:    map[string]map[string]float32{},
	}
}

func (f *fakeBackend) GetVertex(key string) (*pb.Vertex, bool) {
	v, ok := f.vertices[key]
	return v, ok
}

func (f *fakeBackend) PutVerticesWithExpiration(items []graph.VertexItem[string, *pb.Vertex]) {
	f.putVerticesCalls++
	for _, it := range items {
		f.vertices[it.Key] = it.Value
	}
}

func (f *fakeBackend) DeleteVertices(keys []string) int {
	n := 0
	for _, k := range keys {
		if _, ok := f.vertices[k]; ok {
			delete(f.vertices, k)
			n++
		}
	}
	f.deleteVertices += n
	return n
}

func (f *fakeBackend) GetEdgeDetail(tail, head string) (float32, time.Time, bool) {
	if row, ok := f.edges[tail]; ok {
		if w, ok := row[head]; ok {
			return w, time.Time{}, true
		}
	}
	return 0, time.Time{}, false
}

func (f *fakeBackend) AddEdgesWithExpiration(items []graph.EdgeItem[string]) {
	f.addEdgesCalls++
	for _, it := range items {
		if f.edges[it.Tail] == nil {
			f.edges[it.Tail] = map[string]float32{}
		}
		f.edges[it.Tail][it.Head] += it.Weight
	}
}

func (f *fakeBackend) PutEdgesWithExpiration(items []graph.EdgeItem[string]) {
	f.putEdgesCalls++
	for _, it := range items {
		if f.edges[it.Tail] == nil {
			f.edges[it.Tail] = map[string]float32{}
		}
		f.edges[it.Tail][it.Head] = it.Weight
	}
}

func (f *fakeBackend) DeleteEdges(keys []graph.EdgeKey[string]) int {
	n := 0
	for _, k := range keys {
		if row, ok := f.edges[k.Tail]; ok {
			if _, ok := row[k.Head]; ok {
				delete(row, k.Head)
				n++
			}
		}
	}
	f.deleteEdges += n
	return n
}

func (f *fakeBackend) NeighborWithExpirationsContext(
	ctx context.Context,
	seed string,
	step, k int,
	tfidf bool,
) (*coregraph.Graph[string, *pb.Vertex], map[string]map[string]time.Time, error) {
	if f.neighborErr != nil {
		return nil, nil, f.neighborErr
	}
	g := coregraph.NewGraph[string, *pb.Vertex]()
	if v, ok := f.vertices[seed]; ok {
		g.Vertices[seed] = v
	}
	return g, map[string]map[string]time.Time{}, nil
}

func (f *fakeBackend) Watch(ctx context.Context, interval time.Duration) {
	<-ctx.Done()
}

// Prefix-scan stubs. Tests that exercise the prefix RPCs use the real
// GraphCache via the bufconn harness; the fake just walks its in-memory
// map in lexicographic order so unit tests can still hit the wrappers
// without standing up the cache.
func (f *fakeBackend) ScanByPrefix(_ context.Context, prefix string, fn func(string, string, *pb.Vertex) bool) bool {
	keys := make([]string, 0, len(f.vertices))
	for k := range f.vertices {
		if prefix == "" || (len(k) >= len(prefix) && k[:len(prefix)] == prefix) {
			keys = append(keys, k)
		}
	}
	// Sort to match the radix index's lexicographic walk order.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	for _, k := range keys {
		if !fn(k, k, f.vertices[k]) {
			return false
		}
	}
	return true
}

func (f *fakeBackend) CountByPrefix(prefix string) int {
	n := 0
	for k := range f.vertices {
		if prefix == "" || (len(k) >= len(prefix) && k[:len(prefix)] == prefix) {
			n++
		}
	}
	return n
}

func (f *fakeBackend) DeleteByPrefix(_ context.Context, prefix string, limit int) int {
	victims := []string{}
	for k := range f.vertices {
		if prefix == "" || (len(k) >= len(prefix) && k[:len(prefix)] == prefix) {
			victims = append(victims, k)
			if limit > 0 && len(victims) >= limit {
				break
			}
		}
	}
	for _, k := range victims {
		delete(f.vertices, k)
	}
	f.deleteVertices += len(victims)
	return len(victims)
}

func (f *fakeBackend) ScanEdgesByPrefix(_ context.Context, tailPrefix, headPrefix string,
	fn func(string, string, string, string, float32, time.Time) bool,
) bool {
	tails := make([]string, 0, len(f.edges))
	for t := range f.edges {
		if tailPrefix == "" || (len(t) >= len(tailPrefix) && t[:len(tailPrefix)] == tailPrefix) {
			tails = append(tails, t)
		}
	}
	for i := 1; i < len(tails); i++ {
		for j := i; j > 0 && tails[j-1] > tails[j]; j-- {
			tails[j-1], tails[j] = tails[j], tails[j-1]
		}
	}
	for _, t := range tails {
		row := f.edges[t]
		heads := make([]string, 0, len(row))
		for h := range row {
			if headPrefix == "" || (len(h) >= len(headPrefix) && h[:len(headPrefix)] == headPrefix) {
				heads = append(heads, h)
			}
		}
		for i := 1; i < len(heads); i++ {
			for j := i; j > 0 && heads[j-1] > heads[j]; j-- {
				heads[j-1], heads[j] = heads[j], heads[j-1]
			}
		}
		for _, h := range heads {
			if !fn(t, t, h, h, row[h], time.Time{}) {
				return false
			}
		}
	}
	return true
}

// Compile-time check that fakeBackend really satisfies Backend.
var _ Backend = (*fakeBackend)(nil)

// Replicated-write entry points (#182). The fake mirrors the production
// semantics with a minimal in-memory model so service-level tests that
// touch ApplyMutation can run against the fake without spinning up a
// real GraphCache. It does not enforce HLC/ContribID dedup — tests that
// care about convergence use the real backend.
func (f *fakeBackend) AddEdgeWithExpirationContrib(tail, head string, w float32, _ time.Time, _ graph.ContribID) bool {
	if f.edges[tail] == nil {
		f.edges[tail] = map[string]float32{}
	}
	f.edges[tail][head] += w
	return true
}

func (f *fakeBackend) PutVertexWithExpirationHLC(key string, value *pb.Vertex, _ time.Time, _ hlc.Timestamp) bool {
	f.vertices[key] = value
	return true
}

func (f *fakeBackend) PutEdgeWithExpirationHLC(tail, head string, w float32, _ time.Time, _ hlc.Timestamp) bool {
	if f.edges[tail] == nil {
		f.edges[tail] = map[string]float32{}
	}
	f.edges[tail][head] = w
	return true
}

func TestLanternService_FakeBackend_PutGetDelete(t *testing.T) {
	fb := newFakeBackend()
	svc := NewLanternService(fb)
	ctx := context.Background()

	v := &pb.Vertex{Key: "a", Value: &pb.Vertex_String_{String_: "alpha"}}
	if _, err := svc.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{v}}); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}
	if fb.putVerticesCalls != 1 {
		t.Errorf("putVerticesCalls = %d, want 1", fb.putVerticesCalls)
	}

	resp, err := svc.GetVertex(ctx, &pb.GetVertexRequest{Key: "a"})
	if err != nil {
		t.Fatalf("GetVertex: %v", err)
	}
	if got := resp.Vertex.GetString_(); got != "alpha" {
		t.Errorf("value = %q, want \"alpha\"", got)
	}

	if _, err := svc.DeleteVertices(ctx, &pb.DeleteVerticesRequest{Keys: []string{"a"}}); err != nil {
		t.Fatalf("DeleteVertices: %v", err)
	}
	if fb.deleteVertices != 1 {
		t.Errorf("deleteVertices = %d, want 1", fb.deleteVertices)
	}
}

func TestLanternService_FakeBackend_Illuminate_PropagatesError(t *testing.T) {
	fb := newFakeBackend()
	fb.neighborErr = errors.New("simulated cache failure")
	svc := NewLanternService(fb)

	_, err := svc.Illuminate(context.Background(), &pb.IlluminateRequest{Seed: "a", Step: 1, K: 1})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// status.FromContextError turns a generic non-context error into Unknown.
	if st, _ := status.FromError(err); st.Code() != codes.Unknown {
		t.Errorf("status code = %v, want Unknown", st.Code())
	}
}

func TestLanternService_FakeBackend_PutAndDeleteEdge(t *testing.T) {
	fb := newFakeBackend()
	svc := NewLanternService(fb)
	ctx := context.Background()

	if _, err := svc.PutEdge(ctx, &pb.PutEdgeRequest{Edge: &pb.Edge{Tail: "t", Head: "h", Weight: 2.5}}); err != nil {
		t.Fatalf("PutEdge: %v", err)
	}
	if fb.putEdgesCalls != 1 {
		t.Errorf("putEdgesCalls = %d, want 1", fb.putEdgesCalls)
	}

	resp, err := svc.GetEdge(ctx, &pb.GetEdgeRequest{Tail: "t", Head: "h"})
	if err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	if resp.Edge.Weight != 2.5 {
		t.Errorf("weight = %v, want 2.5", resp.Edge.Weight)
	}

	del, err := svc.DeleteEdge(ctx, &pb.DeleteEdgeRequest{Tail: "t", Head: "h"})
	if err != nil {
		t.Fatalf("DeleteEdge: %v", err)
	}
	if !del.Existed {
		t.Error("Existed = false, want true")
	}
}
