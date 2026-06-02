package graph

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestGraphCache_NeighborContextCancelled(t *testing.T) {
	c := NewGraphCache[string, int](time.Minute)
	c.PutVertex("a", 1)
	c.PutVertex("b", 2)
	c.AddEdge("a", "b", 1.0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := c.NeighborContext(ctx, "a", 5, 10, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}
