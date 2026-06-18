package service

import (
	"context"
	"testing"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// recordingSender captures the BackupSnapshotResponse frames a handler streams.
type recordingSender struct{ records []*pb.BackupSnapshotResponse }

func (s *recordingSender) Send(r *pb.BackupSnapshotResponse) error {
	s.records = append(s.records, r)
	return nil
}

func splitBackupSnapshotResponses(recs []*pb.BackupSnapshotResponse) (map[string]*pb.Vertex, map[string]*pb.Edge) {
	vs := map[string]*pb.Vertex{}
	es := map[string]*pb.Edge{}
	for _, r := range recs {
		switch x := r.GetRecord().(type) {
		case *pb.BackupSnapshotResponse_Vertex:
			vs[x.Vertex.GetKey()] = x.Vertex
		case *pb.BackupSnapshotResponse_Edge:
			es[x.Edge.GetTail()+"->"+x.Edge.GetHead()] = x.Edge
		}
	}
	return vs, es
}

func TestLanternService_BackupSnapshot(t *testing.T) {
	t.Run("WholeGraph", func(t *testing.T) {
		fb := newFakeBackend()
		fb.vertices["alice"] = &pb.Vertex{Key: "alice"}
		fb.vertices["bob"] = &pb.Vertex{Key: "bob"}
		fb.edges["alice"] = map[string]float32{"bob": 1.5}
		svc := NewLanternService(fb)

		sender := &recordingSender{}
		if err := svc.BackupSnapshot(context.Background(), &pb.BackupSnapshotRequest{}, sender); err != nil {
			t.Fatalf("BackupSnapshot: %v", err)
		}
		vs, es := splitBackupSnapshotResponses(sender.records)
		if len(vs) != 2 {
			t.Errorf("got %d vertices, want 2: %v", len(vs), vs)
		}
		if _, ok := vs["alice"]; !ok {
			t.Errorf("missing vertex alice")
		}
		e, ok := es["alice->bob"]
		if !ok {
			t.Fatalf("missing edge alice->bob: %v", es)
		}
		if e.GetWeight() != 1.5 {
			t.Errorf("edge weight = %v, want 1.5", e.GetWeight())
		}
		if e.Expiration != nil {
			t.Errorf("permanent edge should have nil expiration, got %v", e.Expiration)
		}
	})

	// vertex_prefix scopes the dump to the induced subgraph: only matching
	// vertices, and only edges whose BOTH endpoints match.
	t.Run("PrefixInducedSubgraph", func(t *testing.T) {
		fb := newFakeBackend()
		fb.vertices["user:a"] = &pb.Vertex{Key: "user:a"}
		fb.vertices["user:b"] = &pb.Vertex{Key: "user:b"}
		fb.vertices["other:c"] = &pb.Vertex{Key: "other:c"}
		fb.edges["user:a"] = map[string]float32{"user:b": 1, "other:c": 2}
		svc := NewLanternService(fb)

		sender := &recordingSender{}
		err := svc.BackupSnapshot(context.Background(), &pb.BackupSnapshotRequest{VertexPrefix: "user:"}, sender)
		if err != nil {
			t.Fatalf("BackupSnapshot: %v", err)
		}
		vs, es := splitBackupSnapshotResponses(sender.records)
		if len(vs) != 2 {
			t.Errorf("got %d vertices, want 2 (user:a,user:b): %v", len(vs), vs)
		}
		if _, ok := vs["other:c"]; ok {
			t.Errorf("non-matching vertex other:c leaked into prefix backup")
		}
		if len(es) != 1 {
			t.Errorf("got %d edges, want 1 (user:a->user:b): %v", len(es), es)
		}
		if _, ok := es["user:a->user:b"]; !ok {
			t.Errorf("missing intra-prefix edge user:a->user:b")
		}
		if _, ok := es["user:a->other:c"]; ok {
			t.Errorf("edge to non-matching endpoint other:c leaked into prefix backup")
		}
	})
}
