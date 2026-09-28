package graphcache

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

func TestDeleteEdgeContributionsPreservesOtherRowsAndFencesDelayedAdd(t *testing.T) {
	expiration := time.Now().Add(time.Hour)
	cache := NewGraphCache[string, string](time.Hour)
	addA, addB := ContribID{1}, ContribID{2}
	ts := func(n int64) hlc.Timestamp { return hlc.Timestamp{WallNs: n} }
	if !cache.PutEdgeWithExpirationHLC("tail", "head", 10, expiration, ts(1)) ||
		!cache.AddEdgeWithExpirationContribHLC("tail", "head", 4, expiration, addA, ts(2)) ||
		!cache.AddEdgeWithExpirationContribHLC("tail", "head", 3, expiration, addB, ts(3)) {
		t.Fatal("failed to seed Put and distinct Adds")
	}
	a := EdgeContributionKey[string]{Tail: "tail", Head: "head", ContribID: addA}
	b := EdgeContributionKey[string]{Tail: "tail", Head: "head", ContribID: addB}
	missing := EdgeContributionKey[string]{Tail: "tail", Head: "head", ContribID: ContribID{3}}
	existed, accepted, err := cache.DeleteEdgeContributionsHLCDecisionsChecked(
		[]EdgeContributionKey[string]{a, missing, a, b}, ts(4), expiration)
	if err != nil || !slices.Equal(existed, []bool{true, false, false, true}) ||
		!slices.Equal(accepted, []int{0, 1, 2, 3}) {
		t.Fatalf("Delete outcomes = %v, accepted=%v, err=%v", existed, accepted, err)
	}
	if weight, ok := cache.GetWeight("tail", "head"); !ok || weight != 10 {
		t.Fatalf("Delete removed Put base: weight=%v, present=%v", weight, ok)
	}
	if cache.AddEdgeWithExpirationContribHLC("tail", "head", 4, expiration, addA, ts(2)) ||
		cache.AddEdgeWithExpirationContribHLC("tail", "head", 4, expiration, addA, ts(5)) ||
		cache.AddEdgeWithExpirationContribHLC("tail", "head", 4, expiration, missing.ContribID, ts(3)) {
		t.Fatal("deleted or never-observed Add identity was resurrected")
	}
	for _, key := range []EdgeContributionKey[string]{a, b, missing} {
		if _, ok := cache.edgeContributionTombstones[key]; !ok {
			t.Fatalf("missing D4 floor for %v", key)
		}
	}
	snapshot := cache.SnapshotReplication()
	if len(snapshot.Tombstones.EdgeContributions) != 3 {
		t.Fatalf("snapshot lost contribution floors: %+v", snapshot.Tombstones)
	}
	replica := NewGraphCache[string, string](time.Hour)
	replayReplicationSnapshot(replica, snapshot)
	replayReplicationSnapshot(replica, snapshot)
	if replica.AddEdgeWithExpirationContribHLC("tail", "head", 4, expiration, addA, ts(2)) {
		t.Fatal("Snapshot replay lost deleted-ID evidence")
	}
	if weight, ok := replica.GetWeight("tail", "head"); !ok || weight != 10 {
		t.Fatalf("replica weight=%v, present=%v", weight, ok)
	}
}

func TestDeleteEdgeContributionAbsentStillConsumesCapacityAndExpiryReclaimsIt(t *testing.T) {
	cache := NewGraphCache[string, string](time.Hour)
	cache.SetCausalMetadataLimits(CausalMetadataLimits{MaxEdgeEntries: 1})
	deadline := time.Now().Add(time.Hour)
	a := EdgeContributionKey[string]{Tail: "tail", Head: "head", ContribID: ContribID{1}}
	b := EdgeContributionKey[string]{Tail: "tail", Head: "head", ContribID: ContribID{2}}
	ts := hlc.Timestamp{WallNs: 10}
	existed, accepted, err := cache.DeleteEdgeContributionsHLCDecisionsChecked([]EdgeContributionKey[string]{a, a}, ts, deadline)
	if err != nil || !slices.Equal(existed, []bool{false, false}) || !slices.Equal(accepted, []int{0, 1}) {
		t.Fatalf("accepted absent duplicate = %v, %v, %v", existed, accepted, err)
	}
	before := cache.CausalMetadataStats()
	if before.EdgeEntries != 1 || before.EdgeEstimatedBytes == 0 ||
		!before.OldestEdgeRetentionDeadline.Equal(deadline) {
		t.Fatalf("unbounded or unaccounted tombstone: %+v", before)
	}
	existed, accepted, err = cache.DeleteEdgeContributionsHLCDecisionsChecked([]EdgeContributionKey[string]{a, b}, ts, deadline)
	var capacity *CausalMetadataCapacityError
	if existed != nil || accepted != nil || !errors.As(err, &capacity) ||
		capacity.Current != 1 || capacity.Requested != 1 {
		t.Fatalf("partial capacity rejection = %v, %v, %v", existed, accepted, err)
	}
	if _, ok := cache.edgeContributionTombstones[b]; ok {
		t.Fatal("rejected absent identity acquired a floor")
	}
	// An already-committed remote Delete must still converge across a
	// differently configured replica, including its over-limit metrics.
	if _, _, err := cache.DeleteEdgeContributionsHLCDecisions([]EdgeContributionKey[string]{b}, ts, deadline); err != nil {
		t.Fatal(err)
	}
	if stats := cache.CausalMetadataStats(); stats.EdgeEntries != 2 || !stats.EdgeOverLimit {
		t.Fatalf("remote causal budget=%+v", stats)
	}
	cache.mu.Lock()
	cache.sweepExpiredTombstonesLocked(deadline)
	cache.mu.Unlock()
	if stats := cache.CausalMetadataStats(); stats.EdgeEntries != 0 || stats.EdgeEstimatedBytes != 0 {
		t.Fatalf("GC failed to reclaim per-ID floors: %+v", stats)
	}
	if _, _, err := cache.DeleteEdgeContributionsHLCDecisionsChecked([]EdgeContributionKey[string]{b}, hlc.Timestamp{WallNs: 20}, deadline.Add(time.Hour)); err != nil {
		t.Fatalf("reclaimed capacity was not reusable: %v", err)
	}
}

func TestDeleteEdgeContributionStagesAndRestoresExactState(t *testing.T) {
	cache := NewGraphCacheWithStaging[string, string](time.Hour)
	cache.EnablePrefixIndex(func(s string) string { return s })
	deadline := time.Now().Add(time.Hour)
	a := EdgeContributionKey[string]{Tail: "tail", Head: "head", ContribID: ContribID{1}}
	b := EdgeContributionKey[string]{Tail: "tail", Head: "head", ContribID: ContribID{2}}
	if !cache.AddEdgeWithExpirationContribHLC("tail", "head", 5, deadline, a.ContribID, hlc.Timestamp{WallNs: 1}) ||
		!cache.AddEdgeWithExpirationContribHLC("tail", "head", 7, deadline, b.ContribID, hlc.Timestamp{WallNs: 2}) {
		t.Fatal("failed to seed source rows")
	}
	before := cache.SnapshotReplication()
	statsBefore := cache.CausalMetadataStats()
	tx, err := cache.PrepareEdgeContributionDelete(
		[]EdgeContributionKey[string]{a, a, b}, hlc.Timestamp{WallNs: 3}, deadline)
	if err != nil {
		t.Fatal(err)
	}
	want := EdgeContributionDeleteStageResult[string]{
		Existed: []bool{true, false, true},
		Accepted: []IndexedEdgeContributionDelete[string]{
			{Index: 0, Key: a}, {Index: 1, Key: a}, {Index: 2, Key: b},
		},
	}
	if got := tx.Result(); !reflect.DeepEqual(got, want) {
		t.Fatalf("prepared result=%+v want=%+v", got, want)
	}
	if len(cache.edgeContributionTombstones) != 0 || cache.edges.edgeCount != 1 {
		t.Fatal("Prepare changed graph before WAL admission")
	}
	tx.Apply()
	tx.Abort()
	tx.Abort()
	if got := cache.SnapshotReplication(); !reflect.DeepEqual(got, before) {
		t.Fatalf("aborted Delete changed replica snapshot: got=%+v want=%+v", got, before)
	}
	if stats := cache.CausalMetadataStats(); !reflect.DeepEqual(stats, statsBefore) {
		t.Fatalf("aborted Delete changed usage: %+v != %+v", stats, statsBefore)
	}
	tx, err = cache.BeginEdgeContributionDelete([]EdgeContributionKey[string]{a}, hlc.Timestamp{WallNs: 4}, deadline)
	if err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	if weight, ok := cache.GetWeight("tail", "head"); !ok || weight != 7 {
		t.Fatalf("committed partial Delete=%v, present=%v", weight, ok)
	}
	if _, ok := cache.edgeContributionTombstones[a]; !ok {
		t.Fatal("committed Delete lost tombstone")
	}
}

func TestDeleteEdgeContributionRejectsZeroIdentityAndDeadline(t *testing.T) {
	cache := NewGraphCache[string, string](time.Hour)
	key := EdgeContributionKey[string]{Tail: "a", Head: "b"}
	if _, _, err := cache.DeleteEdgeContributionsHLCDecisionsChecked(
		[]EdgeContributionKey[string]{key}, hlc.Timestamp{WallNs: 1}, time.Now().Add(time.Hour),
	); err == nil {
		t.Fatal("zero ContribID was accepted")
	}
	key.ContribID[0] = 1
	if _, _, err := cache.DeleteEdgeContributionsHLCDecisionsChecked(
		[]EdgeContributionKey[string]{key}, hlc.Timestamp{WallNs: 1}, time.Time{},
	); err == nil {
		t.Fatal("nonzero causal Delete without D4 deadline was accepted")
	}
}
