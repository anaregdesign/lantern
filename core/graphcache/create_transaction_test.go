package graphcache

import (
	"math"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

func TestEdgeCreateTransaction(t *testing.T) {
	now := time.Now()
	ts := hlc.Timestamp{WallNs: now.UnixNano(), Logical: 1, NodeID: hlc.NodeID{1}}
	fixture := func() *GraphCache[string, string] {
		c := NewGraphCacheWithStaging[string, string](time.Hour)
		c.EnablePrefixIndex(identityExtract)
		c.applicationClock = func() time.Time { return now }
		for _, key := range []string{"a", "b", "c", "d"} {
			c.PutVertexWithExpiration(key, "value-"+key, now.Add(time.Hour))
		}
		return c
	}
	t.Run("aligned duplicates and existing endpoints only", func(t *testing.T) {
		c := fixture()
		before := c.SnapshotGraph()
		tx, err := c.BeginEdgeCreate([]EdgeItem[string]{{Tail: "a", Head: "b", Weight: 2}, {Tail: "a", Head: "b", Weight: 9}, {Tail: "a", Head: "missing", Weight: 2}, {Tail: "missing", Head: "missing2", Weight: 2, Expiration: now}}, ts)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Abort()
		result := tx.Result()
		if !reflect.DeepEqual(result.Outcomes, []CreateOutcome{CreateOutcomeCreatedAndLive, CreateOutcomeEdgeExists, CreateOutcomeEndpointNotLive, CreateOutcomeExpired}) || len(result.Accepted) != 1 || result.Accepted[0].Index != 0 {
			t.Fatal(result)
		}
		result.Outcomes[0] = CreateOutcomeExpired
		result.Accepted[0].Item.Weight = 99
		if tx.Result().Outcomes[0] != CreateOutcomeCreatedAndLive || tx.Result().Accepted[0].Item.Weight != 2 {
			t.Fatal("result aliases authoritative state")
		}
		tx.Commit()
		if weight, live := c.GetWeight("a", "b"); !live || weight != 2 {
			t.Fatal("duplicate overwrote original creation", weight, live)
		}
		after := c.SnapshotGraph()
		sort.Slice(before.Vertices, func(i, j int) bool { return before.Vertices[i].Key < before.Vertices[j].Key })
		sort.Slice(after.Vertices, func(i, j int) bool { return after.Vertices[i].Key < after.Vertices[j].Key })
		if !reflect.DeepEqual(after.Vertices, before.Vertices) {
			t.Fatal("Create changed endpoint value, TTL or HLC")
		}
		if c.vertices.Has("missing") || c.vertices.Has("missing2") {
			t.Fatal("Create fabricated an endpoint")
		}
	})
	t.Run("collision preserves contributions including zero aggregate", func(t *testing.T) {
		c := fixture()
		c.AddEdgeWithExpirationContrib("a", "b", 3, now.Add(time.Hour), ContribID{1})
		c.AddEdgeWithExpirationContrib("a", "b", -3, now.Add(time.Hour), ContribID{2})
		before := captureStagedDeleteState(c)
		tx, err := c.BeginEdgeCreate([]EdgeItem[string]{{Tail: "a", Head: "b", Weight: 9, Expiration: now.Add(2 * time.Hour)}}, ts)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(tx.Result().Outcomes, []CreateOutcome{CreateOutcomeEdgeExists}) {
			t.Fatal(tx.Result())
		}
		tx.Commit()
		if !reflect.DeepEqual(captureStagedDeleteState(c), before) {
			t.Fatal("collision altered a live contribution or TTL")
		}
	})
	t.Run("abort restores expired bucket and every index", func(t *testing.T) {
		c := fixture()
		c.AddEdgeWithExpirationContrib("a", "b", 7, now.Add(time.Minute), ContribID{1})
		now = now.Add(2 * time.Minute)
		before := captureStagedDeleteState(c)
		tx, err := c.BeginEdgeCreate([]EdgeItem[string]{{Tail: "a", Head: "b", Weight: 2}, {Tail: "c", Head: "d", Weight: 3}}, ts)
		if err != nil {
			t.Fatal(err)
		}
		if len(tx.Result().Accepted) != 2 {
			t.Fatal(tx.Result())
		}
		tx.Abort()
		tx.Abort()
		if after := captureStagedDeleteState(c); !reflect.DeepEqual(after, before) {
			t.Fatalf("abort drift: before=%+v after=%+v", before, after)
		}
	})
	t.Run("final application time controls TTL", func(t *testing.T) {
		c := fixture()
		cut := now.Add(time.Second)
		c.PutVertexWithExpiration("b", "existing", cut)
		samples := 0
		c.applicationClock = func() time.Time { samples++; return cut }
		tx, err := c.BeginEdgeCreate([]EdgeItem[string]{{Tail: "a", Head: "b", Weight: 2}, {Tail: "a", Head: "c", Weight: 2, Expiration: cut}}, ts)
		if err != nil {
			t.Fatal(err)
		}
		if samples != 1 || !reflect.DeepEqual(tx.Result().Outcomes, []CreateOutcome{CreateOutcomeEndpointNotLive, CreateOutcomeExpired}) {
			t.Fatal(samples, tx.Result())
		}
		tx.Commit()
		if _, live := c.GetWeight("a", "b"); live {
			t.Fatal("expired endpoint was resurrected")
		}
	})
	t.Run("stale causal floor fails before any effects", func(t *testing.T) {
		c := fixture()
		c.DeleteEdgeHLC("c", "d", hlc.Timestamp{WallNs: ts.WallNs + 1, NodeID: ts.NodeID}, now.Add(time.Hour))
		before := captureStagedDeleteState(c)
		if _, err := c.BeginEdgeCreate([]EdgeItem[string]{{Tail: "a", Head: "b", Weight: 1}, {Tail: "c", Head: "d", Weight: 1}}, ts); err == nil {
			t.Fatal("stale conditional creation admitted")
		}
		if !reflect.DeepEqual(captureStagedDeleteState(c), before) {
			t.Fatal("stale batch partially created an earlier Edge")
		}
	})
	t.Run("projection executes outside exclusive locks", func(t *testing.T) {
		c := NewGraphCacheWithStaging[string, string](time.Hour)
		c.EnablePrefixIndex(func(key string) string { _, _ = c.GetVertex(key); return key })
		for _, key := range []string{"a", "b"} {
			c.PutVertex(key, key)
		}
		tx, err := c.BeginEdgeCreate([]EdgeItem[string]{{Tail: "a", Head: "b", Weight: 1}}, ts)
		if err != nil {
			t.Fatal(err)
		}
		tx.Commit()
		if value, _ := c.GetWeight("a", "b"); value != 1 {
			t.Fatal(value)
		}
	})
	t.Run("invalid input is rejected before mutation", func(t *testing.T) {
		c := fixture()
		for _, item := range []EdgeItem[string]{{Weight: 0}, {Weight: float32(math.NaN())}, {Weight: float32(math.Inf(1))}, {Weight: 1, ContribID: ContribID{1}}} {
			item.Tail, item.Head = "a", "b"
			if _, err := c.BeginEdgeCreate([]EdgeItem[string]{item}, ts); err == nil {
				t.Fatal("invalid Create input accepted", item)
			}
		}
		if c.edges.edgeCount != 0 {
			t.Fatal("invalid creation changed Edge stats")
		}
		if _, err := NewGraphCache[string, string](time.Hour).BeginEdgeCreate(nil, ts); err == nil {
			t.Fatal("unstaged graph admitted conditional publication")
		}
	})
}

func TestEdgeCreateTransactionSerializesDeleteAndConcurrentCreate(t *testing.T) {
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	c.PutVertex("a", "a")
	c.PutVertex("b", "b")
	var wg sync.WaitGroup
	var mu sync.Mutex
	created := 0
	for range 16 {
		wg.Go(func() {
			tx, err := c.BeginEdgeCreate([]EdgeItem[string]{{Tail: "a", Head: "b", Weight: 1}}, hlc.Timestamp{})
			if err != nil {
				t.Error(err)
				return
			}
			result := tx.Result()
			tx.Commit()
			if result.Outcomes[0] == CreateOutcomeCreatedAndLive {
				mu.Lock()
				created++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if created != 1 {
		t.Fatal("local atomic absence admitted multiple creations", created)
	}
	if !c.DeleteVertex("b") {
		t.Fatal("endpoint Delete failed")
	}
	tx, err := c.BeginEdgeCreate([]EdgeItem[string]{{Tail: "a", Head: "b", Weight: 2}}, hlc.Timestamp{})
	if err != nil {
		t.Fatal(err)
	}
	if tx.Result().Outcomes[0] != CreateOutcomeEndpointNotLive {
		t.Fatal("Delete did not fence Create", tx.Result())
	}
	tx.Commit()
	if c.vertices.Has("b") {
		t.Fatal("Create resurrected a deleted endpoint")
	}
}

func BenchmarkEdgeCreateTransaction(b *testing.B) {
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	c.EnablePrefixIndex(identityExtract)
	c.PutVertex("a", "a")
	c.PutVertex("b", "b")
	item := []EdgeItem[string]{{Tail: "a", Head: "b", Weight: 1}}
	b.ReportAllocs()
	for b.Loop() {
		tx, err := c.BeginEdgeCreate(item, hlc.Timestamp{})
		if err != nil {
			b.Fatal(err)
		}
		tx.Commit()
		c.DeleteEdge("a", "b")
	}
}

func TestEdgeCreateReplayPreservesAbsenceAndFloors(t *testing.T) {
	now := time.Now()
	ts := hlc.Timestamp{WallNs: now.UnixNano(), NodeID: hlc.NodeID{1}}
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	c.applicationClock = func() time.Time { return now }
	c.PutVertex("a", "a")
	c.PutVertex("b", "b")
	c.DeleteEdgeHLC("a", "b", hlc.Timestamp{WallNs: ts.WallNs + 1, NodeID: ts.NodeID}, now.Add(time.Hour))
	tx, err := c.BeginEdgeCreateReplay([]EdgeItem[string]{{Tail: "a", Head: "b", Weight: 2}, {Tail: "a", Head: "missing", Weight: 2}, {Tail: "a", Head: "b", Weight: 2, Expiration: now}}, ts)
	if err != nil {
		t.Fatal(err)
	}
	if len(tx.Result().Accepted) != 0 {
		t.Fatal("replay resurrected rejected/expired/newer-deleted item")
	}
	tx.Commit()
	if _, found := c.GetVertex("missing"); found {
		t.Fatal("replay fabricated endpoint")
	}
	c.AddEdge("b", "a", 3)
	if _, err := c.BeginEdgeCreateReplay([]EdgeItem[string]{{Tail: "b", Head: "a", Weight: 9}}, ts); err == nil {
		t.Fatal("accepted replay overwrote live collision")
	}
	if weight, _ := c.GetWeight("b", "a"); weight != 3 {
		t.Fatal(weight)
	}
}
