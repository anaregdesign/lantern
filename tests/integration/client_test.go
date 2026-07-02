package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	client "github.com/anaregdesign/lantern/sdks/go"
)

func TestLantern_PutGetDeleteVertex(t *testing.T) {
	l, cleanup := newInProcessClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := l.PutVertex(ctx, "k", "v", time.Minute); err != nil {
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

func TestLantern_AddPutDeleteEdge(t *testing.T) {
	l, cleanup := newInProcessClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := l.AddEdge(ctx, "a", "b", 1.5, time.Minute); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	e, err := l.GetEdge(ctx, "a", "b")
	if err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	if e.Weight != 1.5 {
		t.Errorf("weight = %v, want 1.5", e.Weight)
	}

	// PutEdge replaces.
	if err := l.PutEdge(ctx, "a", "b", 9, time.Minute); err != nil {
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
		if err := l.PutVertex(ctx, k, k, time.Minute); err != nil {
			t.Fatalf("PutVertex %s: %v", k, err)
		}
	}
	if err := l.PutEdge(ctx, "a", "b", 1, time.Minute); err != nil {
		t.Fatalf("PutEdge a->b: %v", err)
	}
	if err := l.PutEdge(ctx, "b", "c", 1, time.Minute); err != nil {
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

func TestLantern_GetVertices_BatchPartialMiss(t *testing.T) {
	l, cleanup := newInProcessClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := l.PutVertex(ctx, "a", int64(1), time.Minute); err != nil {
		t.Fatalf("PutVertex a: %v", err)
	}
	if err := l.PutVertex(ctx, "b", "two", time.Minute); err != nil {
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

	if err := l.AddEdge(ctx, "a", "b", 1.5, time.Minute); err != nil {
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
	err := l.PutVertex(ctx, "", "v", time.Minute)
	if err == nil {
		t.Fatal("expected error for empty key")
	}
	if !errors.Is(err, client.ErrInvalidArgument) {
		t.Errorf("want errors.Is(err, ErrInvalidArgument); got %v", err)
	}
}
