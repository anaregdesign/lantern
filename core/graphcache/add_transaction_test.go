package graphcache

import (
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

func TestEdgeAddTransaction(t *testing.T) {
	expiration := time.Now().Add(time.Hour)
	ts := hlc.Timestamp{WallNs: time.Now().UnixNano(), Logical: 1, NodeID: hlc.NodeID{1}}
	contrib := ContribID{1}

	t.Run("commit publishes aligned results", func(t *testing.T) {
		c := NewGraphCacheWithStaging[string, string](time.Hour)
		tx, err := c.BeginEdgeAdd([]EdgeItem[string]{
			{Tail: "a", Head: "b", Weight: 2, Expiration: expiration, ContribID: contrib},
			{Tail: "a", Head: "b", Weight: 3, Expiration: expiration, ContribID: ContribID{2}},
		}, ts)
		if err != nil {
			t.Fatal(err)
		}
		result := tx.Result()
		if got, want := result.Effective, []float32{2, 5}; !equalFloat32s(got, want) {
			t.Fatalf("Effective = %v, want %v", got, want)
		}
		if len(result.Accepted) != 2 || result.Accepted[0].Index != 0 || result.Accepted[1].Index != 1 {
			t.Fatalf("Accepted = %+v", result.Accepted)
		}
		tx.Commit()
		if got, ok := c.GetWeight("a", "b"); !ok || got != 5 {
			t.Fatalf("GetWeight = %v, %v, want 5, true", got, ok)
		}
	})

	t.Run("abort restores edge and endpoint state", func(t *testing.T) {
		c := NewGraphCacheWithStaging[string, string](time.Hour)
		c.PutVertexWithExpiration("keep", "value", expiration)
		c.AddEdgeWithExpirationContrib("keep", "head", 7, expiration, ContribID{9})
		tx, err := c.BeginEdgeAdd([]EdgeItem[string]{
			{Tail: "keep", Head: "head", Weight: 2, Expiration: expiration, ContribID: contrib},
			{Tail: "new-tail", Head: "new-head", Weight: 3, Expiration: expiration, ContribID: ContribID{2}},
		}, ts)
		if err != nil {
			t.Fatal(err)
		}
		tx.Abort()
		if got, ok := c.GetWeight("keep", "head"); !ok || got != 7 {
			t.Fatalf("restored existing weight = %v, %v, want 7, true", got, ok)
		}
		if _, ok := c.GetWeight("new-tail", "new-head"); ok {
			t.Fatal("aborted edge remained visible")
		}
		if _, ok := c.GetVertex("new-tail"); ok {
			t.Fatal("aborted endpoint remained visible")
		}
	})

	t.Run("duplicate contribution is not accepted", func(t *testing.T) {
		c := NewGraphCacheWithStaging[string, string](time.Hour)
		c.AddEdgeWithExpirationContribHLC("a", "b", 4, expiration, contrib, ts)
		tx, err := c.BeginEdgeAdd([]EdgeItem[string]{
			{Tail: "a", Head: "b", Weight: 4, Expiration: expiration, ContribID: contrib},
		}, ts)
		if err != nil {
			t.Fatal(err)
		}
		result := tx.Result()
		if len(result.Accepted) != 0 || len(result.Effective) != 1 || result.Effective[0] != 4 {
			t.Fatalf("Result = %+v, want duplicate effective weight 4", result)
		}
		tx.Commit()
		if got, _ := c.GetWeight("a", "b"); got != 4 {
			t.Fatalf("GetWeight = %v, want 4", got)
		}
	})
}

func equalFloat32s(left, right []float32) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
