package graphcache

import (
	"reflect"
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
		c.EnablePrefixIndex(func(key string) string { return key })
		c.PutVertexWithExpiration("keep", "value", expiration)
		c.AddEdgeWithExpirationContrib("keep", "head", 7, expiration, ContribID{9})
		before := captureStagedDeleteState(c)
		tx, err := c.BeginEdgeAdd([]EdgeItem[string]{
			{Tail: "keep", Head: "head", Weight: 2, Expiration: expiration, ContribID: contrib},
			{Tail: "new-tail", Head: "new-head", Weight: 3, Expiration: expiration, ContribID: ContribID{2}},
			{Tail: "new-tail", Head: "new-head", Weight: 4, Expiration: expiration, ContribID: ContribID{3}},
		}, ts)
		if err != nil {
			t.Fatal(err)
		}
		tx.Abort()
		tx.Abort()
		if after := captureStagedDeleteState(c); !reflect.DeepEqual(after, before) {
			t.Fatalf("transaction Abort drift: before=%+v after=%+v", before, after)
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

	t.Run("replicated Add obeys a newer Delete floor", func(t *testing.T) {
		c := NewGraphCacheWithStaging[string, string](time.Hour)
		if _, err := c.DeleteEdgesHLCChecked(
			[]EdgeKey[string]{{Tail: "a", Head: "b"}},
			hlc.Timestamp{WallNs: ts.WallNs + 1, NodeID: ts.NodeID},
			expiration,
		); err != nil {
			t.Fatal(err)
		}
		tx, err := c.BeginReplicatedEdgeAdd([]EdgeItem[string]{
			{Tail: "a", Head: "b", Weight: 4, Expiration: expiration, ContribID: contrib},
		}, ts)
		if err != nil {
			t.Fatal(err)
		}
		result := tx.Result()
		if len(result.Accepted) != 0 || !equalFloat32s(result.Effective, []float32{0}) {
			t.Fatalf("Result = %+v, want rejected effective weight 0", result)
		}
		tx.Commit()
		if _, ok := c.GetWeight("a", "b"); ok {
			t.Fatal("causally rejected Add became visible")
		}
	})

	t.Run("born-expired result is the unchanged live sum", func(t *testing.T) {
		c := NewGraphCacheWithStaging[string, string](time.Hour)
		tx, err := c.BeginEdgeAdd([]EdgeItem[string]{
			{
				Tail: "a", Head: "b", Weight: 4,
				Expiration: time.Now().Add(-time.Hour), ContribID: contrib,
			},
		}, ts)
		if err != nil {
			t.Fatal(err)
		}
		result := tx.Result()
		if len(result.Accepted) != 1 || !equalFloat32s(result.Effective, []float32{0}) {
			t.Fatalf("Result = %+v, want accepted live effective weight 0", result)
		}
		tx.Commit()
		if _, ok := c.GetWeight("a", "b"); ok {
			t.Fatal("born-expired Add remained live")
		}
	})
}

func TestStagedEdgeAddRejectedHistoryAllocationIsPerEdge(t *testing.T) {
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	expiration := time.Now().Add(time.Hour)
	putFloor := hlc.Timestamp{WallNs: 100, NodeID: hlc.NodeID{1}}
	if !c.PutEdgeWithExpirationHLC("hot", "edge", 1, expiration, putFloor) {
		t.Fatal("failed to install Put floor")
	}
	for i := 0; i < 128; i++ {
		contribID := ContribID{byte(i + 1), byte(i >> 8)}
		if !c.AddEdgeWithExpirationContribHLC(
			"hot",
			"edge",
			1,
			expiration,
			contribID,
			hlc.Timestamp{WallNs: int64(101 + i), NodeID: hlc.NodeID{1}},
		) {
			t.Fatalf("history Add %d was rejected", i)
		}
	}
	measure := func(count int) float64 {
		items := make([]EdgeItem[string], count)
		for i := range items {
			items[i] = EdgeItem[string]{
				Tail: "hot", Head: "edge", Weight: 1,
				Expiration: expiration, ContribID: ContribID{0xff, byte(i)},
			}
		}
		return testing.AllocsPerRun(10, func() {
			c.mu.Lock()
			plans, effective, accepted, err := c.planStagedEdgeAddLocked(
				items,
				putFloor,
				expiration.Add(-time.Minute),
			)
			c.mu.Unlock()
			if err != nil {
				panic(err)
			}
			if len(plans) != 0 || len(effective) != count || len(accepted) != count {
				panic("rejected Add planning result drift")
			}
		})
	}
	single := measure(1)
	repeated := measure(256)
	if repeated > single+32 {
		t.Fatalf(
			"repeated rejected edge allocations = %.0f, single = %.0f; history was cloned per request item",
			repeated,
			single,
		)
	}
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
