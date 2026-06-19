package graphcache

import (
	"context"
	"testing"
	"time"
)

func TestGraphCache_IndexLifecycle_ExplicitVertexAndEndpointCreation(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	c.EnableSearchIndex(textExtract)
	exp := time.Now().Add(time.Minute)

	c.PutVertexWithExpiration("user:1", "explicit searchable payload", exp)
	c.AddEdgeWithExpiration("user:1", "post:1", 2, exp)

	if got := c.CountByPrefix("user:"); got != 1 {
		t.Fatalf("CountByPrefix(user:) = %d, want 1", got)
	}
	if got := c.CountByPrefix("post:"); got != 1 {
		t.Fatalf("CountByPrefix(post:) = %d, want 1 (edge endpoint must be prefix-indexed)", got)
	}

	if got := keys(c.SearchVertices("searchable", 10, "")); !equalKeys(got, []string{"user:1"}) {
		t.Fatalf("SearchVertices(searchable) = %v, want [user:1]", got)
	}
	if got := c.SearchVertices("post", 10, ""); got != nil {
		t.Fatalf("SearchVertices(post) = %v, want nil (endpoint key must not be indexed as content)", keys(got))
	}

	var edges []string
	ok := c.ScanEdgesByPrefix(context.Background(), "user:", "post:", func(tailProjected, tail, headProjected, head string, weight float32, _ time.Time) bool {
		edges = append(edges, tailProjected+"|"+tail+"->"+headProjected+"|"+head)
		if weight != 2 {
			t.Fatalf("edge weight = %v, want 2", weight)
		}
		return true
	})
	if !ok {
		t.Fatal("ScanEdgesByPrefix returned ok=false")
	}
	if want := []string{"user:1|user:1->post:1|post:1"}; !equalKeys(edges, want) {
		t.Fatalf("ScanEdgesByPrefix = %v, want %v", edges, want)
	}

	if !c.DeleteVertex("user:1") {
		t.Fatal("DeleteVertex(user:1) reported false")
	}
	if got := c.CountByPrefix("user:"); got != 0 {
		t.Fatalf("CountByPrefix(user:) after delete = %d, want 0", got)
	}
	if got := c.SearchVertices("searchable", 10, ""); got != nil {
		t.Fatalf("SearchVertices(searchable) after delete = %v, want nil", keys(got))
	}
}

func TestGraphCache_IndexLifecycle_PutEdgeReplacementKeepsHeadIndexStable(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	exp := time.Now().Add(time.Minute)

	c.PutEdgeWithExpiration("tail:1", "head:1", 1, exp)
	c.PutEdgeWithExpiration("tail:1", "head:1", 7, exp)

	var hits int
	ok := c.ScanEdgesByPrefix(context.Background(), "tail:", "head:", func(_, _, _, _ string, weight float32, _ time.Time) bool {
		hits++
		if weight != 7 {
			t.Fatalf("weight after PutEdge replacement = %v, want 7", weight)
		}
		return true
	})
	if !ok {
		t.Fatal("ScanEdgesByPrefix returned ok=false")
	}
	if hits != 1 {
		t.Fatalf("ScanEdgesByPrefix hits = %d, want 1", hits)
	}

	if !c.DeleteEdge("tail:1", "head:1") {
		t.Fatal("DeleteEdge returned false")
	}
	if got := collectScan(c, "tail:", "head:"); len(got) != 0 {
		t.Fatalf("ScanEdgesByPrefix after delete = %v, want empty", got)
	}
}
