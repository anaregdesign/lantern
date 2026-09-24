package graphcache

import (
	"cmp"
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

type stagedDeleteState struct {
	snapshot ReplicationSnapshot[string, string]
	stats    CausalMetadataStats
	count    int
	refs     map[string]struct {
		id    vertexID
		count uint32
	}
	free         []vertexID
	indexed      []string
	deadlines    []causalDeadlineEntry[EdgeKey[string]]
	positions    map[EdgeKey[string]]int
	deadlinePeak int
}

func captureStagedDeleteState(c *GraphCache[string, string]) stagedDeleteState {
	snapshot := ReplicationSnapshot[string, string]{
		Barriers: c.SnapshotCausalBarriers(),
		Graph:    c.SnapshotGraph(),
	}
	c.mu.RLock()
	snapshot.Tombstones = c.snapshotTombstonesRLocked(time.Now())
	deadlineEntries := append([]causalDeadlineEntry[EdgeKey[string]](nil), c.edgeTombstoneDeadlines.entries...)
	deadlinePositions := make(map[EdgeKey[string]]int, len(c.edgeTombstoneDeadlines.positions))
	for key, position := range c.edgeTombstoneDeadlines.positions {
		deadlinePositions[key] = position
	}
	deadlinePeak := c.edgeTombstoneDeadlines.peak
	c.mu.RUnlock()
	slices.SortFunc(snapshot.Graph.Vertices, func(a, b SnapshotVertex[string, string]) int { return cmp.Compare(a.Key, b.Key) })
	slices.SortFunc(snapshot.Graph.Edges, func(a, b SnapshotEdge[string]) int {
		if n := cmp.Compare(a.Tail, b.Tail); n != 0 {
			return n
		}
		return cmp.Compare(a.Head, b.Head)
	})
	slices.SortFunc(snapshot.Tombstones.Edges, func(a, b SnapshotEdgeTombstone[string]) int {
		if n := cmp.Compare(a.Tail, b.Tail); n != 0 {
			return n
		}
		return cmp.Compare(a.Head, b.Head)
	})
	slices.SortFunc(snapshot.Barriers.Edges, func(a, b SnapshotEdgeCausalBarrier[string]) int {
		if n := cmp.Compare(a.Tail, b.Tail); n != 0 {
			return n
		}
		return cmp.Compare(a.Head, b.Head)
	})
	state := stagedDeleteState{
		snapshot:     snapshot,
		stats:        c.CausalMetadataStats(),
		count:        c.EdgeCount(),
		deadlines:    deadlineEntries,
		positions:    deadlinePositions,
		deadlinePeak: deadlinePeak,
		refs: make(map[string]struct {
			id    vertexID
			count uint32
		}),
	}
	c.dict.mu.RLock()
	for key, id := range c.dict.forward {
		state.refs[key] = struct {
			id    vertexID
			count uint32
		}{id, atomic.LoadUint32(&c.dict.refcount[id])}
	}
	state.free = append([]vertexID(nil), c.dict.free...)
	c.dict.mu.RUnlock()
	c.ScanEdgesByPrefix(context.Background(), "", "", func(_, tail, _, head string, _ float32, _ time.Time) bool {
		state.indexed = append(state.indexed, tail+"/"+head)
		return true
	})
	slices.Sort(state.indexed)
	return state
}

func stageAndRollbackForTest(t *testing.T, c *GraphCache[string, string], keys []EdgeKey[string], ts hlc.Timestamp, expiration time.Time, inspect func(*stagedEdgeDelete[string, string])) []bool {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	projected := make(map[EdgeKey[string]]string, len(keys))
	if c.headByTail != nil {
		for _, key := range keys {
			projected[key] = c.prefixExtract(key.Head)
		}
	}
	c.publicationGate.Lock()
	defer c.publicationGate.Unlock()
	stage, err := c.prepareStagedEdgeDeleteLocked(keys, ts, expiration, c.applicationTime(), projected)
	if err != nil {
		t.Fatal(err)
	}
	stage.applyLocked()
	defer stage.rollbackLocked()
	if inspect != nil {
		inspect(stage)
	}
	return append([]bool(nil), stage.outcomes...)
}

func TestStagedEdgeDeleteSparseRollback(t *testing.T) {
	c := newGraphCacheWithStaging[string, string](time.Hour)
	c.EnablePrefixIndex(func(key string) string {
		if strings.HasPrefix(key, "head-") {
			return "head"
		}
		return key
	})
	expiration := time.Now().Add(time.Hour)
	old := hlc.Timestamp{WallNs: 10}
	deleteAt := hlc.Timestamp{WallNs: 20}
	newer := hlc.Timestamp{WallNs: 30}
	if !c.PutEdgeWithExpirationHLC("tail", "head-1", 1, expiration, old) ||
		!c.PutEdgeWithExpirationHLC("tail", "head-2", 2, expiration, old) ||
		!c.AddEdgeWithExpirationContribHLC("tail", "newer", 3, expiration, ContribID{0: 1}, newer) {
		t.Fatal("seed failed")
	}
	// A physically dangling self-loop owns the last two references to one ID.
	c.AddEdgeWithExpiration("self", "self", 4, expiration)
	c.DeleteVertex("self")
	selfID, ok := c.dict.lookup("self")
	if !ok {
		t.Fatal("self-loop lost its endpoint ID before staging")
	}
	// A born-expired Put retains a barrier without a bucket. Staged Delete
	// must replace it with a tombstone and rollback its usage ledger exactly.
	if !c.PutEdgeWithExpirationHLC("barrier", "head-3", 1, time.Now().Add(-time.Hour), old) {
		t.Fatal("barrier seed rejected")
	}
	before := captureStagedDeleteState(c)
	keys := []EdgeKey[string]{
		{"tail", "head-1"}, {"tail", "head-1"},
		{"tail", "head-2"}, {"tail", "newer"}, {"tail", "newer"},
		{"self", "self"}, {"barrier", "head-3"}, {"missing", "edge"},
		{"missing", "edge"},
	}
	got := stageAndRollbackForTest(t, c, keys, deleteAt, expiration, func(stage *stagedEdgeDelete[string, string]) {
		assertStagedDeadlineIndexLocked(t, c)
		if _, exists := c.dict.forward["self"]; exists {
			t.Fatal("deleted dangling self-loop kept its freed dictionary ID")
		}
		if _, exists := c.edgeCausalBarriers[EdgeKey[string]{"barrier", "head-3"}]; exists {
			t.Fatal("barrier survived staged replacement")
		}
		if _, exists := c.edgeTombstones[EdgeKey[string]{"barrier", "head-3"}]; !exists {
			t.Fatal("staged tombstone missing")
		}
		if _, exists := c.edgeCausalUsage[EdgeKey[string]{"missing", "edge"}]; !exists ||
			c.edgeCausalHighWater <= before.stats.EdgeEntriesHighWater {
			t.Fatal("new tombstone was not counted in staged causal usage")
		}
		if c.edges.edgeCount != before.count-3 {
			t.Fatalf("staged edge count = %d, want %d", c.edges.edgeCount, before.count-3)
		}
		if c.edges.tf[stage.plans[0].tailID][stage.plans[0].headID] != nil {
			t.Fatal("deleted edge bucket remained in stage")
		}
		if index := c.headByTail[stage.plans[0].tailID]; index == nil || len(index.byProj["head"]) != 0 {
			t.Fatal("projection-colliding deleted heads remained in staged index")
		}
		newer := c.edges.bucket("tail", "newer")
		if newer == nil || len(newer.values) != 1 || newer.values[0].value != 3 || newer.values[0].hlc != (hlc.Timestamp{WallNs: 30}) {
			t.Fatalf("newer Add was not retained: %+v", newer)
		}
	})
	want := []bool{true, false, true, true, true, true, false, false, false}
	if !slices.Equal(got, want) {
		t.Fatalf("staged outcomes = %v, want %v", got, want)
	}
	if after := captureStagedDeleteState(c); !reflect.DeepEqual(after, before) {
		t.Fatalf("rollback drift: before=%+v after=%+v", before, after)
	}
	if id, ok := c.dict.lookup("self"); !ok || id != selfID {
		t.Fatalf("self-loop ID after rollback = (%d, %v), want (%d, true)", id, ok, selfID)
	}
	c.AddEdgeWithExpiration("other", "new", 1, expiration)
	if id, _ := c.dict.lookup("self"); id != selfID {
		t.Fatalf("rollback ID reused after another insert: %d, want %d", id, selfID)
	}
}

func TestStagedEdgeDeleteRollbackBlocksObservers(t *testing.T) {
	c := newGraphCacheWithStaging[string, string](time.Hour)
	c.EnablePrefixIndex(func(s string) string { return s })
	expiration := time.Now().Add(time.Hour)
	if !c.AddEdgeWithExpirationContrib("tail", "head", 1, expiration, ContribID{0: 1}) {
		t.Fatal("seed failed")
	}
	before := captureStagedDeleteState(c)
	ready := make(chan error, 1)
	release := make(chan struct{})
	finished := make(chan struct{}, 1)
	go func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.publicationGate.Lock()
		defer c.publicationGate.Unlock()
		key := EdgeKey[string]{"tail", "head"}
		stage, err := c.prepareStagedEdgeDeleteLocked(
			[]EdgeKey[string]{key}, hlc.Timestamp{WallNs: 20}, expiration, c.applicationTime(),
			map[EdgeKey[string]]string{key: key.Head},
		)
		if err != nil {
			ready <- err
			return
		}
		stage.applyLocked()
		ready <- nil
		<-release
		stage.rollbackLocked()
		finished <- struct{}{}
	}()
	if err := <-ready; err != nil {
		t.Fatal(err)
	}
	checks := []func(){
		func() { c.GetVertex("tail") },
		func() { c.GetWeight("tail", "head") },
		func() { c.GetEdgeDetail("tail", "head") },
		func() { c.GetEdgeDetails([]EdgeKey[string]{{"tail", "head"}}) },
		func() { c.EdgeCount() },
		func() { c.CausalMetadataStats() },
		func() { c.SnapshotReplication() },
		func() {
			c.ScanEdgesByPrefix(context.Background(), "", "", func(_, _, _, _ string, _ float32, _ time.Time) bool { return true })
		},
		func() { c.AddEdgeWithExpirationContrib("tail", "head", 1, expiration, ContribID{0: 1}) },
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
		close(release)
		t.Fatal("observer escaped the staged publication gate")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	<-finished
	wg.Wait()
	if after := captureStagedDeleteState(c); !reflect.DeepEqual(after, before) {
		t.Fatalf("concurrent rollback drift: before=%+v after=%+v", before, after)
	}
}

func TestStagedEdgeDeleteRollbackPreservesExpiredHigherFloor(t *testing.T) {
	c := newGraphCacheWithStaging[string, string](time.Hour)
	key := EdgeKey[string]{"tail", "head"}
	higher := hlc.Timestamp{WallNs: 30}
	if _, err := c.DeleteEdgesHLCChecked([]EdgeKey[string]{key}, higher, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	expiration := time.Now().Add(time.Hour)
	c.AddEdgeWithExpiration(key.Tail, key.Head, 1, expiration)
	before := captureStagedDeleteState(c)
	got := stageAndRollbackForTest(t, c, []EdgeKey[string]{key}, hlc.Timestamp{WallNs: 20}, expiration, func(*stagedEdgeDelete[string, string]) {
		if tombstone := c.edgeTombstones[key]; tombstone.ts != higher {
			t.Fatalf("expired larger floor was replaced: %+v", tombstone)
		}
	})
	if !slices.Equal(got, []bool{true}) {
		t.Fatalf("outcomes = %v, want [true]", got)
	}
	if after := captureStagedDeleteState(c); !reflect.DeepEqual(after, before) {
		t.Fatalf("rollback drift: before=%+v after=%+v", before, after)
	}
}

func TestStagedEdgeDeleteRollbackRestoresNilCausalMaps(t *testing.T) {
	c := newGraphCacheWithStaging[string, string](time.Hour)
	expiration := time.Now().Add(time.Hour)
	stageAndRollbackForTest(t, c, []EdgeKey[string]{{"absent", "edge"}}, hlc.Timestamp{WallNs: 20}, expiration, func(*stagedEdgeDelete[string, string]) {
		if c.edgeTombstones == nil || c.edgeCausalUsage == nil {
			t.Fatal("stage did not create tombstone and causal usage")
		}
	})
	if c.edgeTombstones != nil || c.edgeCausalBarriers != nil || c.edgeCausalUsage != nil ||
		c.edgeCausalUsageBytes != 0 || c.edgeCausalHighWater != 0 || c.edgeCausalBytesHighWater != 0 ||
		c.edgeTombstoneDeadlines.Len() != 0 || c.edgeTombstoneDeadlines.positions != nil ||
		c.edgeTombstoneDeadlineBytes != 0 || !c.oldestEdgeTombstoneDeadline.IsZero() {
		t.Fatal("rollback did not restore nil causal maps and counters")
	}
}

func TestStagedEdgeDeleteIndexedDeadlineUndo(t *testing.T) {
	c := newGraphCacheWithStaging[string, string](time.Hour)
	base := time.Now().Add(3 * time.Hour)
	old := hlc.Timestamp{WallNs: 10}
	for i, head := range []string{"a", "b", "c"} {
		if _, err := c.DeleteEdgesHLCChecked(
			[]EdgeKey[string]{{"tail", head}}, old, base.Add(time.Duration(i)*time.Hour),
		); err != nil {
			t.Fatal(err)
		}
	}
	before := captureStagedDeleteState(c)
	cases := []struct {
		name       string
		key        EdgeKey[string]
		expiration time.Time
	}{
		{"move minimum down", EdgeKey[string]{"tail", "a"}, base.Add(4 * time.Hour)},
		{"move maximum up", EdgeKey[string]{"tail", "c"}, base.Add(-time.Hour)},
		{"insert new minimum", EdgeKey[string]{"tail", "d"}, base.Add(-2 * time.Hour)},
		{"remove deadline", EdgeKey[string]{"tail", "a"}, time.Time{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stageAndRollbackForTest(t, c, []EdgeKey[string]{tc.key}, hlc.Timestamp{WallNs: 20}, tc.expiration, func(*stagedEdgeDelete[string, string]) {
				assertStagedDeadlineIndexLocked(t, c)
			})
			if after := captureStagedDeleteState(c); !reflect.DeepEqual(after, before) {
				t.Fatalf("deadline rollback drift: before=%+v after=%+v", before, after)
			}
		})
	}
}

func assertStagedDeadlineIndexLocked(t *testing.T, c *GraphCache[string, string]) {
	t.Helper()
	h := &c.edgeTombstoneDeadlines
	if len(h.entries) != len(h.positions) {
		t.Fatalf("deadline entries/positions = %d/%d", len(h.entries), len(h.positions))
	}
	var bytes uint64
	for i, entry := range h.entries {
		if position, ok := h.positions[entry.key]; !ok || position != i {
			t.Fatalf("deadline position for %v = (%d,%v), want %d", entry.key, position, ok, i)
		}
		if tombstone := c.edgeTombstones[entry.key]; !tombstone.expiration.Equal(entry.deadline) {
			t.Fatalf("deadline/tombstone mismatch for %v", entry.key)
		}
		if i > 0 && entry.deadline.Before(h.entries[(i-1)/2].deadline) {
			t.Fatalf("deadline heap order violated at %d", i)
		}
		bytes += causalEdgeDeadlineEntryBaseBytes + causalKeyPayloadBytes(entry.key.Tail) + causalKeyPayloadBytes(entry.key.Head)
	}
	if bytes != c.edgeTombstoneDeadlineBytes {
		t.Fatalf("deadline bytes = %d, want %d", c.edgeTombstoneDeadlineBytes, bytes)
	}
	for key, tombstone := range c.edgeTombstones {
		_, indexed := h.positions[key]
		if indexed != !tombstone.expiration.IsZero() {
			t.Fatalf("tombstone %v indexed=%v, expiration=%v", key, indexed, tombstone.expiration)
		}
	}
	if h.Len() > 0 && !c.oldestEdgeTombstoneDeadline.Equal(h.entries[0].deadline) {
		t.Fatalf("oldest deadline = %v, want %v", c.oldestEdgeTombstoneDeadline, h.entries[0].deadline)
	}
	if h.Len() == 0 && !c.oldestEdgeTombstoneDeadline.IsZero() {
		t.Fatalf("empty heap retained oldest deadline %v", c.oldestEdgeTombstoneDeadline)
	}
}

func TestStagedIndexedDeadlineJournalMatchesHeapOperations(t *testing.T) {
	rng := rand.New(rand.NewSource(1115))
	for _, initial := range []int{0, 1, 5, 64} {
		t.Run(fmt.Sprintf("initial=%d", initial), func(t *testing.T) {
			var staged, reference indexedCausalDeadlineHeap[int]
			for key := 0; key < initial; key++ {
				deadline := time.Unix(int64(rng.Intn(1000)+1), 0)
				staged.upsert(key, deadline)
				reference.upsert(key, deadline)
			}
			originalEntries := append([]causalDeadlineEntry[int](nil), staged.entries...)
			var originalPositions map[int]int
			if staged.positions != nil {
				originalPositions = make(map[int]int, len(staged.positions))
				for key, index := range staged.positions {
					originalPositions[key] = index
				}
			}
			originalPeak := staged.peak
			var journal stagedIndexedDeadlineUndo[int]
			journal.capture(&staged)
			for step := 0; step < 200; step++ {
				key := rng.Intn(initial + 30)
				if rng.Intn(4) == 0 {
					if got, want := journal.remove(&staged, key), reference.remove(key); got != want {
						t.Fatalf("step %d remove(%d) = %v, want %v", step, key, got, want)
					}
				} else {
					deadline := time.Unix(int64(rng.Intn(1000)+1), 0)
					if got, want := journal.upsert(&staged, key, deadline), reference.upsert(key, deadline); got != want {
						t.Fatalf("step %d upsert(%d) = %v, want %v", step, key, got, want)
					}
				}
				if !reflect.DeepEqual(staged.entries, reference.entries) ||
					!reflect.DeepEqual(staged.positions, reference.positions) || staged.peak != reference.peak {
					t.Fatalf("step %d staged heap differs from indexed heap", step)
				}
			}
			journal.restore(&staged)
			if !reflect.DeepEqual(staged.entries, originalEntries) ||
				!reflect.DeepEqual(staged.positions, originalPositions) || staged.peak != originalPeak {
				t.Fatal("journal rollback did not restore indexed heap")
			}
		})
	}
}

func TestStagedEdgeDeletePartialApplyPanicRollsBack(t *testing.T) {
	c := newGraphCacheWithStaging[string, string](time.Hour)
	expiration := time.Now().Add(time.Hour)
	c.AddEdgeWithExpiration("a", "one", 1, expiration)
	c.AddEdgeWithExpiration("b", "two", 2, expiration)
	before := captureStagedDeleteState(c)
	c.mu.Lock()
	c.publicationGate.Lock()
	keys := []EdgeKey[string]{{"a", "one"}, {"b", "two"}}
	stage, err := c.prepareStagedEdgeDeleteLocked(keys, hlc.Timestamp{WallNs: 20}, expiration, c.applicationTime(), nil)
	if err != nil {
		c.publicationGate.Unlock()
		c.mu.Unlock()
		t.Fatal(err)
	}
	second := stage.plans[1]
	delete(c.edges.tf[second.tailID], second.headID) // test-only drift after preparation
	func() {
		defer func() {
			if recovered := recover(); recovered == nil {
				t.Error("expected drift panic")
			}
		}()
		stage.applyLocked()
	}()
	c.publicationGate.Unlock()
	c.mu.Unlock()
	if after := captureStagedDeleteState(c); !reflect.DeepEqual(after, before) {
		t.Fatalf("partial apply rollback drift: before=%+v after=%+v", before, after)
	}
}

func BenchmarkStagedEdgeDeleteSparseRollback(b *testing.B) {
	for _, edgeCount := range []int{100, 10_000, 100_000} {
		b.Run(fmt.Sprintf("graph_and_tombstones=%d", edgeCount), func(b *testing.B) {
			c := newGraphCacheWithStaging[string, string](time.Hour)
			c.EnablePrefixIndex(func(s string) string { return s })
			expiration := time.Now().Add(time.Hour)
			for i := 0; i < edgeCount; i++ {
				c.AddEdgeWithExpiration("tail", fmt.Sprintf("head-%05d", i), 1, expiration)
				if _, err := c.DeleteEdgesHLCChecked(
					[]EdgeKey[string]{{"tombstone", fmt.Sprintf("key-%05d", i)}},
					hlc.Timestamp{WallNs: 10}, expiration.Add(time.Duration(i+1)*time.Second),
				); err != nil {
					b.Fatal(err)
				}
			}
			key := EdgeKey[string]{"tail", "head-00000"}
			projected := map[EdgeKey[string]]string{key: key.Head}
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				c.mu.Lock()
				c.publicationGate.Lock()
				stage, err := c.prepareStagedEdgeDeleteLocked([]EdgeKey[string]{key}, hlc.Timestamp{WallNs: 20}, expiration, c.applicationTime(), projected)
				if err != nil {
					b.Fatal(err)
				}
				stage.applyLocked()
				stage.rollbackLocked()
				c.publicationGate.Unlock()
				c.mu.Unlock()
			}
		})
	}
}
