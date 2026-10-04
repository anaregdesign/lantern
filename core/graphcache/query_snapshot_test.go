package graphcache

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSnapshotGraphContext(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	c.PutVertex("visible:1", "allowed")
	c.PutVertex("hidden:1", "secret")
	c.PutVertex("visible:2", "other")
	c.AddEdgesWithExpiration([]EdgeItem[string]{{Tail: "visible:1", Head: "visible:2", Weight: 1}, {Tail: "visible:1", Head: "hidden:1", Weight: 100}})
	view, _ := NewQueryView([]KeyRange{{"visible:", "visible;"}}, []KeyRange{{"visible:", "visible;"}})
	snapshot, err := c.SnapshotGraphContext(WithQueryView(context.Background(), view))
	if err != nil || len(snapshot.Vertices) != 2 || len(snapshot.Edges) != 1 || snapshot.Edges[0].Head != "visible:2" {
		t.Fatalf("snapshot %+v error %v", snapshot, err)
	}
	ctx, cancel := context.WithCancel(WithQueryView(context.Background(), view))
	cancel()
	if snapshot, err = c.SnapshotGraphContext(ctx); !errors.Is(err, context.Canceled) || len(snapshot.Vertices) != 0 || len(snapshot.Edges) != 0 {
		t.Fatal("cancelled capture returned data", err)
	}
	if len(c.SnapshotGraph().Vertices) != 3 {
		t.Fatal("public query view changed private snapshot")
	}
}
