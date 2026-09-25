package graphcache

import (
	"context"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

func TestVertexPutTransactionConditionalResultAndCommit(t *testing.T) {
	c := newVertexTransactionTestCache()
	applicationTime := time.Now()
	live := applicationTime.Add(time.Hour)
	expired := applicationTime.Add(-time.Hour)
	applicationCalls := 0
	c.applicationClock = func() time.Time {
		applicationCalls++
		return applicationTime
	}
	if err := c.PutVertexWithExpiration("existing", "original", live); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DeleteVerticesHLCChecked(
		[]string{"superseded"}, hlc.Timestamp{WallNs: 30}, live,
	); err != nil {
		t.Fatal(err)
	}
	applicationCalls = 0

	items := []VertexItem[string, string]{
		{Key: "existing", Value: "condition miss", Expiration: live},
		{Key: "duplicate", Value: "born expired", Expiration: expired},
		{Key: "duplicate", Value: "accepted searchable", Expiration: live},
		{Key: "duplicate", Value: "later duplicate", Expiration: live},
		{Key: "superseded", Value: "causally old", Expiration: live},
		{Key: "expired-only", Value: "delete-like", Expiration: expired},
	}
	tx, err := c.BeginVertexPut(items, hlc.Timestamp{WallNs: 20}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Abort()
	if applicationCalls != 1 {
		t.Fatalf("application clock sampled %d times, want 1", applicationCalls)
	}

	want := VertexPutStageResult[string, string]{
		Outcomes: []PutOutcome{
			PutOutcomeConditionNotMet,
			PutOutcomeExpired,
			PutOutcomeAppliedAndLive,
			PutOutcomeConditionNotMet,
			PutOutcomeSuperseded,
			PutOutcomeExpired,
		},
		Accepted: []IndexedVertexPut[string, string]{
			{Index: 1, Item: items[1], Outcome: PutOutcomeExpired},
			{Index: 2, Item: items[2], Outcome: PutOutcomeAppliedAndLive},
			{Index: 5, Item: items[5], Outcome: PutOutcomeExpired},
		},
	}
	if got := tx.Result(); !reflect.DeepEqual(got, want) {
		t.Fatalf("staged result = %+v, want %+v", got, want)
	}
	tampered := tx.Result()
	tampered.Outcomes[0] = PutOutcomeExpired
	tampered.Accepted[0].Index = 99
	tampered.Accepted[0].Item.Key = "changed"
	if got := tx.Result(); !reflect.DeepEqual(got, want) {
		t.Fatalf("result alias changed transaction = %+v, want %+v", got, want)
	}

	tx.Commit()
	tx.Abort()
	if value, ok := c.GetVertex("existing"); !ok || value != "original" {
		t.Fatalf("condition miss changed existing vertex = %q/%v", value, ok)
	}
	if value, ok := c.GetVertex("duplicate"); !ok || value != "accepted searchable" {
		t.Fatalf("committed duplicate vertex = %q/%v", value, ok)
	}
	if _, ok := c.GetVertex("superseded"); ok {
		t.Fatal("causally superseded Put became visible")
	}
	if _, ok := c.GetVertex("expired-only"); ok {
		t.Fatal("born-expired Put became visible")
	}
	c.mu.RLock()
	barrier, hasBarrier := c.vertexCausalBarriers["expired-only"]
	c.mu.RUnlock()
	if !hasBarrier || barrier != (hlc.Timestamp{WallNs: 20}) {
		t.Fatalf("born-expired causal barrier = %v/%v", barrier, hasBarrier)
	}
	if got := c.SearchVertices("searchable", 10, ""); len(got) != 1 || got[0].ID != "duplicate" {
		t.Fatalf("search after commit = %+v, want duplicate", got)
	}
}

func TestVertexPutTransactionUnconditionalResultAndCommit(t *testing.T) {
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	applicationTime := time.Now()
	live := applicationTime.Add(time.Hour)
	expired := applicationTime.Add(-time.Hour)
	c.applicationClock = func() time.Time { return applicationTime }
	if _, err := c.PutVerticesWithExpirationHLCOutcomesChecked(
		[]VertexItem[string, string]{{Key: "protected", Value: "original", Expiration: live}},
		hlc.Timestamp{WallNs: 30},
	); err != nil {
		t.Fatal(err)
	}
	items := []VertexItem[string, string]{
		{Key: "duplicate", Value: "first", Expiration: live},
		{Key: "duplicate", Value: "second", Expiration: live},
		{Key: "expired", Value: "delete-like", Expiration: expired},
		{Key: "protected", Value: "causally old", Expiration: live},
	}
	tx, err := c.BeginVertexPut(items, hlc.Timestamp{WallNs: 20}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Abort()

	want := VertexPutStageResult[string, string]{
		Outcomes: []PutOutcome{
			PutOutcomeAppliedAndLive,
			PutOutcomeAppliedAndLive,
			PutOutcomeExpired,
			PutOutcomeSuperseded,
		},
		Accepted: []IndexedVertexPut[string, string]{
			{Index: 0, Item: items[0], Outcome: PutOutcomeAppliedAndLive},
			{Index: 1, Item: items[1], Outcome: PutOutcomeAppliedAndLive},
			{Index: 2, Item: items[2], Outcome: PutOutcomeExpired},
		},
	}
	if got := tx.Result(); !reflect.DeepEqual(got, want) {
		t.Fatalf("unconditional staged result = %+v, want %+v", got, want)
	}
	tx.Commit()
	if value, ok := c.GetVertex("duplicate"); !ok || value != "second" {
		t.Fatalf("unconditional duplicate vertex = %q/%v", value, ok)
	}
	if _, ok := c.GetVertex("expired"); ok {
		t.Fatal("unconditional born-expired Put became visible")
	}
	if value, ok := c.GetVertex("protected"); !ok || value != "original" {
		t.Fatalf("unconditional superseded Put changed protected vertex = %q/%v", value, ok)
	}
}

func TestVertexDeleteTransactionResultAndCommit(t *testing.T) {
	c := newVertexTransactionTestCache()
	expiration := time.Now().Add(time.Hour)
	if err := c.PutVertexWithExpiration("present", "remove searchable", expiration); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutVerticesWithExpirationHLCOutcomesChecked(
		[]VertexItem[string, string]{{Key: "protected", Value: "keep searchable", Expiration: expiration}},
		hlc.Timestamp{WallNs: 30},
	); err != nil {
		t.Fatal(err)
	}
	keys := []string{"present", "absent", "present", "protected"}
	tx, err := c.BeginVertexDelete(keys, hlc.Timestamp{WallNs: 20}, expiration)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Abort()

	want := VertexDeleteStageResult[string]{
		Existed: []bool{true, false, false, false},
		Accepted: []IndexedVertexDelete[string]{
			{Index: 0, Key: "present"},
			{Index: 1, Key: "absent"},
			{Index: 2, Key: "present"},
		},
	}
	if got := tx.Result(); !reflect.DeepEqual(got, want) {
		t.Fatalf("staged result = %+v, want %+v", got, want)
	}
	tampered := tx.Result()
	tampered.Existed[0] = false
	tampered.Accepted[0].Index = 99
	if got := tx.Result(); !reflect.DeepEqual(got, want) {
		t.Fatalf("result alias changed transaction = %+v, want %+v", got, want)
	}

	tx.Commit()
	if _, ok := c.GetVertex("present"); ok {
		t.Fatal("committed Delete left present vertex visible")
	}
	if value, ok := c.GetVertex("protected"); !ok || value != "keep searchable" {
		t.Fatalf("superseded Delete changed protected vertex = %q/%v", value, ok)
	}
	c.mu.RLock()
	_, presentTombstone := c.vertexTombstones["present"]
	_, absentTombstone := c.vertexTombstones["absent"]
	c.mu.RUnlock()
	if !presentTombstone || !absentTombstone {
		t.Fatalf("accepted Delete tombstones = present:%v absent:%v", presentTombstone, absentTombstone)
	}
	if got := c.SearchVertices("remove", 10, ""); len(got) != 0 {
		t.Fatalf("deleted vertex remained searchable: %+v", got)
	}
}

func TestVertexDeleteTransactionEqualHLCExpirationMatchesCanonicalCommit(t *testing.T) {
	ts := hlc.Timestamp{WallNs: 20}
	now := time.Now()
	tests := []struct {
		name            string
		initial, replay time.Time
	}{
		{name: "later replay retains earlier deadline", initial: now.Add(time.Hour), replay: now.Add(2 * time.Hour)},
		{name: "zero replay retains no deadline", initial: now.Add(time.Hour), replay: time.Time{}},
		{name: "finite replay replaces zero deadline", initial: time.Time{}, replay: now.Add(time.Hour)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			canonical := NewGraphCache[string, string](time.Hour)
			if _, err := canonical.DeleteVerticesHLCChecked([]string{"key"}, ts, tt.initial); err != nil {
				t.Fatal(err)
			}
			if _, err := canonical.DeleteVerticesHLCChecked([]string{"key"}, ts, tt.replay); err != nil {
				t.Fatal(err)
			}
			want := captureVertexTransactionState(canonical)

			staged := NewGraphCacheWithStaging[string, string](time.Hour)
			if _, err := staged.DeleteVerticesHLCChecked([]string{"key"}, ts, tt.initial); err != nil {
				t.Fatal(err)
			}
			tx, err := staged.BeginVertexDelete([]string{"key"}, ts, tt.replay)
			if err != nil {
				t.Fatal(err)
			}
			tx.Commit()

			if got := captureVertexTransactionState(staged); !reflect.DeepEqual(got, want) {
				t.Fatalf("staged equal-HLC commit differs from canonical setter: got=%+v want=%+v", got, want)
			}
		})
	}
}

func TestReplicatedVertexTransactionsMayExceedLocalCausalLimit(t *testing.T) {
	expiration := time.Now().Add(time.Hour)

	t.Run("Put", func(t *testing.T) {
		c := NewGraphCacheWithStaging[string, string](time.Hour)
		c.SetCausalMetadataLimits(CausalMetadataLimits{MaxVertexEntries: 1})
		if _, err := c.PutVerticesWithExpirationHLCOutcomesChecked(
			[]VertexItem[string, string]{{Key: "first", Value: "first", Expiration: expiration}},
			hlc.Timestamp{WallNs: 10},
		); err != nil {
			t.Fatal(err)
		}
		item := []VertexItem[string, string]{{Key: "second", Value: "second", Expiration: expiration}}
		if tx, err := c.BeginVertexPut(item, hlc.Timestamp{WallNs: 20}, false); tx != nil || err == nil {
			if tx != nil {
				tx.Abort()
			}
			t.Fatalf("strict staged Put beyond causal limit = (%v, %v)", tx, err)
		}
		tx, err := c.BeginReplicatedVertexPut(item, hlc.Timestamp{WallNs: 20})
		if err != nil {
			t.Fatal(err)
		}
		if got := tx.Result().Accepted; len(got) != 1 || got[0].Index != 0 {
			tx.Abort()
			t.Fatalf("replicated Put accepted set = %+v", got)
		}
		tx.Commit()
		if stats := c.CausalMetadataStats(); stats.VertexEntries != 2 || !stats.VertexOverLimit {
			t.Fatalf("replicated Put did not converge above limit: %+v", stats)
		}
	})

	t.Run("accepted-expired barrier", func(t *testing.T) {
		c := NewGraphCacheWithStaging[string, string](time.Hour)
		if _, err := c.PutVerticesWithExpirationHLCOutcomesChecked(
			[]VertexItem[string, string]{{Key: "key", Value: "live", Expiration: expiration}},
			hlc.Timestamp{WallNs: 10},
		); err != nil {
			t.Fatal(err)
		}
		tx, err := c.BeginReplicatedVertexPut(
			[]VertexItem[string, string]{{Key: "key", CausalBarrier: true}},
			hlc.Timestamp{WallNs: 20},
		)
		if err != nil {
			t.Fatal(err)
		}
		if got := tx.Result(); !slices.Equal(got.Outcomes, []PutOutcome{PutOutcomeExpired}) ||
			len(got.Accepted) != 1 || got.Accepted[0].Index != 0 {
			tx.Abort()
			t.Fatalf("replicated barrier result = %+v", got)
		}
		tx.Commit()
		if _, ok := c.GetVertex("key"); ok {
			t.Fatal("replicated barrier left the prior value live")
		}
		if vertices, _ := c.CausalBarrierCounts(); vertices != 1 {
			t.Fatalf("vertex barrier count = %d, want 1", vertices)
		}
	})

	t.Run("origin-accepted Put ignores receiver presence", func(t *testing.T) {
		c := NewGraphCacheWithStaging[string, string](time.Hour)
		if _, err := c.PutVerticesWithExpirationHLCOutcomesChecked(
			[]VertexItem[string, string]{{Key: "key", Value: "receiver", Expiration: expiration}},
			hlc.Timestamp{WallNs: 10},
		); err != nil {
			t.Fatal(err)
		}
		tx, err := c.BeginReplicatedVertexPut(
			[]VertexItem[string, string]{{Key: "key", Value: "origin", Expiration: expiration}},
			hlc.Timestamp{WallNs: 20},
		)
		if err != nil {
			t.Fatal(err)
		}
		if got := tx.Result(); !slices.Equal(got.Outcomes, []PutOutcome{PutOutcomeAppliedAndLive}) ||
			len(got.Accepted) != 1 || got.Accepted[0].Index != 0 {
			tx.Abort()
			t.Fatalf("replicated origin projection = %+v", got)
		}
		tx.Commit()
		if value, ok := c.GetVertex("key"); !ok || value != "origin" {
			t.Fatalf("replicated origin value = %q/%v", value, ok)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		c := NewGraphCacheWithStaging[string, string](time.Hour)
		c.SetCausalMetadataLimits(CausalMetadataLimits{MaxVertexEntries: 1})
		if _, err := c.DeleteVerticesHLCChecked(
			[]string{"first"}, hlc.Timestamp{WallNs: 10}, expiration,
		); err != nil {
			t.Fatal(err)
		}
		if tx, err := c.BeginVertexDelete(
			[]string{"second"}, hlc.Timestamp{WallNs: 20}, expiration,
		); tx != nil || err == nil {
			if tx != nil {
				tx.Abort()
			}
			t.Fatalf("strict staged Delete beyond causal limit = (%v, %v)", tx, err)
		}
		tx, err := c.BeginReplicatedVertexDelete(
			[]string{"second"}, hlc.Timestamp{WallNs: 20}, expiration,
		)
		if err != nil {
			t.Fatal(err)
		}
		got := tx.Result()
		if !slices.Equal(got.Existed, []bool{false}) ||
			!reflect.DeepEqual(got.Accepted, []IndexedVertexDelete[string]{{Index: 0, Key: "second"}}) {
			tx.Abort()
			t.Fatalf("replicated Delete result = %+v", got)
		}
		tx.Commit()
		if stats := c.CausalMetadataStats(); stats.VertexEntries != 2 || !stats.VertexOverLimit {
			t.Fatalf("replicated Delete did not converge above limit: %+v", stats)
		}
	})
}

func TestVertexPutTransactionBlocksObservers(t *testing.T) {
	c := newVertexTransactionTestCache()
	expiration := time.Now().Add(time.Hour)
	c.AddEdgeWithExpiration("tail", "head", 1, expiration)
	tx, err := c.BeginVertexPut(
		[]VertexItem[string, string]{{Key: "new", Value: "visible searchable", Expiration: expiration}},
		hlc.Timestamp{WallNs: 20}, false,
	)
	if err != nil {
		t.Fatal(err)
	}

	checks := []func(){
		func() { c.GetVertex("new") },
		func() { c.SnapshotReplication() },
		func() { c.SearchVertices("searchable", 1, "") },
		func() {
			c.ScanByPrefix(context.Background(), "", func(_, _, _ string) bool { return true })
		},
		func() { c.AddEdgeWithExpiration("tail", "head", 2, expiration) },
	}
	started := make(chan struct{}, len(checks))
	observed := make(chan struct{}, len(checks))
	var wg sync.WaitGroup
	for _, check := range checks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			started <- struct{}{}
			check()
			observed <- struct{}{}
		}()
	}
	for range checks {
		<-started
	}
	select {
	case <-observed:
		tx.Abort()
		t.Fatal("observer escaped the staged transaction")
	case <-time.After(30 * time.Millisecond):
	}
	tx.Commit()
	wg.Wait()
	if value, ok := c.GetVertex("new"); !ok || value != "visible searchable" {
		t.Fatalf("published vertex = %q/%v", value, ok)
	}
	if weight, ok := c.GetWeight("tail", "head"); !ok || weight != 3 {
		t.Fatalf("published existing-edge Add = %v/%v, want 3/true", weight, ok)
	}
}

func TestVertexDeleteTransactionBlocksObservers(t *testing.T) {
	c := newVertexTransactionTestCache()
	expiration := time.Now().Add(time.Hour)
	if err := c.PutVertexWithExpiration("old", "visible searchable", expiration); err != nil {
		t.Fatal(err)
	}
	tx, err := c.BeginVertexDelete(
		[]string{"old"}, hlc.Timestamp{WallNs: 20}, expiration,
	)
	if err != nil {
		t.Fatal(err)
	}

	checks := []func(){
		func() { c.GetVertex("old") },
		func() { c.SnapshotReplication() },
		func() { c.SearchVertices("searchable", 1, "") },
		func() {
			c.ScanByPrefix(context.Background(), "", func(_, _, _ string) bool { return true })
		},
	}
	started := make(chan struct{}, len(checks))
	observed := make(chan struct{}, len(checks))
	var wg sync.WaitGroup
	for _, check := range checks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			started <- struct{}{}
			check()
			observed <- struct{}{}
		}()
	}
	for range checks {
		<-started
	}
	select {
	case <-observed:
		tx.Abort()
		t.Fatal("observer escaped the staged transaction")
	case <-time.After(30 * time.Millisecond):
	}
	tx.Commit()
	wg.Wait()
	if _, ok := c.GetVertex("old"); ok {
		t.Fatal("published Delete left the vertex visible")
	}
}

func TestVertexTransactionsCloseAndInputContracts(t *testing.T) {
	plain := NewGraphCache[string, string](time.Hour)
	if tx, err := plain.BeginVertexPut(nil, hlc.Timestamp{}, false); tx != nil || err == nil {
		t.Fatalf("ordinary cache BeginVertexPut = (%v, %v), want staging-gate error", tx, err)
	}
	if tx, err := plain.BeginVertexDelete(nil, hlc.Timestamp{}, time.Time{}); tx != nil || err == nil {
		t.Fatalf("ordinary cache BeginVertexDelete = (%v, %v), want staging-gate error", tx, err)
	}

	c := NewGraphCacheWithStaging[string, *int](time.Hour)
	if tx, err := c.BeginVertexPut(
		[]VertexItem[string, *int]{{Key: "barrier", CausalBarrier: true}},
		hlc.Timestamp{WallNs: 10}, false,
	); tx != nil || err == nil {
		if tx != nil {
			tx.Abort()
		}
		t.Fatalf("local causal barrier = (%v, %v), want input error", tx, err)
	}
	put, err := c.BeginVertexPut(
		[]VertexItem[string, *int]{{Key: "nil", Value: nil, Expiration: time.Now().Add(time.Hour)}},
		hlc.Timestamp{WallNs: 10}, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	put.Commit()
	if value, ok := c.GetVertex("nil"); !ok || value != nil {
		t.Fatalf("nil value = %v/%v, want nil/true", value, ok)
	}
	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("second Put Commit did not panic")
		}
	}()
	put.Commit()
}

func TestVertexDeleteTransactionCommitAfterAbortPanics(t *testing.T) {
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	tx, err := c.BeginVertexDelete(nil, hlc.Timestamp{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got := tx.Result(); got.Existed == nil || got.Accepted == nil {
		tx.Abort()
		t.Fatalf("empty Delete result = %+v, want detached empty slices", got)
	}
	tx.Abort()
	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("Delete Commit after Abort did not panic")
		}
	}()
	tx.Commit()
}
