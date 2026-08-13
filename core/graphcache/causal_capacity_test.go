package graphcache

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

func TestCausalMetadataCapacityVertex(t *testing.T) {
	t.Run("local rejection is atomic and replication remains convergent", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		c.SetCausalMetadataLimits(CausalMetadataLimits{MaxVertexEntries: 1})
		now := time.Now()
		expired := now.Add(-time.Second)
		live := now.Add(time.Hour)

		outcomes, err := c.PutVerticesWithExpirationHLCOutcomesChecked([]VertexItem[string, string]{
			{Key: "first", Value: "expired", Expiration: expired},
		}, hlc.Timestamp{WallNs: 10})
		if err != nil || len(outcomes) != 1 || outcomes[0] != PutOutcomeExpired {
			t.Fatalf("first Put = (%v, %v), want [EXPIRED], nil", outcomes, err)
		}

		_, err = c.PutVerticesWithExpirationHLCOutcomesChecked([]VertexItem[string, string]{
			{Key: "rejected", Value: "live", Expiration: live},
		}, hlc.Timestamp{WallNs: 20})
		var capacityErr *CausalMetadataCapacityError
		if !errors.As(err, &capacityErr) || capacityErr.Kind != "vertex" {
			t.Fatalf("second Put error = %v, want vertex capacity error", err)
		}
		if _, ok := c.GetVertex("rejected"); ok {
			t.Fatal("rejected local Put changed live graph")
		}
		if _, ok := c.vertexCausalBarriers["rejected"]; ok {
			t.Fatal("rejected local Put retained a barrier")
		}

		// Replication apply deliberately bypasses the local budget so a
		// converging peer never drops state another replica committed.
		got := c.PutVerticesWithExpirationHLCOutcomes([]VertexItem[string, string]{
			{Key: "replicated", Value: "live", Expiration: live},
		}, hlc.Timestamp{WallNs: 30})
		if len(got) != 1 || got[0] != PutOutcomeAppliedAndLive {
			t.Fatalf("replicated Put outcomes = %v", got)
		}
		stats := c.CausalMetadataStats()
		if stats.VertexEntries != 2 || stats.VertexRejected != 1 || !stats.VertexOverLimit {
			t.Fatalf("stats = %+v, want entries=2 rejected=1 over-limit", stats)
		}
		if stats.VertexEntriesHighWater != 2 || stats.VertexEstimatedBytesHighWater < stats.VertexEstimatedBytes {
			t.Fatalf("high-water stats = %+v", stats)
		}
		// Once replication has taken the node over its configured local
		// budget, replacements of already-retained identities still apply;
		// only another new identity is refused.
		got = c.PutVerticesWithExpirationHLCOutcomes([]VertexItem[string, string]{
			{Key: "third-replicated", Value: "live", Expiration: live},
		}, hlc.Timestamp{WallNs: 40})
		if len(got) != 1 || got[0] != PutOutcomeAppliedAndLive {
			t.Fatalf("second replicated Put outcomes = %v", got)
		}
		if _, err := c.PutVerticesWithExpirationHLCOutcomesChecked([]VertexItem[string, string]{
			{Key: "first", Value: "replacement", Expiration: live},
		}, hlc.Timestamp{WallNs: 50}); err != nil {
			t.Fatalf("existing identity replacement over limit: %v", err)
		}
	})

	t.Run("barrier to tombstone transition keeps one slot", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		c.SetCausalMetadataLimits(CausalMetadataLimits{MaxVertexEntries: 1})
		ts := hlc.Timestamp{WallNs: 10}
		_, err := c.PutVerticesWithExpirationHLCOutcomesChecked([]VertexItem[string, string]{
			{Key: "key", Value: "expired", Expiration: time.Now().Add(-time.Second)},
		}, ts)
		if err != nil {
			t.Fatal(err)
		}
		barrierBytes := c.CausalMetadataStats().VertexEstimatedBytes
		deadline := time.Now().Add(time.Hour)
		if _, err := c.DeleteVerticesHLCChecked([]string{"key"}, hlc.Timestamp{WallNs: 20}, deadline); err != nil {
			t.Fatalf("barrier replacement Delete: %v", err)
		}
		stats := c.CausalMetadataStats()
		if stats.VertexEntries != 1 || stats.OldestVertexRetentionDeadline != deadline ||
			stats.VertexEstimatedBytes <= barrierBytes {
			t.Fatalf("transition stats = %+v", stats)
		}
	})

	t.Run("absent Delete admission and rejection are atomic", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		c.SetCausalMetadataLimits(CausalMetadataLimits{MaxVertexEntries: 1})
		deadline := time.Now().Add(time.Hour)
		if deleted, err := c.DeleteVerticesHLCChecked(
			[]string{"first"}, hlc.Timestamp{WallNs: 10}, deadline,
		); err != nil || deleted != 0 {
			t.Fatalf("first absent Delete = (%d, %v), want 0, nil", deleted, err)
		}
		if _, err := c.DeleteVerticesHLCChecked(
			[]string{"rejected"}, hlc.Timestamp{WallNs: 20}, deadline,
		); err == nil {
			t.Fatal("second absent Delete unexpectedly crossed the causal limit")
		}
		if _, ok := c.vertexTombstones["rejected"]; ok {
			t.Fatal("rejected absent Delete retained a tombstone")
		}
		stats := c.CausalMetadataStats()
		if stats.VertexEntries != 1 || stats.VertexRejected != 1 {
			t.Fatalf("stats after rejected absent Delete = %+v", stats)
		}
		c.mu.Lock()
		c.sweepExpiredTombstonesLocked(deadline)
		c.mu.Unlock()
		if stats := c.CausalMetadataStats(); stats.VertexEntries != 0 || stats.VertexEstimatedBytes != 0 {
			t.Fatalf("expired tombstone stats = %+v, want no retained causal bytes", stats)
		}
		if _, err := c.DeleteVerticesHLCChecked(
			[]string{"reused"}, hlc.Timestamp{WallNs: 30}, deadline.Add(time.Hour),
		); err != nil {
			t.Fatalf("Delete after tombstone expiry did not reuse the slot: %v", err)
		}
	})

	t.Run("zero HLC Delete never retains or bypasses the budget", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		c.SetCausalMetadataLimits(CausalMetadataLimits{MaxVertexEntries: 1})
		deadline := time.Now().Add(time.Hour)
		for _, key := range []string{"zero-a", "zero-b"} {
			if deleted, err := c.DeleteVerticesHLCChecked(
				[]string{key}, hlc.Timestamp{}, deadline,
			); err != nil || deleted != 0 {
				t.Fatalf("zero-HLC Delete %q = (%d, %v), want 0, nil", key, deleted, err)
			}
		}
		if stats := c.CausalMetadataStats(); stats.VertexEntries != 0 || stats.VertexRejected != 0 {
			t.Fatalf("zero-HLC Delete stats = %+v, want no retained metadata", stats)
		}
	})
}

func TestCausalMetadataCapacityEdge(t *testing.T) {
	c := NewGraphCache[string, string](time.Hour)
	c.SetCausalMetadataLimits(CausalMetadataLimits{MaxEdgeEntries: 1})
	expired := time.Now().Add(-time.Second)
	live := time.Now().Add(time.Hour)

	first, err := c.PutEdgesWithExpirationHLCOutcomesChecked([]EdgeItem[string]{
		{Tail: "a", Head: "b", Weight: 1, Expiration: expired},
	}, hlc.Timestamp{WallNs: 10})
	if err != nil || len(first) != 1 || first[0] != PutOutcomeExpired {
		t.Fatalf("first edge Put = (%v, %v)", first, err)
	}
	_, err = c.PutEdgesWithExpirationHLCOutcomesChecked([]EdgeItem[string]{
		{Tail: "c", Head: "d", Weight: 1, Expiration: live},
		{Tail: "e", Head: "f", Weight: 1, Expiration: live},
	}, hlc.Timestamp{WallNs: 20})
	var capacityErr *CausalMetadataCapacityError
	if !errors.As(err, &capacityErr) || capacityErr.Kind != "edge" || capacityErr.Requested != 2 {
		t.Fatalf("batch error = %#v, want two-edge capacity rejection", err)
	}
	for _, key := range []EdgeKey[string]{{Tail: "c", Head: "d"}, {Tail: "e", Head: "f"}} {
		if _, _, ok := c.GetEdgeDetail(key.Tail, key.Head); ok {
			t.Fatalf("rejected batch stored edge %v", key)
		}
		if _, ok := c.edgeCausalUsage[key]; ok {
			t.Fatalf("rejected batch retained causal identity %v", key)
		}
	}
	stats := c.CausalMetadataStats()
	if stats.EdgeEntries != 1 || stats.EdgeRejected != 1 || stats.EdgeOverLimit {
		t.Fatalf("stats = %+v, want entries=1 rejected=1 within limit", stats)
	}

	zero := NewGraphCache[string, string](time.Hour)
	zero.SetCausalMetadataLimits(CausalMetadataLimits{MaxEdgeEntries: 1})
	for _, key := range []EdgeKey[string]{{Tail: "zero-a", Head: "h"}, {Tail: "zero-b", Head: "h"}} {
		if deleted, err := zero.DeleteEdgesHLCChecked(
			[]EdgeKey[string]{key}, hlc.Timestamp{}, time.Now().Add(time.Hour),
		); err != nil || deleted != 0 {
			t.Fatalf("zero-HLC edge Delete %v = (%d, %v), want 0, nil", key, deleted, err)
		}
	}
	if stats := zero.CausalMetadataStats(); stats.EdgeEntries != 0 || stats.EdgeRejected != 0 {
		t.Fatalf("zero-HLC edge Delete stats = %+v", stats)
	}
}

func TestCausalMetadataOldestDeadlineIsCached(t *testing.T) {
	c := NewGraphCache[string, string](time.Hour)
	base := time.Unix(1_000, 0).UTC()
	if _, err := c.DeleteVerticesHLCChecked(
		[]string{"late"}, hlc.Timestamp{WallNs: 1}, base.Add(3*time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DeleteVerticesHLCChecked(
		[]string{"early"}, hlc.Timestamp{WallNs: 2}, base.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DeleteEdgesHLCChecked(
		[]EdgeKey[string]{{Tail: "tail", Head: "head"}}, hlc.Timestamp{WallNs: 3}, base.Add(2*time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	stats := c.CausalMetadataStats()
	if stats.OldestVertexRetentionDeadline != base.Add(time.Hour) ||
		stats.OldestEdgeRetentionDeadline != base.Add(2*time.Hour) {
		t.Fatalf("oldest deadlines = (%v, %v)", stats.OldestVertexRetentionDeadline, stats.OldestEdgeRetentionDeadline)
	}

	// Replacing the current minimum and clearing an unrelated kind must both
	// update the cached O(1) status value while lazy heap entries remain safe.
	if _, err := c.DeleteVerticesHLCChecked(
		[]string{"early"}, hlc.Timestamp{WallNs: 4}, base.Add(4*time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.clearEdgeTombstoneLocked("tail", "head")
	c.mu.Unlock()
	stats = c.CausalMetadataStats()
	if stats.OldestVertexRetentionDeadline != base.Add(3*time.Hour) ||
		!stats.OldestEdgeRetentionDeadline.IsZero() {
		t.Fatalf("updated oldest deadlines = (%v, %v)", stats.OldestVertexRetentionDeadline, stats.OldestEdgeRetentionDeadline)
	}

	// Repeated renewal of one identity must compact stale heap entries rather
	// than making the deadline index itself an unbounded retention surface.
	for i := 0; i < 2*causalDeadlineCompactSlack; i++ {
		if _, err := c.DeleteVerticesHLCChecked(
			[]string{"late"}, hlc.Timestamp{WallNs: int64(10 + i)}, base.Add(time.Duration(5+i)*time.Hour),
		); err != nil {
			t.Fatal(err)
		}
	}
	if got, max := len(c.vertexTombstoneDeadlines), 2*len(c.vertexTombstones)+causalDeadlineCompactSlack; got > max {
		t.Fatalf("deadline heap len = %d, want <= %d", got, max)
	}
}

func TestSetCausalMetadataLimitsNormalizesNegativeToUnlimited(t *testing.T) {
	c := NewGraphCache[string, string](time.Hour)
	c.SetCausalMetadataLimits(CausalMetadataLimits{
		MaxVertexEntries: -1,
		MaxEdgeEntries:   -2,
	})
	stats := c.CausalMetadataStats()
	if stats.MaxVertexEntries != 0 || stats.MaxEdgeEntries != 0 {
		t.Fatalf("normalized limits = (%d, %d), want unlimited (0, 0)", stats.MaxVertexEntries, stats.MaxEdgeEntries)
	}
}

func TestCausalMetadataDeadlineHeapReleasesBackingStorage(t *testing.T) {
	c := NewGraphCache[string, string](time.Hour)
	base := time.Unix(1_000, 0).UTC()
	const count = 2 * causalDeadlineCompactSlack
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("key-%05d", i)
		if _, err := c.DeleteVerticesHLCChecked(
			[]string{key}, hlc.Timestamp{WallNs: int64(i + 1)}, base.Add(time.Duration(i+1)*time.Second),
		); err != nil {
			t.Fatal(err)
		}
	}
	c.mu.Lock()
	c.sweepExpiredTombstonesLocked(base.Add((count + 1) * time.Second))
	gotLen, gotCap := len(c.vertexTombstoneDeadlines), cap(c.vertexTombstoneDeadlines)
	gotBytes := c.vertexTombstoneDeadlineBytes
	c.mu.Unlock()
	if gotLen != 0 || gotCap != 0 || gotBytes != 0 {
		t.Fatalf("drained deadline heap = len %d cap %d bytes %d, want 0/0/0", gotLen, gotCap, gotBytes)
	}
}

func BenchmarkCausalMetadataCapacity(b *testing.B) {
	expired := time.Unix(1, 0)
	live := time.Now().Add(time.Hour)

	b.Run("unlimited existing vertex floor", func(b *testing.B) {
		c := NewGraphCache[string, string](time.Hour)
		items := []VertexItem[string, string]{{Key: "key", Value: "value", Expiration: live}}
		if _, err := c.PutVerticesWithExpirationHLCOutcomesChecked(items, hlc.Timestamp{WallNs: 1}); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		for b.Loop() {
			if _, err := c.PutVerticesWithExpirationHLCOutcomesChecked(items, hlc.Timestamp{WallNs: 2}); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("existing vertex floor", func(b *testing.B) {
		c := NewGraphCache[string, string](time.Hour)
		c.SetCausalMetadataLimits(CausalMetadataLimits{MaxVertexEntries: 1})
		items := []VertexItem[string, string]{{Key: "key", Value: "value", Expiration: live}}
		if _, err := c.PutVerticesWithExpirationHLCOutcomesChecked(items, hlc.Timestamp{WallNs: 1}); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		for b.Loop() {
			if _, err := c.PutVerticesWithExpirationHLCOutcomesChecked(items, hlc.Timestamp{WallNs: 2}); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("1024 born-expired identities", func(b *testing.B) {
		items := make([]VertexItem[string, string], 1024)
		for i := range items {
			items[i] = VertexItem[string, string]{
				Key:        fmt.Sprintf("causal-%04d", i),
				Value:      "value",
				Expiration: expired,
			}
		}
		b.ReportAllocs()
		b.SetBytes(int64(len(items)))
		for b.Loop() {
			c := NewGraphCache[string, string](time.Hour)
			c.SetCausalMetadataLimits(CausalMetadataLimits{MaxVertexEntries: len(items)})
			if _, err := c.PutVerticesWithExpirationHLCOutcomesChecked(items, hlc.Timestamp{WallNs: 1}); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkCausalMetadataStats(b *testing.B) {
	c := NewGraphCache[string, string](time.Hour)
	keys := make([]string, 10_000)
	for i := range keys {
		keys[i] = fmt.Sprintf("tombstone-%05d", i)
	}
	if _, err := c.DeleteVerticesHLCChecked(
		keys, hlc.Timestamp{WallNs: 1}, time.Now().Add(time.Hour),
	); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = c.CausalMetadataStats()
	}
}
