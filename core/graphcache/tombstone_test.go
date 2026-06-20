package graphcache

import (
	"context"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

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
	c.EnableSearchIndex(textExtract)
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
