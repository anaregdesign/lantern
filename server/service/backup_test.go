package service

import (
	"context"
	"math"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// recordingSender captures the BackupSnapshotResponse frames a handler streams.
type recordingSender struct {
	records []*pb.BackupSnapshotResponse
	onSend  func(*pb.BackupSnapshotResponse) error
}

func (s *recordingSender) Send(r *pb.BackupSnapshotResponse) error {
	if s.onSend != nil {
		if err := s.onSend(r); err != nil {
			return err
		}
	}
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

func TestLanternService_BackupSnapshotFailsClosedUntilVerifiedSnapshot(t *testing.T) {
	fb := newFakeBackend()
	fb.vertices["tail"] = &pb.Vertex{Key: "tail"}
	fb.vertices["head"] = &pb.Vertex{Key: "head"}
	fb.edges["tail"] = map[string]float32{"head": 1}
	svc := NewLanternService(fb)
	ctx := context.Background()
	finish, err := svc.BeginSnapshotInstall()
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"active", "failed"} {
		if stage == "failed" {
			finish(false)
		}
		sender := &recordingSender{}
		err := svc.BackupSnapshot(ctx, &pb.BackupSnapshotRequest{}, sender)
		if connect.CodeOf(err) != connect.CodeFailedPrecondition || len(sender.records) != 0 {
			t.Fatalf("backup during %s install = (%v, %d records), want FailedPrecondition without frames",
				stage, err, len(sender.records))
		}
	}
	retry, err := svc.BeginSnapshotInstall()
	if err != nil {
		t.Fatal(err)
	}
	retry(true)
	sender := &recordingSender{}
	if err := svc.BackupSnapshot(ctx, &pb.BackupSnapshotRequest{}, sender); err != nil {
		t.Fatalf("backup after verified retry: %v", err)
	}
	_, edges := splitBackupSnapshotResponses(sender.records)
	if len(edges) != 1 || edges["tail->head"].GetWeight() != 1 {
		t.Fatalf("backup after retry edges = %+v, want tail->head weight 1", edges)
	}
}

func TestLanternService_BackupSnapshotDetachesVerticesBeforeSend(t *testing.T) {
	fb := newFakeBackend()
	source := &pb.Vertex{Key: "kept", Value: &pb.Vertex_String_{String_: "before"}}
	fb.vertices["kept"] = source
	svc := NewLanternService(fb)
	sender := &recordingSender{onSend: func(frame *pb.BackupSnapshotResponse) error {
		finish := beginSnapshotInstallDuringSend(t, svc)
		defer finish(false)
		source.Value = &pb.Vertex_String_{String_: "during-replay"}
		if got := frame.GetVertex().GetString_(); got != "before" {
			t.Fatalf("backup frame retained a mutable cache vertex: %q", got)
		}
		return nil
	}}
	if err := svc.BackupSnapshot(context.Background(), &pb.BackupSnapshotRequest{}, sender); err != nil {
		t.Fatal(err)
	}
	if len(sender.records) != 1 || sender.records[0].GetVertex().GetString_() != "before" {
		t.Fatalf("off-lock backup emitted mutable vertex frames: %+v", sender.records)
	}
}

func TestLanternService_BackupSnapshotFoldedInfinityRestoresDirectly(t *testing.T) {
	for _, tc := range []struct {
		name   string
		weight float32
		sign   int
	}{
		{"positive", math.MaxFloat32, 1},
		{"negative", -math.MaxFloat32, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
			for _, key := range []string{"tail", "head"} {
				if err := cache.PutVertex(key, &pb.Vertex{Key: key}); err != nil {
					t.Fatal(err)
				}
				t.Run("historical NaN aggregate stays marked", func(t *testing.T) {
					cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
					svc := NewLanternService(cache)
					_, err := svc.RestoreEdges(context.Background(), &pb.PutEdgesRequest{Edges: []*pb.Edge{{
						Tail: "old-tail", Head: "old-head", Weight: float32(math.NaN()),
					}}})
					if err != nil {
						t.Fatal(err)
					}
					snapshot := cache.SnapshotEdges()
					if len(snapshot) != 1 || len(snapshot[0].Contributions) != 1 ||
						!snapshot[0].Contributions[0].DerivedAggregate ||
						!math.IsNaN(float64(snapshot[0].Contributions[0].Weight)) {
						t.Fatalf("historical NaN backup aggregate lost provenance: %+v", snapshot)
					}
					got, err := svc.GetEdge(context.Background(), &pb.GetEdgeRequest{Tail: "old-tail", Head: "old-head"})
					if err != nil || !math.IsNaN(float64(got.GetEdge().GetWeight())) {
						t.Fatalf("historical NaN aggregate read = (%+v, %v)", got, err)
					}
				})
			}
			for range 2 {
				cache.AddEdgeWithExpiration("tail", "head", tc.weight, time.Now().Add(time.Hour))
			}
			sender := &recordingSender{}
			if err := NewLanternService(cache).BackupSnapshot(context.Background(), &pb.BackupSnapshotRequest{}, sender); err != nil {
				t.Fatal(err)
			}
			_, edges := splitBackupSnapshotResponses(sender.records)
			folded := edges["tail->head"]
			if folded == nil || !math.IsInf(float64(folded.GetWeight()), tc.sign) {
				t.Fatalf("finite contributions folded into %v, want %s Infinity", folded, tc.name)
			}

			restored := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
			svc := NewLanternService(restored)
			request := &pb.PutEdgesRequest{Edges: []*pb.Edge{folded}}
			if _, err := svc.PutEdges(context.Background(), request); connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("public PutEdges of derived Infinity = %v, want InvalidArgument", err)
			}
			if _, live := restored.GetWeight("tail", "head"); live {
				t.Fatal("rejected public PutEdges changed the restored graph")
			}
			if _, err := svc.RestoreEdges(context.Background(), request); err != nil {
				t.Fatalf("direct startup RestoreEdges of derived Infinity: %v", err)
			}
			snapshot := restored.SnapshotEdges()
			if len(snapshot) != 1 || len(snapshot[0].Contributions) != 1 ||
				!snapshot[0].Contributions[0].DerivedAggregate ||
				!math.IsInf(float64(snapshot[0].Contributions[0].Weight), tc.sign) {
				t.Fatalf("restore lost folded aggregate provenance: %+v", snapshot)
			}
			got, err := svc.GetEdge(context.Background(), &pb.GetEdgeRequest{Tail: "tail", Head: "head"})
			if err != nil || !math.IsInf(float64(got.GetEdge().GetWeight()), tc.sign) {
				t.Fatalf("restored folded edge = (%+v, %v), want %s Infinity", got, err, tc.name)
			}
		})
	}
}
