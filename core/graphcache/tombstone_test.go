package graphcache

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

func TestDeleteHLCOutcomesRespectCausalAdmissionAndCapacity(t *testing.T) {
	expiration := time.Now().Add(time.Hour)
	older := hlc.Timestamp{WallNs: 10}
	newer := hlc.Timestamp{WallNs: 20}

	t.Run("vertex and edge positions", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		if !c.PutVertexWithExpirationHLC("protected", "value", expiration, newer) ||
			!c.PutVertexWithExpirationHLC("deletable", "value", expiration, older) {
			t.Fatal("seed vertex Put failed")
		}
		vertex, err := c.DeleteVerticesHLCOutcomesChecked(
			[]string{"protected", "missing", "protected", "deletable", "deletable"}, older, expiration)
		if err != nil || !slices.Equal(vertex, []bool{false, false, false, true, false}) {
			t.Fatalf("vertex outcomes = (%v, %v)", vertex, err)
		}
		if _, ok := c.GetVertex("protected"); !ok {
			t.Fatal("older Delete removed protected vertex")
		}
		if !c.PutEdgeWithExpirationHLC("a", "protected", 1, expiration, newer) ||
			!c.PutEdgeWithExpirationHLC("a", "deletable", 1, expiration, older) {
			t.Fatal("seed edge Put failed")
		}
		edge, err := c.DeleteEdgesHLCOutcomesChecked([]EdgeKey[string]{
			{Tail: "a", Head: "protected"}, {Tail: "missing", Head: "edge"},
			{Tail: "a", Head: "protected"}, {Tail: "a", Head: "deletable"},
			{Tail: "a", Head: "deletable"},
		}, older, expiration)
		if err != nil || !slices.Equal(edge, []bool{false, false, false, true, false}) {
			t.Fatalf("edge outcomes = (%v, %v)", edge, err)
		}
		if _, ok := c.GetWeight("a", "protected"); !ok {
			t.Fatal("older Delete removed protected edge")
		}
	})

	t.Run("edge newer Add survives repeated older Delete observations", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		contrib := ContribID{0: 1}
		if !c.AddEdgeWithExpirationContribHLC("a", "b", 1, expiration, contrib, hlc.Timestamp{WallNs: 30}) {
			t.Fatal("seed Add failed")
		}
		outcomes, err := c.DeleteEdgesHLCOutcomesChecked([]EdgeKey[string]{
			{Tail: "a", Head: "b"}, {Tail: "a", Head: "b"},
		}, newer, expiration)
		if err != nil || !slices.Equal(outcomes, []bool{true, true}) {
			t.Fatalf("newer-contribution Delete outcomes = (%v, %v)", outcomes, err)
		}
		if weight, ok := c.GetWeight("a", "b"); !ok || weight != 1 {
			t.Fatalf("newer Add was removed: (%g, %v)", weight, ok)
		}
	})

	t.Run("capacity rejection returns no outcomes and changes no graph state", func(t *testing.T) {
		vertices := NewGraphCache[string, string](time.Minute)
		vertices.SetCausalMetadataLimits(CausalMetadataLimits{MaxVertexEntries: 1})
		if !vertices.PutVertexWithExpirationHLC("present", "value", expiration, older) {
			t.Fatal("seed vertex Put failed")
		}
		outcomes, err := vertices.DeleteVerticesHLCOutcomesChecked(
			[]string{"present", "new"}, newer, expiration)
		var capacity *CausalMetadataCapacityError
		if outcomes != nil || !errors.As(err, &capacity) {
			t.Fatalf("vertex capacity = (%v, %v)", outcomes, err)
		}
		if _, ok := vertices.GetVertex("present"); !ok {
			t.Fatal("rejected vertex batch partially deleted")
		}

		edges := NewGraphCache[string, string](time.Minute)
		edges.SetCausalMetadataLimits(CausalMetadataLimits{MaxEdgeEntries: 1})
		if !edges.PutEdgeWithExpirationHLC("a", "b", 1, expiration, older) {
			t.Fatal("seed edge Put failed")
		}
		outcomes, err = edges.DeleteEdgesHLCOutcomesChecked([]EdgeKey[string]{
			{Tail: "a", Head: "b"}, {Tail: "a", Head: "new"},
		}, newer, expiration)
		if outcomes != nil || !errors.As(err, &capacity) {
			t.Fatalf("edge capacity = (%v, %v)", outcomes, err)
		}
		if _, ok := edges.GetWeight("a", "b"); !ok {
			t.Fatal("rejected edge batch partially deleted")
		}
	})
}

func TestDeleteHLCDecisionsCheckedRetainsAcceptedRequestPositions(t *testing.T) {
	expiration := time.Now().Add(time.Hour)
	older := hlc.Timestamp{WallNs: 10}
	newer := hlc.Timestamp{WallNs: 20}

	t.Run("vertices", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		if !c.PutVertexWithExpirationHLC("protected", "value", expiration, newer) ||
			!c.PutVertexWithExpirationHLC("live", "value", expiration, older) {
			t.Fatal("seed vertex Put failed")
		}
		existed, accepted, err := c.DeleteVerticesHLCDecisionsChecked(
			[]string{"protected", "absent", "protected", "live", "live"}, older, expiration)
		if err != nil || !slices.Equal(existed, []bool{false, false, false, true, false}) ||
			!slices.Equal(accepted, []int{1, 3, 4}) {
			t.Fatalf("vertex decisions = existed %v, accepted %v, error %v", existed, accepted, err)
		}
		if _, ok := c.GetVertex("protected"); !ok {
			t.Fatal("rejected Delete removed protected vertex")
		}
		if c.PutVertexWithExpirationHLC("absent", "stale", expiration, hlc.Timestamp{WallNs: 9}) {
			t.Fatal("accepted absent-key Delete did not retain a tombstone")
		}
	})

	t.Run("edges", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		if !c.PutEdgeWithExpirationHLC("a", "protected", 1, expiration, newer) ||
			!c.PutEdgeWithExpirationHLC("a", "live", 1, expiration, older) {
			t.Fatal("seed edge Put failed")
		}
		existed, accepted, err := c.DeleteEdgesHLCDecisionsChecked([]EdgeKey[string]{
			{Tail: "a", Head: "protected"}, {Tail: "a", Head: "absent"},
			{Tail: "a", Head: "protected"}, {Tail: "a", Head: "live"}, {Tail: "a", Head: "live"},
		}, older, expiration)
		if err != nil || !slices.Equal(existed, []bool{false, false, false, true, false}) ||
			!slices.Equal(accepted, []int{1, 3, 4}) {
			t.Fatalf("edge decisions = existed %v, accepted %v, error %v", existed, accepted, err)
		}
		if _, ok := c.GetWeight("a", "protected"); !ok {
			t.Fatal("rejected Delete removed protected edge")
		}
		if c.PutEdgeWithExpirationHLC("a", "absent", 1, expiration, hlc.Timestamp{WallNs: 9}) {
			t.Fatal("accepted absent-edge Delete did not retain a tombstone")
		}
	})

	t.Run("newer edge Add survives two accepted Delete positions", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		key := EdgeKey[string]{Tail: "a", Head: "b"}
		if !c.AddEdgeWithExpirationContribHLC(key.Tail, key.Head, 1, expiration, ContribID{1}, hlc.Timestamp{WallNs: 30}) {
			t.Fatal("seed newer Add failed")
		}
		existed, accepted, err := c.DeleteEdgesHLCDecisionsChecked([]EdgeKey[string]{key, key}, newer, expiration)
		if err != nil || !slices.Equal(existed, []bool{true, true}) || !slices.Equal(accepted, []int{0, 1}) {
			t.Fatalf("duplicate edge decisions = %v, %v, %v", existed, accepted, err)
		}
		if weight, ok := c.GetWeight(key.Tail, key.Head); !ok || weight != 1 {
			t.Fatalf("newer Add changed after accepted Deletes: %g, %v", weight, ok)
		}
	})

	t.Run("capacity errors return no decision or partial effect", func(t *testing.T) {
		vertices := NewGraphCache[string, string](time.Minute)
		vertices.SetCausalMetadataLimits(CausalMetadataLimits{MaxVertexEntries: 1})
		if !vertices.PutVertexWithExpirationHLC("present", "value", expiration, older) {
			t.Fatal("seed vertex Put failed")
		}
		existed, accepted, err := vertices.DeleteVerticesHLCDecisionsChecked([]string{"present", "new"}, newer, expiration)
		var capacity *CausalMetadataCapacityError
		if existed != nil || accepted != nil || !errors.As(err, &capacity) {
			t.Fatalf("vertex capacity = %v, %v, %v", existed, accepted, err)
		}
		if _, ok := vertices.GetVertex("present"); !ok {
			t.Fatal("capacity error partially deleted vertex")
		}

		edges := NewGraphCache[string, string](time.Minute)
		edges.SetCausalMetadataLimits(CausalMetadataLimits{MaxEdgeEntries: 1})
		if !edges.PutEdgeWithExpirationHLC("a", "present", 1, expiration, older) {
			t.Fatal("seed edge Put failed")
		}
		existed, accepted, err = edges.DeleteEdgesHLCDecisionsChecked([]EdgeKey[string]{
			{Tail: "a", Head: "present"}, {Tail: "a", Head: "new"},
		}, newer, expiration)
		if existed != nil || accepted != nil || !errors.As(err, &capacity) {
			t.Fatalf("edge capacity = %v, %v, %v", existed, accepted, err)
		}
		if _, ok := edges.GetWeight("a", "present"); !ok {
			t.Fatal("capacity error partially deleted edge")
		}
	})
}

// After DeleteVertexHLC at T1, a later Put with strictly-older T0 must
// not resurrect the key. A Put with T2 > T1 (or equal) is allowed and
// must clear the tombstone so further T1 Puts are accepted as normal LWW.
func TestDeleteVertexHLC_BlocksOlderPut(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	exp := time.Now().Add(time.Hour)

	t0 := hlc.Timestamp{WallNs: 1000}
	t1 := hlc.Timestamp{WallNs: 2000}
	t2 := hlc.Timestamp{WallNs: 3000}

	if !c.PutVertexWithExpirationHLC("v", "live", exp, t0) {
		t.Fatalf("seed put failed")
	}
	if !c.DeleteVertexHLC("v", t1, exp) {
		t.Fatalf("delete: want existed=true")
	}
	if _, ok := c.GetVertex("v"); ok {
		t.Fatalf("vertex still present after delete")
	}
	// strictly-older Put must be dropped
	if c.PutVertexWithExpirationHLC("v", "stale", exp, t0) {
		t.Fatalf("older put: want applied=false (tombstone)")
	}
	if _, ok := c.GetVertex("v"); ok {
		t.Fatalf("older put resurrected vertex")
	}
	// newer Put applies and clears the tombstone
	if !c.PutVertexWithExpirationHLC("v", "new", exp, t2) {
		t.Fatalf("newer put: want applied=true")
	}
	got, ok := c.GetVertex("v")
	if !ok || got != "new" {
		t.Fatalf("vertex: got (%q,%v), want (\"new\",true)", got, ok)
	}
}

// DeleteEdgeHLC tombstone blocks older Add and older Put on the same
// (tail, head) pair without affecting parallel edges in the reverse
// direction.
func TestDeleteEdgeHLC_BlocksOlderAddAndPut(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	exp := time.Now().Add(time.Hour)

	t0 := hlc.Timestamp{WallNs: 1000}
	t1 := hlc.Timestamp{WallNs: 2000}

	cID := ContribID{0: 1}
	if !c.AddEdgeWithExpirationContribHLC("a", "b", 1.0, exp, cID, t0) {
		t.Fatalf("seed add failed")
	}
	if !c.DeleteEdgeHLC("a", "b", t1, exp) {
		t.Fatalf("delete: want existed=true")
	}
	if _, ok := c.GetWeight("a", "b"); ok {
		t.Fatalf("edge still present after delete")
	}
	// older Add dropped
	if c.AddEdgeWithExpirationContribHLC("a", "b", 9.0, exp, cID, t0) {
		t.Fatalf("older add: want applied=false")
	}
	if _, ok := c.GetWeight("a", "b"); ok {
		t.Fatalf("older add resurrected edge")
	}
	// older Put dropped
	if c.PutEdgeWithExpirationHLC("a", "b", 9.0, exp, t0) {
		t.Fatalf("older put: want applied=false")
	}
	// reverse direction unaffected
	if !c.PutEdgeWithExpirationHLC("b", "a", 2.0, exp, t0) {
		t.Fatalf("reverse-direction put must succeed; tombstone is per-(tail,head)")
	}
}

func TestDeleteHLCSupersedesCausalBarrierWithBoundedTombstone(t *testing.T) {
	barrierTS := hlc.Timestamp{WallNs: 20}
	deleteTS := hlc.Timestamp{WallNs: 30}
	olderTS := hlc.Timestamp{WallNs: 10}
	tombstoneExpiration := time.Now().Add(time.Hour)
	liveExpiration := time.Now().Add(time.Hour)
	expired := time.Now().Add(-time.Hour)

	t.Run("vertex", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		if !c.PutVertexWithExpirationHLC("v", "dead", expired, barrierTS) {
			t.Fatal("expired Put rejected")
		}
		c.DeleteVertexHLC("v", deleteTS, tombstoneExpiration)
		if vertices, _ := c.CausalBarrierCounts(); vertices != 0 {
			t.Fatalf("vertex barriers after Delete = %d, want 0", vertices)
		}
		if c.PutVertexWithExpirationHLC("v", "older", liveExpiration, olderTS) {
			t.Fatal("bounded tombstone did not fence older vertex Put")
		}
	})

	t.Run("edge", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		if !c.PutEdgeWithExpirationHLC("tail", "head", 1, expired, barrierTS) {
			t.Fatal("expired Put rejected")
		}
		c.DeleteEdgeHLC("tail", "head", deleteTS, tombstoneExpiration)
		if _, edges := c.CausalBarrierCounts(); edges != 0 {
			t.Fatalf("edge barriers after Delete = %d, want 0", edges)
		}
		if c.PutEdgeWithExpirationHLC("tail", "head", 9, liveExpiration, olderTS) {
			t.Fatal("bounded tombstone did not fence older edge Put")
		}
	})
}

func TestDeleteHLCRejectsOlderBeforePhysicalMutation(t *testing.T) {
	older := hlc.Timestamp{WallNs: 10}
	floor := hlc.Timestamp{WallNs: 20}
	tombstoneExpiration := time.Now().Add(time.Hour)
	liveExpiration := time.Now().Add(time.Hour)
	expired := time.Now().Add(-time.Hour)

	t.Run("vertex live and barrier", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		if !c.PutVertexWithExpirationHLC("live", "value", liveExpiration, floor) ||
			!c.PutVertexWithExpirationHLC("barrier", "dead", expired, floor) {
			t.Fatal("seed Put rejected")
		}
		if c.DeleteVertexHLC("live", older, tombstoneExpiration) {
			t.Fatal("older singular Delete reported live removal")
		}
		if value, ok := c.GetVertex("live"); !ok || value != "value" {
			t.Fatalf("older Delete changed newer live vertex: %q/%v", value, ok)
		}
		if got := c.DeleteVerticesHLC([]string{"live", "barrier", "barrier"}, older, tombstoneExpiration); got != 0 {
			t.Fatalf("older batch Delete removed %d vertices, want 0", got)
		}
		if _, ok := c.GetVertex("live"); !ok {
			t.Fatal("older batch Delete removed newer live vertex")
		}
		if got := c.vertexCausalBarriers["barrier"]; got != floor {
			t.Fatalf("older batch Delete changed barrier to %+v, want %+v", got, floor)
		}
		if got := c.DeleteVerticesHLC([]string{"live", "live", "barrier", "barrier"}, floor, tombstoneExpiration); got != 1 {
			t.Fatalf("equal duplicate batch Delete count = %d, want 1", got)
		}
		if vertices, _ := c.CausalBarrierCounts(); vertices != 0 {
			t.Fatalf("equal Delete left %d vertex barriers", vertices)
		}
	})

	t.Run("edge live and barrier", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		if !c.PutEdgeWithExpirationHLC("live-tail", "live-head", 2, liveExpiration, floor) ||
			!c.PutEdgeWithExpirationHLC("barrier-tail", "barrier-head", 1, expired, floor) {
			t.Fatal("seed Put rejected")
		}
		if c.DeleteEdgeHLC("live-tail", "live-head", older, tombstoneExpiration) {
			t.Fatal("older singular Delete reported live removal")
		}
		if weight, ok := c.GetWeight("live-tail", "live-head"); !ok || weight != 2 {
			t.Fatalf("older Delete changed newer live edge: %v/%v", weight, ok)
		}
		keys := []EdgeKey[string]{
			{Tail: "live-tail", Head: "live-head"},
			{Tail: "barrier-tail", Head: "barrier-head"},
			{Tail: "barrier-tail", Head: "barrier-head"},
		}
		if got := c.DeleteEdgesHLC(keys, older, tombstoneExpiration); got != 0 {
			t.Fatalf("older batch Delete removed %d edges, want 0", got)
		}
		if weight, ok := c.GetWeight("live-tail", "live-head"); !ok || weight != 2 {
			t.Fatalf("older batch Delete changed newer edge: %v/%v", weight, ok)
		}
		if got := c.edgeCausalBarriers[EdgeKey[string]{Tail: "barrier-tail", Head: "barrier-head"}]; got != floor {
			t.Fatalf("older batch Delete changed barrier to %+v, want %+v", got, floor)
		}
		keys = append(keys, EdgeKey[string]{Tail: "live-tail", Head: "live-head"})
		if got := c.DeleteEdgesHLC(keys, floor, tombstoneExpiration); got != 1 {
			t.Fatalf("equal duplicate batch Delete count = %d, want 1", got)
		}
		if _, edges := c.CausalBarrierCounts(); edges != 0 {
			t.Fatalf("equal Delete left %d edge barriers", edges)
		}
	})
}

// An expired tombstone (expiration in the past) must not block a
// strictly-older Put — sweepExpiredTombstonesLocked drops it lazily, and
// vertexTombstoneLocked treats already-expired entries as absent.
func TestVertexTombstone_ExpiredAllowsOlderPut(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	exp := time.Now().Add(time.Hour)

	t0 := hlc.Timestamp{WallNs: 1000}
	t1 := hlc.Timestamp{WallNs: 2000}

	if !c.PutVertexWithExpirationHLC("v", "live", exp, t0) {
		t.Fatalf("seed put failed")
	}
	// stamp tombstone already-expired
	if !c.DeleteVertexHLC("v", t1, time.Now().Add(-time.Second)) {
		t.Fatalf("delete: want existed=true")
	}
	// older Put now allowed because tombstone is dead
	if !c.PutVertexWithExpirationHLC("v", "back", exp, t0) {
		t.Fatalf("older put after expired tombstone: want applied=true")
	}
	got, ok := c.GetVertex("v")
	if !ok || got != "back" {
		t.Fatalf("vertex: got (%q,%v), want (\"back\",true)", got, ok)
	}
}

// DeleteByPrefixHLC stamps a tombstone per deleted vertex and the same
// older-Put gate then applies to each.
func TestDeleteByPrefixHLC_TombstonesPerVertex(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(func(k string) string { return k })
	exp := time.Now().Add(time.Hour)
	t0 := hlc.Timestamp{WallNs: 1000}
	t1 := hlc.Timestamp{WallNs: 2000}

	for _, k := range []string{"p:1", "p:2", "p:3", "q:1"} {
		if !c.PutVertexWithExpirationHLC(k, "v", exp, t0) {
			t.Fatalf("seed %q failed", k)
		}
	}
	n, err := c.DeleteByPrefixHLC(context.Background(), "p:", 0, t1, exp)
	if err != nil {
		t.Fatalf("DeleteByPrefixHLC: %v", err)
	}
	if n != 3 {
		t.Fatalf("deleted: got %d, want 3", n)
	}
	// q:1 untouched
	if _, ok := c.GetVertex("q:1"); !ok {
		t.Fatalf("q:1 was not in prefix; must survive")
	}
	// older Put on a tombstoned key must be dropped
	if c.PutVertexWithExpirationHLC("p:1", "back", exp, t0) {
		t.Fatalf("older put against tombstone: want applied=false")
	}
}

// TestDeleteVerticesHLC_BatchTombstonesAllDeletesPresent verifies the batched
// HLC delete (#738) still counts only keys that were present while stamping a
// tombstone on EVERY supplied key — including absent ones — so a late
// Delete-before-Add race is resolved by LWW once the Add arrives.
func TestDeleteVerticesHLC_BatchTombstonesAllDeletesPresent(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	c.EnableSearchIndex(textExtract, compareStringID)
	exp := time.Now().Add(time.Hour)
	t0 := hlc.Timestamp{WallNs: 1000}
	t1 := hlc.Timestamp{WallNs: 2000}

	for _, k := range []string{"present:1", "present:2"} {
		if !c.PutVertexWithExpirationHLC(k, "searchable payload", exp, t0) {
			t.Fatalf("seed %q failed", k)
		}
	}

	// "absent:1" was never stored; it must still receive a tombstone.
	n := c.DeleteVerticesHLC([]string{"present:1", "present:2", "absent:1"}, t1, exp)
	if n != 2 {
		t.Fatalf("DeleteVerticesHLC count = %d, want 2 (only present keys)", n)
	}

	// Present keys gone from point reads and both side indexes.
	for _, k := range []string{"present:1", "present:2"} {
		if _, ok := c.GetVertex(k); ok {
			t.Fatalf("%q still present after batch HLC delete", k)
		}
	}
	if got := c.CountByPrefix("present:"); got != 0 {
		t.Fatalf("CountByPrefix(present:) = %d, want 0", got)
	}
	if got := c.SearchVertices("searchable", 10, ""); got != nil {
		t.Fatalf("SearchVertices after batch HLC delete = %v, want nil", keys(got))
	}

	// Tombstone fences an older Put on the absent key too.
	if c.PutVertexWithExpirationHLC("absent:1", "late", exp, t0) {
		t.Fatalf("older put against absent-key tombstone: want applied=false")
	}
	// A newer Put is still accepted (tombstone is LWW, not permanent).
	if !c.PutVertexWithExpirationHLC("present:1", "revived", exp, hlc.Timestamp{WallNs: 3000}) {
		t.Fatalf("newer put after tombstone: want applied=true")
	}
}
