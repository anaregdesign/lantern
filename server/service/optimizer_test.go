package service

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	coregraph "github.com/anaregdesign/lantern/core/graph"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestResolveOptimizer_ShortestPathTreeRejectsNegativeCosts(t *testing.T) {
	g := coregraph.NewGraph[string, *pb.Vertex]()
	g.PutEdge("a", "b", -1)
	g.PutEdge("b", "a", -1)

	for _, objective := range []pb.Objective{
		pb.Objective_OBJECTIVE_MINIMIZE,
		pb.Objective_OBJECTIVE_MAXIMIZE,
	} {
		t.Run(objective.String(), func(t *testing.T) {
			opt := resolveOptimizer(pb.Reduction_REDUCTION_SHORTEST_PATH_TREE, objective)
			if opt == nil {
				t.Fatal("resolveOptimizer returned nil")
			}
			_, err := opt(context.Background(), g, "a")
			if !errors.Is(err, coregraph.ErrInvalidShortestPathCost) {
				t.Fatalf("errors.Is(err, ErrInvalidShortestPathCost) = false; err = %v", err)
			}
			if got := connect.CodeOf(optimizerToConnect(err)); got != connect.CodeFailedPrecondition {
				t.Fatalf("optimizer error code = %v, want FailedPrecondition", got)
			}
		})
	}
}
