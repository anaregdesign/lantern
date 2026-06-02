package graph

import (
	"context"
	"errors"
	"testing"
)

func TestGraph_ContextCancelled(t *testing.T) {
	g := NewGraph[string, int]()
	g.PutEdge("a", "b", 1.0)
	g.PutEdge("b", "c", 1.0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cases := []struct {
		name string
		run  func() error
	}{
		{"ConnectedGraphContext", func() error {
			_, err := g.ConnectedGraphContext(ctx, "a")
			return err
		}},
		{"MinimumSpanningTreeContext", func() error {
			_, err := g.MinimumSpanningTreeContext(ctx, "a", false)
			return err
		}},
		{"ShortestPathTreeContext", func() error {
			_, err := g.ShortestPathTreeContext(ctx, "a", func(w float32) float32 { return w })
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("want context.Canceled, got %v", err)
			}
		})
	}
}
