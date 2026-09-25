package graphcache

import (
	"reflect"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

func TestVertexPutTransactionAbortRestoresExactState(t *testing.T) {
	c := newVertexTransactionTestCache()
	live := time.Now().Add(time.Hour)
	if _, err := c.PutVerticesWithExpirationHLCOutcomesChecked(
		[]VertexItem[string, string]{
			{Key: "replace", Value: "before searchable", Expiration: live},
			{Key: "expire", Value: "retained searchable", Expiration: live},
		},
		hlc.Timestamp{WallNs: 10},
	); err != nil {
		t.Fatal(err)
	}
	c.AddEdgeWithExpiration("replace", "head", 1, live)
	before := captureVertexTransactionState(c)
	beforeVertices, beforeEdges, beforeDict := c.vertices, c.edges, c.dict
	beforePrefix, beforeSearch := c.prefixIndex, c.searchIndex

	tx, err := c.BeginVertexPut(
		[]VertexItem[string, string]{
			{Key: "replace", Value: "after searchable", Expiration: live},
			{Key: "new", Value: "new searchable", Expiration: live},
			{Key: "expire", Value: "gone", Expiration: time.Now().Add(-time.Hour)},
		},
		hlc.Timestamp{WallNs: 20},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	tx.Abort()
	tx.Abort()

	if c.vertices != beforeVertices || c.edges != beforeEdges || c.dict != beforeDict ||
		c.prefixIndex != beforePrefix || c.searchIndex != beforeSearch {
		t.Fatal("Abort did not restore original state owners")
	}
	if after := captureVertexTransactionState(c); !equalVertexTransactionAbortState(after, before) {
		t.Fatalf("Put Abort drift: before=%+v after=%+v", before, after)
	}
	if got := c.SearchVertices("before", 10, ""); len(got) == 0 || got[0].ID != "replace" {
		t.Fatalf("Put Abort search state = %+v, want replace ranked first", got)
	}
}

func TestVertexDeleteTransactionAbortRestoresExactState(t *testing.T) {
	c := newVertexTransactionTestCache()
	expiration := time.Now().Add(time.Hour)
	if _, err := c.PutVerticesWithExpirationHLCOutcomesChecked(
		[]VertexItem[string, string]{
			{Key: "one", Value: "alpha", Expiration: expiration},
			{Key: "two", Value: "beta", Expiration: expiration},
		},
		hlc.Timestamp{WallNs: 10},
	); err != nil {
		t.Fatal(err)
	}
	c.AddEdgeWithExpiration("one", "two", 1, expiration)
	before := captureVertexTransactionState(c)
	beforeVertices, beforeEdges, beforeDict := c.vertices, c.edges, c.dict
	beforePrefix, beforeSearch := c.prefixIndex, c.searchIndex

	tx, err := c.BeginVertexDelete(
		[]string{"one", "absent", "one"}, hlc.Timestamp{WallNs: 20}, expiration,
	)
	if err != nil {
		t.Fatal(err)
	}
	tx.Abort()
	tx.Abort()

	if c.vertices != beforeVertices || c.edges != beforeEdges || c.dict != beforeDict ||
		c.prefixIndex != beforePrefix || c.searchIndex != beforeSearch {
		t.Fatal("Abort did not restore original state owners")
	}
	if after := captureVertexTransactionState(c); !equalVertexTransactionAbortState(after, before) {
		t.Fatalf("Delete Abort drift: before=%+v after=%+v", before, after)
	}
	if weight, ok := c.GetWeight("one", "two"); !ok || weight != 1 {
		t.Fatalf("Abort changed incident edge = %v/%v", weight, ok)
	}
	if got := c.SearchVertices("alpha", 10, ""); len(got) != 1 || got[0].ID != "one" {
		t.Fatalf("Delete Abort search state = %+v, want one", got)
	}
}

func TestVertexPutTransactionAbortRestoresExpiredPhysicalSlot(t *testing.T) {
	c := newVertexTransactionTestCache()
	key := "expired-physical"
	expired := time.Now().Add(-time.Hour)
	c.mu.Lock()
	c.vertices.PutWithExpiration(key, "old", expired)
	c.dict.intern(key)
	c.prefixIndex.insert(key)
	c.mu.Unlock()
	before := captureVertexTransactionState(c)

	tx, err := c.BeginVertexPut(
		[]VertexItem[string, string]{{Key: key, Value: "new", Expiration: time.Now().Add(time.Hour)}},
		hlc.Timestamp{WallNs: 20}, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	tx.Abort()

	value, expiration, present := c.vertices.PeekWithExpiration(key)
	if !present || value != "old" || !expiration.Equal(expired) {
		t.Fatalf("restored physical slot = %q/%v/%v, want old/%v/true", value, expiration, present, expired)
	}
	if after := captureVertexTransactionState(c); !equalVertexTransactionAbortState(after, before) {
		t.Fatalf("expired physical Abort drift: before=%+v after=%+v", before, after)
	}
}

func TestVertexPutTransactionAbortRestoresDictionaryFreeCells(t *testing.T) {
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	expiration := time.Now().Add(time.Hour)
	if err := c.PutVerticesWithExpirationChecked([]VertexItem[string, string]{
		{Key: "free-a", Value: "a", Expiration: expiration},
		{Key: "free-b", Value: "b", Expiration: expiration},
		{Key: "keep", Value: "keep", Expiration: expiration},
	}); err != nil {
		t.Fatal(err)
	}
	if deleted := c.DeleteVertices([]string{"free-a", "free-b"}); deleted != 2 {
		t.Fatalf("seed DeleteVertices = %d, want 2", deleted)
	}
	before := captureVertexTransactionState(c)

	tx, err := c.BeginVertexPut(
		[]VertexItem[string, string]{
			{Key: "new-a", Value: "a", Expiration: expiration},
			{Key: "new-b", Value: "b", Expiration: expiration},
			{Key: "keep", Value: "expired", Expiration: time.Now().Add(-time.Hour)},
		},
		hlc.Timestamp{WallNs: 20},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	tx.Abort()

	if after := captureVertexTransactionState(c); !reflect.DeepEqual(after, before) {
		t.Fatalf("dictionary free-cell Abort drift: before=%+v after=%+v", before, after)
	}
}

func TestVertexPutTransactionAbortRestoresCausalDeadlineHeap(t *testing.T) {
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	now := time.Now()
	if _, err := c.DeleteVerticesHLCChecked(
		[]string{"later"}, hlc.Timestamp{WallNs: 10}, now.Add(2*time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DeleteVerticesHLCChecked(
		[]string{"earliest"}, hlc.Timestamp{WallNs: 10}, now.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	before := captureVertexTransactionState(c)

	tx, err := c.BeginVertexPut(
		[]VertexItem[string, string]{{Key: "earliest", Value: "live", Expiration: now.Add(3 * time.Hour)}},
		hlc.Timestamp{WallNs: 20},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	tx.Abort()

	if after := captureVertexTransactionState(c); !reflect.DeepEqual(after, before) {
		t.Fatalf("causal deadline Abort drift: before=%+v after=%+v", before, after)
	}
}

func TestVertexDeleteTransactionAbortRestoresEqualHLCTombstoneDeadline(t *testing.T) {
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	ts := hlc.Timestamp{WallNs: 20}
	earlier := time.Now().Add(time.Hour)
	if _, err := c.DeleteVerticesHLCChecked([]string{"key"}, ts, earlier); err != nil {
		t.Fatal(err)
	}
	before := captureVertexTransactionState(c)

	tx, err := c.BeginVertexDelete([]string{"key"}, ts, earlier.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	tx.Abort()

	if after := captureVertexTransactionState(c); !reflect.DeepEqual(after, before) {
		t.Fatalf("equal-HLC Delete Abort drift: before=%+v after=%+v", before, after)
	}
}

func TestVertexTransactionPanicRestoresStateAndLocks(t *testing.T) {
	c := newVertexTransactionTestCache()
	expiration := time.Now().Add(time.Hour)
	if err := c.PutVertexWithExpiration("victim", "searchable", expiration); err != nil {
		t.Fatal(err)
	}
	c.dict.mu.Lock()
	delete(c.dict.forward, "victim")
	c.dict.mu.Unlock()
	before := captureVertexTransactionState(c)

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_, _ = c.BeginVertexDelete(
			[]string{"victim"}, hlc.Timestamp{WallNs: 20}, expiration,
		)
	}()
	if recovered == nil {
		t.Fatal("malformed dictionary did not panic during staged apply")
	}
	if after := captureVertexTransactionState(c); !equalVertexTransactionAbortState(after, before) {
		t.Fatalf("panic rollback drift: before=%+v after=%+v", before, after)
	}
	if !c.mu.TryLock() {
		t.Fatal("panic left GraphCache lock held")
	}
	c.mu.Unlock()
	if !c.publicationGate.TryLock() {
		t.Fatal("panic left publication gate held")
	}
	c.publicationGate.Unlock()
	if !c.searchCommitMu.TryLock() {
		t.Fatal("panic left search commit lock held")
	}
	c.searchCommitMu.Unlock()
}
