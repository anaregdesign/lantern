package dataruntime

import (
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/graphstorage"
	"github.com/anaregdesign/tarp"
)

func TestDiskGraphReadRPCs(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	db, err := graphstore.Open(ctx, dir, hlc.NodeID{1})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := graphstorage.Rows()
	if err != nil {
		t.Fatal(err)
	}
	expired := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := db.Update(func(g uint64, _ *tarp.Snapshot, b *tarp.WriteBatch) error {
		for _, key := range []string{"a", "b", "b\x00", "c", "d", "z"} {
			v := graphstore.Vertex[*pb.Vertex]{Value: &pb.Vertex{Key: key}}
			if key == "d" {
				v.Expiration = expired
			}
			if key == "a" {
				v.Value = nil
			}
			if err := rows.PutVertex(b, g, key, v); err != nil {
				return err
			}
		}
		for _, head := range []string{"a", "b", "b\x00", "c", "d", "missing", "z"} {
			k := graphstore.EdgeKey[string]{Tail: "a", Head: head}
			if err := rows.PutEdge(b, g, k, graphstore.Edge{LastOrdinal: 2, Base: &graphstore.Source{Weight: 1}}); err != nil {
				return err
			}
			w := float32(3)
			if head == "b\x00" {
				w = -1
			}
			if err := rows.PutSource(b, g, k, graphstore.SourceID{Ordinal: 1}, graphstore.Source{Weight: w}); err != nil {
				return err
			}
			if err := rows.PutSource(b, g, k, graphstore.SourceID{Ordinal: 2}, graphstore.Source{Weight: 100, Expiration: expired}); err != nil {
				return err
			}
		}
		return rows.MarkIncomingEdgeIndex(b, g)
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = graphstore.OpenExisting(ctx, dir, hlc.NodeID{1})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := NewExecutor(nil) // any in-memory fallback would panic
	s.runtime = &ServingRuntime{tarp: &tarpRelay{db: db}}
	if got, err := s.Illuminate(ctx, &pb.IlluminateRequest{
		Seed: "a", Weighting: pb.Weighting_WEIGHTING_BM25,
		Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 1, FanOut: 1}},
	}); got != nil || connect.CodeOf(err) != connect.CodeDataLoss {
		t.Fatalf("durable traversal without certified topology = %v, %v", got, err)
	}
	if err := db.EnsureTopologyStats(ctx); err != nil {
		t.Fatal(err)
	}

	vertices, err := s.GetVertices(ctx, &pb.GetVerticesRequest{Keys: []string{"a", "d", "missing", "a", "b\x00"}})
	if err != nil || len(vertices.GetVertices()) != 3 || !vertices.GetVertices()[0].GetNil() || !slices.Equal(vertices.GetMissing(), []string{"d", "missing"}) {
		t.Fatalf("vertex batch=%v err=%v", vertices, err)
	}
	key := func(head string) *pb.EdgeKey { return &pb.EdgeKey{Tail: "a", Head: head} }
	edges, err := s.GetEdges(ctx, &pb.GetEdgesRequest{Edges: []*pb.EdgeKey{key("a"), key("missing"), key("b\x00"), key("d"), key("a")}})
	if err != nil || len(edges.GetEdges()) != 2 || edges.GetEdges()[1].GetWeight() != 4 || len(edges.GetMissing()) != 3 {
		t.Fatalf("edge batch=%v err=%v", edges, err)
	}
	ranges := []graphcache.KeyRange{{Lower: "a", Upper: "b"}, {Lower: "b\x00", Upper: "e"}}
	view, err := graphcache.NewQueryView(ranges, ranges)
	if err != nil {
		t.Fatal(err)
	}
	scoped := graphcache.WithQueryView(ctx, view)
	var all []string
	page := &pb.ScanVerticesRequest{Limit: 1, Order: pb.ScanOrder_SCAN_ORDER_DESC}
	for {
		resp, err := s.ScanVertices(scoped, page)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range resp.GetVertices() {
			all = append(all, v.GetKey())
		}
		page.Cursor = resp.GetNextCursor()
		if len(page.Cursor) == 0 {
			break
		}
	}
	if !slices.Equal(all, []string{"c", "b\x00", "a"}) {
		t.Fatalf("vertex pages=%q", all)
	}
	keys, err := s.ScanVertexKeys(scoped, &pb.ScanVertexKeysRequest{Prefix: "b", Limit: 1})
	if err != nil || !slices.Equal(keys.GetKeys(), []string{"b\x00"}) || len(keys.GetNextCursor()) != 0 {
		t.Fatalf("keys=%v err=%v", keys, err)
	}
	var heads []string
	edgePage := &pb.ScanEdgesRequest{TailPrefix: "a", Limit: 1}
	for {
		resp, err := s.ScanEdges(scoped, edgePage)
		if err != nil {
			t.Fatal(err)
		}
		for _, edge := range resp.GetEdges() {
			heads = append(heads, edge.GetHead())
		}
		edgePage.Cursor = resp.GetNextCursor()
		if len(edgePage.Cursor) == 0 {
			break
		}
	}
	if !slices.Equal(heads, []string{"a", "c"}) {
		t.Fatalf("edge pages=%q", heads)
	}
	for _, tc := range []struct {
		weighting pb.Weighting
		edge      float32
	}{
		{pb.Weighting_WEIGHTING_RAW, 4},
		{pb.Weighting_WEIGHTING_TFIDF, 4},
		{pb.Weighting_WEIGHTING_BM25, float32(search.BM25Score(4, 7, 1, 1, 7, search.DefaultBM25K1, search.DefaultBM25B))},
	} {
		walk, err := s.Illuminate(scoped, &pb.IlluminateRequest{
			Seed: "a", VertexPrefix: "c", Weighting: tc.weighting,
			Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 1, FanOut: 1}},
		})
		if err != nil || len(walk.GetGraph().GetEdges()) != 1 || len(walk.GetGraph().GetVertices()) != 2 ||
			walk.GetGraph().GetEdges()[0].GetHead() != "c" || walk.GetGraph().GetEdges()[0].GetWeight() != tc.edge {
			t.Fatalf("stored %v traversal = %v, %v", tc.weighting, walk, err)
		}
		rank, err := s.Illuminate(scoped, &pb.IlluminateRequest{
			Seed: "a", VertexPrefix: "c", Weighting: tc.weighting,
			Params: &pb.IlluminateRequest_Ppr{Ppr: &pb.PprParams{TopN: 1}},
		})
		if err != nil || len(rank.GetGraph().GetEdges()) != 1 || len(rank.GetGraph().GetVertices()) != 2 ||
			rank.GetGraph().GetEdges()[0].GetHead() != "c" || rank.GetGraph().GetEdges()[0].GetWeight() <= 0 {
			t.Fatalf("stored %v PageRank = %v, %v", tc.weighting, rank, err)
		}
		community, err := s.Illuminate(scoped, &pb.IlluminateRequest{
			Seed: "a", VertexPrefix: "c", Weighting: tc.weighting,
			Params: &pb.IlluminateRequest_Community{Community: &pb.LocalCommunityParams{MaxSize: 2}},
		})
		if err != nil || len(community.GetGraph().GetEdges()) != 2 || len(community.GetGraph().GetVertices()) != 2 {
			t.Fatalf("stored %v community = %v, %v", tc.weighting, community, err)
		}
	}
	degrees, err := s.TopVerticesByDegree(scoped, &pb.TopVerticesByDegreeRequest{Prefix: "a", K: 1})
	if err != nil || len(degrees.GetEntries()) != 1 || degrees.GetEntries()[0].GetDegree() != 2 ||
		degrees.GetEntries()[0].GetWeightedDegree() != 8 {
		t.Fatalf("stored OUT degree = %v, %v", degrees, err)
	}
	for _, tc := range []struct {
		dir    pb.TopVerticesByDegreeRequest_Direction
		degree uint64
	}{
		{pb.TopVerticesByDegreeRequest_DIRECTION_IN, 1},
		{pb.TopVerticesByDegreeRequest_DIRECTION_BOTH, 3},
	} {
		got, err := s.TopVerticesByDegree(scoped, &pb.TopVerticesByDegreeRequest{Prefix: "a", K: 1, Direction: tc.dir})
		if err != nil || len(got.GetEntries()) != 1 || got.GetEntries()[0].GetDegree() != tc.degree ||
			got.GetEntries()[0].GetWeightedDegree() != 4*float64(tc.degree) {
			t.Fatalf("stored %v degree = %v, %v", tc.dir, got, err)
		}
	}
	s.WithTraversalLimits(TraversalLimits{WorkBudget: graphcache.PPRWorkBudget{MaxTouchedEdges: 1}})
	if partial, err := s.TopVerticesByDegree(scoped, &pb.TopVerticesByDegreeRequest{Prefix: "a", K: 1}); partial != nil ||
		connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("stored degree budget = %v, %v", partial, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = s.GetVertex(ctx, &pb.GetVertexRequest{Key: "missing"})
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("closed DB became a miss: %v", err)
	}
}
