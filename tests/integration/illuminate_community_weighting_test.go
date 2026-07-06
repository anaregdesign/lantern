package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestIlluminate_CommunityWeightingTransform pins #966 over the real Connect/h2c
// wire: the LocalCommunity family applies the weighting transform to its
// returned induced-subgraph edge weights (matching the BFS family), rather than
// echoing the verbatim stored weight regardless of the weighting axis.
//
// Fixture (mirrors the core sub-test): two cliques A={s,a,b,c} and B={x,y,z,w}
// joined by a WEAK bridge s<->x, so the seed community is exactly the full seed
// clique A. Every member of A also receives the SAME number of external
// in-edges, so TF-IDF/BM25 down-weight the intra-A edges by an identical factor
// — membership stays A under all three weightings, but the RETURNED a->b weight
// differs: RAW = verbatim 1; TF-IDF/BM25 = the re-scored value.
func TestIlluminate_CommunityWeightingTransform(t *testing.T) {
	exp := timestamppb.New(time.Now().Add(time.Hour))
	c, _ := newRawConnectClient(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	A := []string{"s", "a", "b", "c"}
	B := []string{"x", "y", "z", "w"}

	var verts []*pb.Vertex
	seen := map[string]bool{}
	addVert := func(k string) {
		if seen[k] {
			return
		}
		seen[k] = true
		verts = append(verts, &pb.Vertex{Key: k, Value: &pb.Vertex_String_{String_: k}, Expiration: exp})
	}
	for _, k := range A {
		addVert(k)
	}
	for _, k := range B {
		addVert(k)
	}

	var edges []*pb.Edge
	clique := func(members []string, w float32) {
		for _, u := range members {
			for _, v := range members {
				if u != v {
					edges = append(edges, &pb.Edge{Tail: u, Head: v, Weight: w, Expiration: exp})
				}
			}
		}
	}
	clique(A, 1.0)
	clique(B, 1.0)
	edges = append(edges,
		&pb.Edge{Tail: "s", Head: "x", Weight: 0.05, Expiration: exp}, // weak bridge each way
		&pb.Edge{Tail: "x", Head: "s", Weight: 0.05, Expiration: exp},
	)
	// Symmetric external in-edges: skew docFreq of every A member identically so
	// TF-IDF/BM25 keep membership = A but re-score the intra-A edges.
	for _, m := range A {
		for i := 0; i < 4; i++ {
			ext := fmt.Sprintf("%s_ext%d", m, i)
			addVert(ext)
			edges = append(edges, &pb.Edge{Tail: ext, Head: m, Weight: 1.0, Expiration: exp})
		}
	}

	if _, err := c.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: verts})); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}
	if _, err := c.PutEdges(ctx, connect.NewRequest(&pb.PutEdgesRequest{Edges: edges})); err != nil {
		t.Fatalf("PutEdges: %v", err)
	}

	community := func(t *testing.T, w pb.Weighting) map[string]map[string]float32 {
		t.Helper()
		resp, err := c.Illuminate(ctx, connect.NewRequest(&pb.IlluminateRequest{
			Seed:      "s",
			Weighting: w,
			Params: &pb.IlluminateRequest_Community{Community: &pb.LocalCommunityParams{
				MaxSize: 0, RestartProb: 0.15, Epsilon: 1e-6,
			}},
		}))
		if err != nil {
			t.Fatalf("Illuminate(%v): %v", w, err)
		}
		out := map[string]map[string]float32{}
		for _, e := range resp.Msg.GetGraph().GetEdges() {
			if out[e.GetTail()] == nil {
				out[e.GetTail()] = map[string]float32{}
			}
			out[e.GetTail()][e.GetHead()] = e.GetWeight()
		}
		return out
	}

	rawEdges := community(t, pb.Weighting_WEIGHTING_RAW)
	wr, ok := rawEdges["a"]["b"]
	if !ok || wr != 1.0 {
		t.Fatalf("RAW induced edge a->b = (%v,%v), want (1,true) — verbatim stored weight", wr, ok)
	}

	tfidfEdges := community(t, pb.Weighting_WEIGHTING_TFIDF)
	wt, ok := tfidfEdges["a"]["b"]
	if !ok {
		t.Fatalf("TFIDF induced edge a->b missing — membership shifted off A")
	}
	if !(wt > 0 && wt < wr) {
		t.Errorf("TFIDF edge a->b = %v, want 0 < w < raw(%v) — idf must down-weight the popular head", wt, wr)
	}

	bm25Edges := community(t, pb.Weighting_WEIGHTING_BM25)
	wb, ok := bm25Edges["a"]["b"]
	if !ok {
		t.Fatalf("BM25 induced edge a->b missing — membership shifted off A")
	}
	if wb == wr {
		t.Errorf("BM25 edge a->b = %v, want != raw(%v) — transform not applied over the wire", wb, wr)
	}
}
