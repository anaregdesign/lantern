package graphcache

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
)

func TestEdgeDeleteTransactionResultAndCommit(t *testing.T) {
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	c.EnablePrefixIndex(func(key string) string { return key })
	expiration := time.Now().Add(time.Hour)
	c.AddEdgeWithExpiration("tail", "present", 1, expiration)
	rejected := EdgeKey[string]{Tail: "tail", Head: "rejected"}
	if _, err := c.DeleteEdgesHLCChecked([]EdgeKey[string]{rejected}, hlc.Timestamp{WallNs: 30}, expiration); err != nil {
		t.Fatal(err)
	}
	present := EdgeKey[string]{Tail: "tail", Head: "present"}
	absent := EdgeKey[string]{Tail: "tail", Head: "absent"}
	tx, err := c.BeginEdgeDelete(
		[]EdgeKey[string]{present, absent, present, rejected},
		hlc.Timestamp{WallNs: 20}, expiration,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Abort()
	want := EdgeDeleteStageResult[string]{
		Existed: []bool{true, false, false, false},
		Accepted: []IndexedEdgeDelete[string]{
			{Index: 0, Key: present},
			{Index: 1, Key: absent},
			{Index: 2, Key: present},
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
	tx.Abort() // A deferred Abort after a successful WAL commit is harmless.
	if _, ok := c.GetWeight(present.Tail, present.Head); ok {
		t.Fatal("committed Delete left the edge visible")
	}
	c.mu.RLock()
	_, hasPresent := c.edgeTombstones[present]
	_, hasAbsent := c.edgeTombstones[absent]
	stale := c.edgeTombstones[rejected]
	c.mu.RUnlock()
	if !hasPresent || !hasAbsent || stale.ts != (hlc.Timestamp{WallNs: 30}) {
		t.Fatalf("causal transitions = (present=%v, absent=%v, rejected=%+v)", hasPresent, hasAbsent, stale)
	}
}

func TestEdgeDeleteTransactionAbortRestoresState(t *testing.T) {
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	c.EnablePrefixIndex(func(key string) string { return key })
	expiration := time.Now().Add(time.Hour)
	c.AddEdgeWithExpiration("tail", "head", 1, expiration)
	before := captureStagedDeleteState(c)
	tx, err := c.BeginEdgeDelete(
		[]EdgeKey[string]{{Tail: "tail", Head: "head"}, {Tail: "absent", Head: "head"}},
		hlc.Timestamp{WallNs: 20}, expiration,
	)
	if err != nil {
		t.Fatal(err)
	}
	tx.Abort()
	tx.Abort()
	if after := captureStagedDeleteState(c); !reflect.DeepEqual(after, before) {
		t.Fatalf("transaction Abort drift: before=%+v after=%+v", before, after)
	}
}

func TestEdgeDeleteTransactionProjectionMayRead(t *testing.T) {
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	readDuringBegin := false
	var readOK atomic.Bool
	c.EnablePrefixIndex(func(key string) string {
		if readDuringBegin {
			_, ok := c.GetWeight("tail", "head")
			readOK.Store(ok)
		}
		return key
	})
	expiration := time.Now().Add(time.Hour)
	c.AddEdgeWithExpiration("tail", "head", 1, expiration)
	readDuringBegin = true
	type beginResult struct {
		tx  *EdgeDeleteTransaction[string, string]
		err error
	}
	ready := make(chan beginResult, 1)
	go func() {
		tx, err := c.BeginEdgeDelete(
			[]EdgeKey[string]{{Tail: "tail", Head: "head"}}, hlc.Timestamp{WallNs: 20}, expiration,
		)
		ready <- beginResult{tx, err}
	}()
	select {
	case result := <-ready:
		if result.err != nil {
			t.Fatal(result.err)
		}
		result.tx.Abort()
	case <-time.After(time.Second):
		t.Fatal("BeginEdgeDelete deadlocked in the head projection")
	}
	if !readOK.Load() {
		t.Fatal("projection could not read the original edge")
	}
}

func TestEdgeDeleteTransactionBlocksObservers(t *testing.T) {
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	c.EnablePrefixIndex(func(key string) string { return key })
	c.EnableSearchIndex(func(_ string, value string) search.Document { return search.Text(value) }, strings.Compare)
	expiration := time.Now().Add(time.Hour)
	if !c.AddEdgeWithExpirationContrib("tail", "head", 1, expiration, ContribID{0: 1}) {
		t.Fatal("seed contribution failed")
	}
	if err := c.PutVertexWithExpiration("searchable", "visible term", expiration); err != nil {
		t.Fatal(err)
	}
	before := captureStagedDeleteState(c)
	tx, err := c.BeginEdgeDelete(
		[]EdgeKey[string]{{Tail: "tail", Head: "head"}}, hlc.Timestamp{WallNs: 20}, expiration,
	)
	if err != nil {
		t.Fatal(err)
	}
	checks := []func(){
		func() { c.GetVertex("tail") },
		func() { c.GetWeight("tail", "head") },
		func() { c.GetEdgeDetail("tail", "head") },
		func() { c.GetEdgeDetails([]EdgeKey[string]{{Tail: "tail", Head: "head"}}) },
		func() { c.SnapshotReplication() },
		func() { c.SearchVertices("visible", 1, "") },
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
		tx.Abort()
		t.Fatal("observer escaped the staged transaction")
	case <-time.After(30 * time.Millisecond):
	}
	tx.Abort()
	wg.Wait()
	if after := captureStagedDeleteState(c); !reflect.DeepEqual(after, before) {
		t.Fatalf("observer after Abort changed state: before=%+v after=%+v", before, after)
	}
}

func TestEdgeDeleteTransactionBeginFailureReleasesLocks(t *testing.T) {
	plain := NewGraphCache[string, string](time.Hour)
	if tx, err := plain.BeginEdgeDelete(nil, hlc.Timestamp{}, time.Time{}); tx != nil || err == nil {
		t.Fatalf("ordinary cache BeginEdgeDelete = (%v, %v), want staging-gate error", tx, err)
	}

	c := NewGraphCacheWithStaging[string, string](time.Hour)
	c.applicationClock = func() time.Time { panic("injected clock failure") }
	func() {
		defer func() {
			if recovered := recover(); recovered != "injected clock failure" {
				t.Errorf("recovered = %v, want injected clock failure", recovered)
			}
		}()
		_, _ = c.BeginEdgeDelete([]EdgeKey[string]{{Tail: "tail", Head: "head"}}, hlc.Timestamp{WallNs: 20}, time.Now().Add(time.Hour))
	}()
	c.applicationClock = nil
	finished := make(chan struct{})
	go func() {
		c.AddEdge("tail", "head", 1)
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("Begin panic left GraphCache locks held")
	}
}

func TestReplicatedEdgeDeleteTransactionMayExceedLocalCausalLimit(t *testing.T) {
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	c.SetCausalMetadataLimits(CausalMetadataLimits{MaxEdgeEntries: 1})
	expiration := time.Now().Add(time.Hour)
	first := EdgeKey[string]{Tail: "first", Head: "head"}
	second := EdgeKey[string]{Tail: "second", Head: "head"}
	if _, err := c.DeleteEdgesHLCChecked([]EdgeKey[string]{first}, hlc.Timestamp{WallNs: 10}, expiration); err != nil {
		t.Fatal(err)
	}
	if tx, err := c.BeginEdgeDelete([]EdgeKey[string]{second}, hlc.Timestamp{WallNs: 20}, expiration); tx != nil || err == nil {
		if tx != nil {
			tx.Abort()
		}
		t.Fatalf("strict stage beyond causal limit = (%v, %v)", tx, err)
	}
	tx, err := c.BeginReplicatedEdgeDelete([]EdgeKey[string]{second}, hlc.Timestamp{WallNs: 20}, expiration)
	if err != nil {
		t.Fatal(err)
	}
	if got := tx.Result().Accepted; len(got) != 1 || got[0].Key != second {
		tx.Abort()
		t.Fatalf("replicated accepted set = %+v", got)
	}
	tx.Commit()
	if stats := c.CausalMetadataStats(); stats.EdgeEntries != 2 || !stats.EdgeOverLimit {
		t.Fatalf("replicated stage did not converge above local limit: %+v", stats)
	}
}

func BenchmarkEdgeDeleteTransactionAbort(b *testing.B) {
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	c.EnablePrefixIndex(func(key string) string { return key })
	expiration := time.Now().Add(time.Hour)
	c.AddEdgeWithExpiration("tail", "head", 1, expiration)
	key := EdgeKey[string]{Tail: "tail", Head: "head"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tx, err := c.BeginEdgeDelete([]EdgeKey[string]{key}, hlc.Timestamp{WallNs: 20}, expiration)
		if err != nil {
			b.Fatal(err)
		}
		tx.Abort()
	}
}
