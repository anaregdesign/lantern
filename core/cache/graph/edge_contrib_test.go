package graph

import (
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

// TestAddEdgeWithExpirationContrib_Dedup is the #181 acceptance test:
// re-applying the same (tail, head, contribID) leaves the stored weight
// unchanged — peer reconnects and duplicate stream deliveries (#185)
// must not double-count.
func TestAddEdgeWithExpirationContrib_Dedup(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	id := ContribID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 0, 0, 0, 0, 0, 0, 0, 42}

	expiration := time.Now().Add(time.Minute)
	if !c.AddEdgeWithExpirationContrib("a", "b", 1.5, expiration, id) {
		t.Fatalf("first add: want applied=true")
	}
	if c.AddEdgeWithExpirationContrib("a", "b", 1.5, expiration, id) {
		t.Fatalf("second add: want applied=false (dedup)")
	}
	if c.AddEdgeWithExpirationContrib("a", "b", 1.5, expiration, id) {
		t.Fatalf("third add: want applied=false (dedup)")
	}

	got, ok := c.GetWeight("a", "b")
	if !ok {
		t.Fatalf("edge a→b missing")
	}
	if got != 1.5 {
		t.Errorf("weight: got %v, want 1.5 (one contribution despite three Add calls)", got)
	}
}

// Zero ContribID disables dedup — local (non-replicated) Add semantics
// must remain additive when callers do not opt in.
func TestAddEdgeWithExpirationContrib_ZeroIDStillAdditive(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	expiration := time.Now().Add(time.Minute)

	for i := 0; i < 3; i++ {
		if !c.AddEdgeWithExpirationContrib("a", "b", 1.0, expiration, ContribID{}) {
			t.Fatalf("call %d: want applied=true (zero contrib never dedups)", i)
		}
	}
	got, ok := c.GetWeight("a", "b")
	if !ok {
		t.Fatalf("edge missing")
	}
	if got != 3.0 {
		t.Errorf("weight: got %v, want 3.0 (three contributions, no dedup)", got)
	}
}

// Distinct ContribIDs must accumulate — only the *same* identity dedups.
func TestAddEdgeWithExpirationContrib_DistinctIDsAccumulate(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	expiration := time.Now().Add(time.Minute)

	id1 := ContribID{0: 1}
	id2 := ContribID{0: 2}
	id3 := ContribID{0: 3}

	if !c.AddEdgeWithExpirationContrib("a", "b", 1.0, expiration, id1) {
		t.Fatalf("id1: want applied=true")
	}
	if !c.AddEdgeWithExpirationContrib("a", "b", 1.0, expiration, id2) {
		t.Fatalf("id2: want applied=true")
	}
	if !c.AddEdgeWithExpirationContrib("a", "b", 1.0, expiration, id3) {
		t.Fatalf("id3: want applied=true")
	}

	got, _ := c.GetWeight("a", "b")
	if got != 3.0 {
		t.Errorf("weight: got %v, want 3.0", got)
	}
}

// PutVertexWithExpirationHLC enforces LWW: a strictly-older HLC must not
// overwrite a newer one. Equal HLCs apply (idempotent re-apply remains a
// no-op semantically because the value is the same).
func TestPutVertexWithExpirationHLC_LWW(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	expiration := time.Now().Add(time.Minute)

	older := hlc.Timestamp{WallNs: 1000}
	newer := hlc.Timestamp{WallNs: 2000}

	if !c.PutVertexWithExpirationHLC("v", "first", expiration, newer) {
		t.Fatalf("first put: want applied=true")
	}
	if c.PutVertexWithExpirationHLC("v", "stale", expiration, older) {
		t.Fatalf("older put: want applied=false (LWW)")
	}
	got, ok := c.GetVertex("v")
	if !ok {
		t.Fatalf("vertex missing")
	}
	if got != "first" {
		t.Errorf("value: got %q, want %q (newer HLC must win)", got, "first")
	}
}

// PutEdgeWithExpirationHLC enforces LWW on the edge weight.
func TestPutEdgeWithExpirationHLC_LWW(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	expiration := time.Now().Add(time.Minute)

	older := hlc.Timestamp{WallNs: 1000}
	newer := hlc.Timestamp{WallNs: 2000}

	if !c.PutEdgeWithExpirationHLC("a", "b", 2.5, expiration, newer) {
		t.Fatalf("first put: want applied=true")
	}
	if c.PutEdgeWithExpirationHLC("a", "b", 9.9, expiration, older) {
		t.Fatalf("older put: want applied=false (LWW)")
	}
	got, ok := c.GetWeight("a", "b")
	if !ok {
		t.Fatalf("edge missing")
	}
	if got != 2.5 {
		t.Errorf("weight: got %v, want 2.5 (newer HLC must win)", got)
	}
}

// ContribID.IsZero distinguishes the zero value (no identity) from a
// populated one with a low byte — guards against accidental "all bytes
// must be set" implementations.
func TestContribID_IsZero(t *testing.T) {
	if !(ContribID{}).IsZero() {
		t.Errorf("zero value: IsZero=false, want true")
	}
	if (ContribID{0: 1}).IsZero() {
		t.Errorf("populated low byte: IsZero=true, want false")
	}
	if (ContribID{23: 1}).IsZero() {
		t.Errorf("populated high byte: IsZero=true, want false")
	}
}
