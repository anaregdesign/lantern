package integration_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestLantern_PutGetDeleteVertex(t *testing.T) {
	l, cleanup := newInProcessClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := l.PutVertex(ctx, "k", "v", time.Minute); err != nil {
		t.Fatalf("PutVertex: %v", err)
	}

	v, err := l.GetVertex(ctx, "k")
	if err != nil {
		t.Fatalf("GetVertex: %v", err)
	}
	got, err := client.StringValue(v)
	if err != nil {
		t.Fatalf("StringValue: %v", err)
	}
	if got != "v" {
		t.Errorf("StringValue = %q, want \"v\"", got)
	}

	if _, err := l.DeleteVertex(ctx, "k"); err != nil {
		t.Fatalf("DeleteVertex: %v", err)
	}
	if _, err := l.GetVertex(ctx, "k"); err == nil {
		t.Error("expected error after DeleteVertex, got nil")
	}
}

// TestRawConnect_PermanentExpiration exercises the absent-Timestamp wire
// contract over real Connect/h2c and exact index-aligned Put outcomes.
func TestRawConnect_PermanentExpiration(t *testing.T) {
	c, _ := newRawConnectClient(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	past := timestamppb.New(time.Now().Add(-time.Minute))
	put, err := c.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{
		Vertices: []*pb.Vertex{
			{Key: "permanent", Value: &pb.Vertex_String_{String_: "live"}},
			{Key: "edge-head", Value: &pb.Vertex_Nil{Nil: true}},
			{Key: "born-expired", Value: &pb.Vertex_String_{String_: "dead"}, Expiration: past},
		},
	}))
	if err != nil {
		t.Fatalf("PutVertices: %v", err)
	}
	wantOutcomes := []pb.PutOutcome{pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE, pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE, pb.PutOutcome_PUT_OUTCOME_EXPIRED}
	if got := put.Msg.GetOutcomes(); !slices.Equal(got, wantOutcomes) {
		t.Fatalf("PutVertices.Outcomes = %v, want %v", got, wantOutcomes)
	}

	gotVertices, err := c.GetVertices(ctx, connect.NewRequest(&pb.GetVerticesRequest{
		Keys: []string{"permanent", "born-expired"},
	}))
	if err != nil {
		t.Fatalf("GetVertices: %v", err)
	}
	if len(gotVertices.Msg.GetVertices()) != 1 || gotVertices.Msg.GetVertices()[0].GetKey() != "permanent" {
		t.Fatalf("GetVertices.Vertices = %v, want permanent only", gotVertices.Msg.GetVertices())
	}
	if gotVertices.Msg.GetVertices()[0].GetExpiration() != nil {
		t.Fatal("permanent vertex unexpectedly gained an expiration")
	}
	if got := gotVertices.Msg.GetMissing(); len(got) != 1 || got[0] != "born-expired" {
		t.Fatalf("GetVertices.Missing = %v, want [born-expired]", got)
	}

	putEdge, err := c.PutEdge(ctx, connect.NewRequest(&pb.PutEdgeRequest{Edge: &pb.Edge{
		Tail: "permanent", Head: "edge-head", Weight: 1,
	}}))
	if err != nil {
		t.Fatalf("PutEdge: %v", err)
	}
	if got := putEdge.Msg.GetOutcome(); got != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
		t.Fatalf("PutEdge.Outcome = %v, want APPLIED_AND_LIVE", got)
	}
	gotEdge, err := c.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{
		Tail: "permanent", Head: "edge-head",
	}))
	if err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	if gotEdge.Msg.GetEdge().GetExpiration() != nil {
		t.Fatal("permanent edge unexpectedly gained an expiration")
	}

	// Absolute timestamps come from the caller's clock. A device behind the
	// server can submit an already-expired deadline; the server, not that
	// device, decides EXPIRED. The mixed response stays index aligned and the
	// expired overwrite removes the prior live edge.
	future := timestamppb.New(time.Now().Add(time.Minute))
	mixedEdges, err := c.PutEdges(ctx, connect.NewRequest(&pb.PutEdgesRequest{
		Edges: []*pb.Edge{
			{Tail: "permanent", Head: "edge-head", Weight: 2, Expiration: past},
			{Tail: "clock-ahead", Head: "live-edge", Weight: 3, Expiration: future},
		},
	}))
	if err != nil {
		t.Fatalf("PutEdges(mixed clock): %v", err)
	}
	wantEdgeOutcomes := []pb.PutOutcome{pb.PutOutcome_PUT_OUTCOME_EXPIRED, pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE}
	if got := mixedEdges.Msg.GetOutcomes(); !slices.Equal(got, wantEdgeOutcomes) {
		t.Fatalf("PutEdges.Outcomes = %v, want %v", got, wantEdgeOutcomes)
	}
	gotEdges, err := c.GetEdges(ctx, connect.NewRequest(&pb.GetEdgesRequest{Edges: []*pb.EdgeKey{
		{Tail: "permanent", Head: "edge-head"},
		{Tail: "clock-ahead", Head: "live-edge"},
	}}))
	if err != nil {
		t.Fatalf("GetEdges: %v", err)
	}
	if len(gotEdges.Msg.GetEdges()) != 1 || gotEdges.Msg.GetEdges()[0].GetTail() != "clock-ahead" {
		t.Fatalf("GetEdges.Edges = %v, want clock-ahead edge only", gotEdges.Msg.GetEdges())
	}
	if got := gotEdges.Msg.GetMissing(); len(got) != 1 || got[0].GetTail() != "permanent" || got[0].GetHead() != "edge-head" {
		t.Fatalf("GetEdges.Missing = %v, want permanent->edge-head", got)
	}
}

// TestLantern_PutVertexIfAbsent exercises the SET NX surface (#896) end to end
// through the SDK with typed, server-authoritative outcomes.
func TestLantern_PutVertexIfAbsent(t *testing.T) {
	l, cleanup := newInProcessClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	outcome, err := l.PutVertexIfAbsent(ctx, "k", "one", time.Minute)
	if err != nil {
		t.Fatalf("PutVertexIfAbsent: %v", err)
	}
	if outcome != client.PutOutcomeAppliedAndLive {
		t.Fatalf("first outcome = %v, want applied", outcome)
	}

	outcome, err = l.PutVertexIfAbsent(ctx, "k", "two", time.Minute)
	if err != nil {
		t.Fatalf("PutVertexIfAbsent(repeat): %v", err)
	}
	if outcome != client.PutOutcomeConditionNotMet {
		t.Fatalf("second outcome = %v, want condition-not-met", outcome)
	}

	v, err := l.GetVertex(ctx, "k")
	if err != nil {
		t.Fatalf("GetVertex: %v", err)
	}
	if got, _ := client.StringValue(v); got != "one" {
		t.Errorf("value = %q, want \"one\" (skipped write must not overwrite)", got)
	}

	// Plural: "k" is live (skipped), "fresh" is new (written).
	results, err := l.PutVerticesIfAbsent(ctx, []client.VertexInput{
		{Key: "fresh", Value: "a", Expiration: time.Now().Add(time.Minute)},
		{Key: "k", Value: "b", Expiration: time.Now().Add(time.Minute)},
	})
	if err != nil {
		t.Fatalf("PutVerticesIfAbsent: %v", err)
	}
	want := []client.VertexPutResult{{Key: "fresh", Outcome: client.PutOutcomeAppliedAndLive}, {Key: "k", Outcome: client.PutOutcomeConditionNotMet}}
	if !slices.Equal(results, want) {
		t.Errorf("PutVerticesIfAbsent results = %v, want %v", results, want)
	}
}

func TestLantern_AddPutDeleteEdge(t *testing.T) {
	l, cleanup := newInProcessClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	effective, err := l.AddEdge(ctx, "a", "b", 1.5, time.Minute)
	if err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if effective != 1.5 {
		t.Errorf("AddEdge effective weight = %v, want 1.5", effective)
	}
	e, err := l.GetEdge(ctx, "a", "b")
	if err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	if e.Weight != 1.5 {
		t.Errorf("weight = %v, want 1.5", e.Weight)
	}

	// PutEdge replaces.
	if _, err := l.PutEdge(ctx, "a", "b", 9, time.Minute); err != nil {
		t.Fatalf("PutEdge: %v", err)
	}
	e, err = l.GetEdge(ctx, "a", "b")
	if err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	if e.Weight != 9 {
		t.Errorf("weight after PutEdge = %v, want 9", e.Weight)
	}

	if _, err := l.DeleteEdge(ctx, "a", "b"); err != nil {
		t.Fatalf("DeleteEdge: %v", err)
	}
	if _, err := l.GetEdge(ctx, "a", "b"); err == nil {
		t.Error("expected error after DeleteEdge, got nil")
	}
}

func TestLantern_Illuminate(t *testing.T) {
	l, cleanup := newInProcessClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, k := range []string{"a", "b", "c"} {
		if _, err := l.PutVertex(ctx, k, k, time.Minute); err != nil {
			t.Fatalf("PutVertex %s: %v", k, err)
		}
	}
	if _, err := l.PutEdge(ctx, "a", "b", 1, time.Minute); err != nil {
		t.Fatalf("PutEdge a->b: %v", err)
	}
	if _, err := l.PutEdge(ctx, "b", "c", 1, time.Minute); err != nil {
		t.Fatalf("PutEdge b->c: %v", err)
	}

	g, err := l.Illuminate(ctx, "a", client.WithBFS(client.BFSOpts{Step: 3, FanOut: 10}))
	if err != nil {
		t.Fatalf("Illuminate: %v", err)
	}
	for _, want := range []string{"a", "b", "c"} {
		if _, ok := g.Vertices[want]; !ok {
			t.Errorf("Illuminate result missing vertex %q (got %v)", want, g.Vertices)
		}
	}
	if _, ok := g.Edges["a"]["b"]; !ok {
		t.Errorf("Illuminate missing edge a->b (got %v)", g.Edges)
	}
}

// TestLantern_Illuminate_EqualScoresUseAscendingKeys exercises the public
// Connect/h2c path for #1000. Both BFS and PPR cap four exactly-tied heads to
// two, so the stable ascending-key membership is observable to SDK callers.
func TestLantern_Illuminate_EqualScoresUseAscendingKeys(t *testing.T) {
	l, cleanup := newInProcessClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, key := range []string{"seed", "alpha", "bravo", "charlie", "delta"} {
		if _, err := l.PutVertex(ctx, key, key, time.Minute); err != nil {
			t.Fatalf("PutVertex %q: %v", key, err)
		}
	}
	for _, key := range []string{"delta", "bravo", "alpha", "charlie"} {
		if _, err := l.PutEdge(ctx, "seed", key, 1, time.Minute); err != nil {
			t.Fatalf("PutEdge seed->%s: %v", key, err)
		}
	}

	want := map[string]bool{"alpha": true, "bravo": true}
	for _, test := range []struct {
		name string
		opts []client.IlluminateOption
	}{
		{
			name: "bfs",
			opts: []client.IlluminateOption{
				client.WithBFS(client.BFSOpts{Step: 1, FanOut: 2}),
			},
		},
		{
			name: "pagerank",
			opts: []client.IlluminateOption{
				client.WithPPR(client.PPROpts{TopN: 2, RestartProb: 0.15, Epsilon: 1e-7}),
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			for run := 0; run < 20; run++ {
				g, err := l.Illuminate(ctx, "seed", test.opts...)
				if err != nil {
					t.Fatalf("Illuminate run %d: %v", run, err)
				}
				got := map[string]bool{}
				for key := range g.Edges["seed"] {
					got[key] = true
				}
				if !mapsEqual(got, want) {
					t.Fatalf("run %d retained %v, want %v", run, got, want)
				}
			}
		})
	}
}

func mapsEqual(got, want map[string]bool) bool {
	if len(got) != len(want) {
		return false
	}
	for key := range want {
		if !got[key] {
			return false
		}
	}
	return true
}

func TestLantern_GetVertices_BatchPartialMiss(t *testing.T) {
	l, cleanup := newInProcessClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := l.PutVertex(ctx, "a", int64(1), time.Minute); err != nil {
		t.Fatalf("PutVertex a: %v", err)
	}
	if _, err := l.PutVertex(ctx, "b", "two", time.Minute); err != nil {
		t.Fatalf("PutVertex b: %v", err)
	}

	found, missing, err := l.GetVertices(ctx, []string{"a", "b", "missing"})
	if err != nil {
		t.Fatalf("GetVertices: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("found = %d, want 2", len(found))
	}
	if len(missing) != 1 || missing[0] != "missing" {
		t.Errorf("missing = %v, want [missing]", missing)
	}
}

func TestLantern_GetEdges_BatchPartialMiss(t *testing.T) {
	l, cleanup := newInProcessClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := l.AddEdge(ctx, "a", "b", 1.5, time.Minute); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}

	found, missing, err := l.GetEdges(ctx, []client.EdgeRef{
		{Tail: "a", Head: "b"},
		{Tail: "x", Head: "y"},
	})
	if err != nil {
		t.Fatalf("GetEdges: %v", err)
	}
	if len(found) != 1 || found[0].Tail != "a" || found[0].Head != "b" {
		t.Fatalf("found = %v, want one a->b", found)
	}
	if len(missing) != 1 || missing[0] != (client.EdgeRef{Tail: "x", Head: "y"}) {
		t.Errorf("missing = %v, want [{x y}]", missing)
	}
}

func TestLantern_ErrorSentinels(t *testing.T) {
	l, cleanup := newInProcessClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// NotFound: GetVertex on missing key.
	if _, err := l.GetVertex(ctx, "absent"); err == nil {
		t.Fatal("expected error for missing key")
	} else if !errors.Is(err, client.ErrNotFound) {
		t.Errorf("want errors.Is(err, ErrNotFound); got %v", err)
	}
	// InvalidArgument: empty key trips ValidationInterceptor (checkKey).
	_, err := l.PutVertex(ctx, "", "v", time.Minute)
	if err == nil {
		t.Fatal("expected error for empty key")
	}
	if !errors.Is(err, client.ErrInvalidArgument) {
		t.Errorf("want errors.Is(err, ErrInvalidArgument); got %v", err)
	}
}
