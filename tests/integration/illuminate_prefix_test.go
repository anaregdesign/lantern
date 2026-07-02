package integration_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestLantern_Illuminate_VertexPrefix drives the #602 vertex_prefix frontier
// filter end-to-end through the raw Connect-Go client. The Go SDK does not yet
// expose a WithVertexPrefix option (that is #603), so these full-stack
// assertions speak the wire pb.IlluminateRequest directly — the same pattern
// prefix_scan_test.go uses for RPCs the SDK has not wrapped. Fixtures carry an
// explicit Expiration to dodge the timestamppb-nil → 1970 trap (#250 lesson).
func TestLantern_Illuminate_VertexPrefix(t *testing.T) {
	exp := timestamppb.New(time.Now().Add(time.Hour))

	// putGraph stands up a fresh raw client, seeds the supplied string-valued
	// vertices and directed weighted edges, and returns the client + ctx. The
	// prefix index is irrelevant here: the keep predicate is a pure HasPrefix
	// check applied during the BFS walk, independent of any index.
	putGraph := func(t *testing.T, keys []string, edges []*pb.Edge) (graphv1connect.LanternServiceClient, context.Context) {
		t.Helper()
		c, _ := newRawConnectClient(t, false)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		t.Cleanup(cancel)
		verts := make([]*pb.Vertex, len(keys))
		for i, k := range keys {
			verts[i] = &pb.Vertex{Key: k, Value: &pb.Vertex_String_{String_: k}, Expiration: exp}
		}
		if _, err := c.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: verts})); err != nil {
			t.Fatalf("PutVertices: %v", err)
		}
		for _, e := range edges {
			e.Expiration = exp
		}
		if _, err := c.PutEdges(ctx, connect.NewRequest(&pb.PutEdgesRequest{Edges: edges})); err != nil {
			t.Fatalf("PutEdges: %v", err)
		}
		return c, ctx
	}

	keySet := func(vs []*pb.Vertex) map[string]bool {
		m := make(map[string]bool, len(vs))
		for _, v := range vs {
			m[v.Key] = true
		}
		return m
	}

	t.Run("filters to matching and retains non-matching seed", func(t *testing.T) {
		// Seed "root" does not carry the "p:" prefix, yet must survive as the
		// anchor; "p:a"/"p:b" match and stay; "q:x" is filtered out.
		c, ctx := putGraph(t,
			[]string{"root", "p:a", "p:b", "q:x"},
			[]*pb.Edge{
				{Tail: "root", Head: "p:a", Weight: 1},
				{Tail: "root", Head: "p:b", Weight: 1},
				{Tail: "root", Head: "q:x", Weight: 1},
			},
		)
		resp, err := c.Illuminate(ctx, connect.NewRequest(&pb.IlluminateRequest{
			Seed: "root", VertexPrefix: "p:",
			Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 3, FanOut: 10}},
		}))
		if err != nil {
			t.Fatalf("Illuminate: %v", err)
		}
		got := keySet(resp.Msg.GetGraph().GetVertices())
		for _, want := range []string{"root", "p:a", "p:b"} {
			if !got[want] {
				t.Errorf("missing vertex %q (got %v)", want, got)
			}
		}
		if got["q:x"] {
			t.Errorf("non-matching vertex q:x present (got %v)", got)
		}
	})

	t.Run("filters before per-hop top-k", func(t *testing.T) {
		// s's three strongest edges are all non-matching (100/99/98); the
		// matching edges are weaker (10/9). With k=2, a filter applied AFTER
		// top-k would select {q:1,q:2} and then drop them, leaving only the
		// seed. Applying the filter BEFORE top-k must instead surface the two
		// strongest MATCHING neighbours.
		c, ctx := putGraph(t,
			[]string{"s", "q:1", "q:2", "q:3", "p:1", "p:2"},
			[]*pb.Edge{
				{Tail: "s", Head: "q:1", Weight: 100},
				{Tail: "s", Head: "q:2", Weight: 99},
				{Tail: "s", Head: "q:3", Weight: 98},
				{Tail: "s", Head: "p:1", Weight: 10},
				{Tail: "s", Head: "p:2", Weight: 9},
			},
		)
		resp, err := c.Illuminate(ctx, connect.NewRequest(&pb.IlluminateRequest{
			Seed: "s", VertexPrefix: "p:",
			Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 1, FanOut: 2}},
		}))
		if err != nil {
			t.Fatalf("Illuminate: %v", err)
		}
		got := keySet(resp.Msg.GetGraph().GetVertices())
		for _, want := range []string{"s", "p:1", "p:2"} {
			if !got[want] {
				t.Errorf("missing vertex %q (got %v) — filter likely applied AFTER top-k", want, got)
			}
		}
		for _, no := range []string{"q:1", "q:2", "q:3"} {
			if got[no] {
				t.Errorf("non-matching vertex %q present (got %v)", no, got)
			}
		}
	})

	t.Run("induced subgraph under SPT and MST drops vertices reachable only via non-matching bridge", func(t *testing.T) {
		for _, red := range []pb.Reduction{
			pb.Reduction_REDUCTION_SHORTEST_PATH_TREE,
			pb.Reduction_REDUCTION_MINIMUM_SPANNING_TREE,
		} {
			t.Run(red.String(), func(t *testing.T) {
				// p:leaf matches the prefix but is reachable ONLY through the
				// non-matching bridge q:bridge. Filtering the bridge during the
				// walk makes p:leaf unreachable (induced-subgraph semantics), so
				// the tree contains only p:root and the directly-reachable
				// p:direct — never a true shortest path through the full graph.
				c, ctx := putGraph(t,
					[]string{"p:root", "q:bridge", "p:leaf", "p:direct"},
					[]*pb.Edge{
						{Tail: "p:root", Head: "q:bridge", Weight: 5},
						{Tail: "q:bridge", Head: "p:leaf", Weight: 5},
						{Tail: "p:root", Head: "p:direct", Weight: 1},
					},
				)
				resp, err := c.Illuminate(ctx, connect.NewRequest(&pb.IlluminateRequest{
					Seed: "p:root", VertexPrefix: "p:",
					Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 3, FanOut: 10, Reduction: red}},
				}))
				if err != nil {
					t.Fatalf("Illuminate(%s): %v", red, err)
				}
				got := keySet(resp.Msg.GetGraph().GetVertices())
				for _, want := range []string{"p:root", "p:direct"} {
					if !got[want] {
						t.Errorf("%s: missing vertex %q (got %v)", red, want, got)
					}
				}
				if got["q:bridge"] {
					t.Errorf("%s: non-matching bridge q:bridge present (got %v)", red, got)
				}
				if got["p:leaf"] {
					t.Errorf("%s: p:leaf present but only reachable via filtered bridge (got %v)", red, got)
				}
			})
		}
	})

	t.Run("empty vertex_prefix is no filter", func(t *testing.T) {
		c, ctx := putGraph(t,
			[]string{"root", "p:a", "p:b", "q:x"},
			[]*pb.Edge{
				{Tail: "root", Head: "p:a", Weight: 1},
				{Tail: "root", Head: "p:b", Weight: 1},
				{Tail: "root", Head: "q:x", Weight: 1},
			},
		)
		resp, err := c.Illuminate(ctx, connect.NewRequest(&pb.IlluminateRequest{
			Seed: "root", VertexPrefix: "",
			Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 3, FanOut: 10}},
		}))
		if err != nil {
			t.Fatalf("Illuminate: %v", err)
		}
		got := keySet(resp.Msg.GetGraph().GetVertices())
		for _, want := range []string{"root", "p:a", "p:b", "q:x"} {
			if !got[want] {
				t.Errorf("missing vertex %q with empty prefix (got %v)", want, got)
			}
		}
	})
}

// TestIlluminate_OneofArmRoundTrips drives one round-trip per params arm
// (#846) through the wire: params-unset (bare illuminate), the bfs arm, and
// the ppr arm must each reach the server, dispatch to their family, and
// return a graph rooted at the seed.
func TestIlluminate_OneofArmRoundTrips(t *testing.T) {
	exp := timestamppb.New(time.Now().Add(time.Hour))
	c, _ := newRawConnectClient(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	verts := []*pb.Vertex{
		{Key: "s", Value: &pb.Vertex_String_{String_: "s"}, Expiration: exp},
		{Key: "n1", Value: &pb.Vertex_String_{String_: "n1"}, Expiration: exp},
		{Key: "n2", Value: &pb.Vertex_String_{String_: "n2"}, Expiration: exp},
	}
	if _, err := c.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: verts})); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}
	if _, err := c.PutEdges(ctx, connect.NewRequest(&pb.PutEdgesRequest{Edges: []*pb.Edge{
		{Tail: "s", Head: "n1", Weight: 2, Expiration: exp},
		{Tail: "s", Head: "n2", Weight: 1, Expiration: exp},
	}})); err != nil {
		t.Fatalf("PutEdges: %v", err)
	}
	keySet := func(vs []*pb.Vertex) map[string]bool {
		m := make(map[string]bool, len(vs))
		for _, v := range vs {
			m[v.Key] = true
		}
		return m
	}
	cases := []struct {
		name     string
		req      *pb.IlluminateRequest
		seedOnly bool
	}{
		// step/fan_out zero means no hops, so the unset arm returns just the
		// seed — the same contract the flat request had (0 was never
		// defaulted server-side); the point here is that it dispatches
		// cleanly rather than erroring.
		{"params unset = bare illuminate", &pb.IlluminateRequest{Seed: "s"}, true},
		{"bfs arm", &pb.IlluminateRequest{Seed: "s",
			Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 2, FanOut: 5}}}, false},
		{"bfs arm with reduction", &pb.IlluminateRequest{Seed: "s",
			Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 2, FanOut: 5,
				Reduction: pb.Reduction_REDUCTION_SHORTEST_PATH_TREE, Objective: pb.Objective_OBJECTIVE_MINIMIZE}}}, false},
		{"ppr arm", &pb.IlluminateRequest{Seed: "s",
			Params: &pb.IlluminateRequest_Ppr{Ppr: &pb.PprParams{TopN: 5, RestartProb: 0.2, Epsilon: 1e-3}}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := c.Illuminate(ctx, connect.NewRequest(tc.req))
			if err != nil {
				t.Fatalf("Illuminate: %v", err)
			}
			got := keySet(resp.Msg.GetGraph().GetVertices())
			if !got["s"] {
				t.Fatalf("seed missing from result: %v", got)
			}
			if !tc.seedOnly && len(got) < 2 {
				t.Fatalf("expected at least one neighbour beside the seed, got %v", got)
			}
		})
	}
}
