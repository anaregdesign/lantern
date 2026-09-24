package graphcache

import (
	"testing"
	"time"
)

func TestAddEdgeContribDedupDoesNotReviveExpiredEndpoint(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	expiration := time.Now().Add(time.Hour)
	id := ContribID{0: 1}
	if !c.AddEdgeWithExpirationContrib("tail", "head", 1, expiration, id) {
		t.Fatal("first contribution was not applied")
	}
	if err := c.PutVertexWithExpiration("tail", "expired", time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.GetVertex("tail"); ok {
		t.Fatal("born-expired Put left the endpoint visible")
	}
	if _, ok := c.GetWeight("tail", "head"); ok {
		t.Fatal("Edge remained visible without its endpoint")
	}

	if c.AddEdgeWithExpirationContrib("tail", "head", 1, expiration, id) {
		t.Fatal("duplicate contribution was applied")
	}
	if _, ok := c.GetVertex("tail"); ok {
		t.Fatal("duplicate contribution revived its endpoint")
	}
	if _, ok := c.GetWeight("tail", "head"); ok {
		t.Fatal("duplicate contribution revealed the old Edge")
	}

	if !c.AddEdgeWithExpirationContrib("tail", "head", 1, expiration, ContribID{0: 2}) {
		t.Fatal("new contribution was not applied")
	}
	if _, ok := c.GetVertex("tail"); !ok {
		t.Fatal("new contribution did not create its endpoint")
	}
	if weight, ok := c.GetWeight("tail", "head"); !ok || weight != 2 {
		t.Fatalf("new contribution Edge = %v/%t, want 2/true", weight, ok)
	}
}
