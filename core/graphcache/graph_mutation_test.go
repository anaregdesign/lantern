package graphcache

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
)

func TestGraphMutationCommitAndAbort(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "abort", true: "commit"}[commit], func(t *testing.T) {
			c := newVertexTransactionTestCache()
			now := time.Now()
			c.applicationClock = func() time.Time { return now }
			if err := c.PutVertex("collection", "collection"); err != nil {
				t.Fatal(err)
			}
			if err := c.PutVertex("reuse", "discard"); err != nil {
				t.Fatal(err)
			}
			c.DeleteVertices([]string{"reuse"})
			before := captureVertexTransactionState(c)
			tx, err := c.BeginGraphMutation(context.Background(), []VertexItem[string, string]{{Key: "item", Value: "searchable", Expiration: now.Add(time.Hour)}}, []EdgeItem[string]{{Tail: "collection", Head: "item", Weight: 2, ContribID: ContribID{1}}, {Tail: "collection", Head: "item", Weight: 3, ContribID: ContribID{2}}}, hlc.Timestamp{WallNs: 20}, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Abort()
			result := tx.Result()
			if !reflect.DeepEqual(result.Vertices.Outcomes, []PutOutcome{PutOutcomeAppliedAndLive}) || !reflect.DeepEqual(result.Edges.Effective, []float32{2, 5}) || len(result.Edges.Accepted) != 2 || !result.Edges.Accepted[0].Item.NoEndpointCreation {
				t.Fatal(result)
			}
			if commit {
				tx.Commit()
			}
			tx.Abort()
			tx.Abort()
			assertTransactionPanics(t, tx.Commit)
			if !commit {
				after := captureVertexTransactionState(c)
				// Index generation/diagnostic counters are monotonic, while its
				// current graph/document representation must be exactly restored.
				if !equalVertexTransactionAbortState(before, after) {
					t.Fatalf("abort state drift: before=%+v after=%+v", before, after)
				}
				if got := c.SearchVertices("searchable", 10, "item"); len(got) != 0 {
					t.Fatal(got)
				}
			} else {
				if got, ok := c.GetWeight("collection", "item"); !ok || got != 5 {
					t.Fatal(got, ok)
				}
				if got := c.SearchVertices("searchable", 10, "item"); len(got) != 1 {
					t.Fatal(got)
				}
			}
		})
	}
}

// This context cancels exactly after the new Vertex becomes hidden. It checks
// the underlying slot directly, without reentering the graph visibility lock.
type graphMutationCancelAfterApply struct {
	context.Context
	cache *GraphCache[string, string]
}

func (ctx graphMutationCancelAfterApply) Err() error {
	if _, _, present := ctx.cache.vertices.PeekWithExpiration("new"); present {
		return context.Canceled
	}
	return nil
}

func TestGraphMutationCancelAfterHiddenApplyRollsBackUnion(t *testing.T) {
	c := newVertexTransactionTestCache()
	if err := c.PutVertex("collection", "original"); err != nil {
		t.Fatal(err)
	}
	before := captureVertexTransactionState(c)
	ctx := graphMutationCancelAfterApply{Context: context.Background(), cache: c}
	tx, err := c.BeginGraphMutation(ctx,
		[]VertexItem[string, string]{{Key: "new", Value: "hidden"}},
		[]EdgeItem[string]{{Tail: "collection", Head: "new", Weight: 2, ContribID: ContribID{1}}},
		hlc.Timestamp{WallNs: 1}, 0)
	if tx != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(tx, err)
	}
	after := captureVertexTransactionState(c)
	if !equalVertexTransactionAbortState(before, after) {
		t.Fatal("hidden cancellation did not undo the complete graph and dictionary")
	}
	if c.SearchVertices("hidden", 10, "new") != nil {
		t.Fatal("hidden search document survived cancellation")
	}
}

func TestGraphMutationPreWALBudgetsAbortHiddenState(t *testing.T) {
	for _, exclusive := range []bool{false, true} {
		c := NewGraphCacheWithStaging[string, string](time.Hour)
		tx, err := c.BeginGraphMutation(t.Context(), []VertexItem[string, string]{{Key: "a", Value: "value"}}, []EdgeItem[string]{{Tail: "a", Head: "a", Weight: 1, ContribID: ContribID{1}}}, hlc.Timestamp{WallNs: 1}, 0)
		if err != nil {
			t.Fatal(err)
		}
		if exclusive {
			tx.mixed.lockedAt = time.Now().Add(-graphMutationLockBudget - time.Second)
		} else {
			tx.mixed.deadline = time.Now().Add(-time.Second)
		}
		err = tx.CheckBeforeWAL()
		tx.Abort()
		if !errors.Is(err, ErrGraphMutationBudget) || len(c.SnapshotVertices()) != 0 || len(c.SnapshotEdges()) != 0 {
			t.Fatal("expired hidden stage escaped", err)
		}
	}
}

func TestGraphMutationSearchReplacementIsBounded(t *testing.T) {
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	extract := func(_ string, value string) search.Document {
		c.mu.Lock()
		c.searchIndex = newSearchIndex[string](true, search.SearchAnalysisLimits{}, strings.Compare)
		c.mu.Unlock()
		return search.Text(value)
	}
	c.EnableSearchIndex(extract, strings.Compare)
	tx, err := c.BeginGraphMutation(context.Background(),
		[]VertexItem[string, string]{{Key: "a", Value: "a"}, {Key: "b", Value: "b"}},
		[]EdgeItem[string]{{Tail: "a", Head: "b", Weight: 1, ContribID: ContribID{1}}},
		hlc.Timestamp{WallNs: 1}, 0)
	if tx != nil {
		tx.Abort()
	}
	if tx != nil || !errors.Is(err, ErrGraphMutationBudget) {
		t.Fatal(tx, err)
	}
	if len(c.SnapshotVertices()) != 0 || len(c.SnapshotEdges()) != 0 {
		t.Fatal("repreparation published a partial graph")
	}
}

func TestGraphMutationEndpointFailureLeavesWholeCallUnchanged(t *testing.T) {
	for _, expired := range []bool{false, true} {
		c := newVertexTransactionTestCache()
		now := time.Now()
		c.applicationClock = func() time.Time { return now }
		if err := c.PutVertex("collection", "kept"); err != nil {
			t.Fatal(err)
		}
		before := captureVertexTransactionState(c)
		item := VertexItem[string, string]{Key: "item", Value: "unpublished"}
		head := "missing"
		if expired {
			item.Expiration = now.Add(-time.Second)
			head = "item"
		}
		tx, err := c.BeginGraphMutation(context.Background(), []VertexItem[string, string]{item}, []EdgeItem[string]{{Tail: "collection", Head: head, Weight: 1, ContribID: ContribID{1}}}, hlc.Timestamp{WallNs: 20}, 0)
		if tx != nil || !errors.Is(err, ErrEdgeEndpointNotLive) {
			t.Fatal(tx, err)
		}
		if after := captureVertexTransactionState(c); !reflect.DeepEqual(before, after) {
			t.Fatal("rejected call changed graph")
		}
	}
}

func TestGraphMutationProjectionConvergesAndDoesNotRevive(t *testing.T) {
	for _, order := range [][]int{{0, 1}, {1, 0}} {
		c := NewGraphCacheWithStaging[string, string](time.Hour)
		c.RetainDanglingEdgeHistory()
		if err := c.PutVertex("collection", "live"); err != nil {
			t.Fatal(err)
		}
		for _, i := range order {
			tx, err := c.BeginReplicatedGraphMutation(context.Background(), []VertexItem[string, string]{{Key: "item", Value: []string{"old", "winner"}[i]}}, []EdgeItem[string]{{Tail: "collection", Head: "item", Weight: 1, ContribID: ContribID{byte(i + 1)}}}, hlc.Timestamp{WallNs: int64(i + 1)}, 0)
			if err != nil {
				t.Fatal(err)
			}
			if i == 0 && order[0] == 1 && len(tx.Result().Vertices.Accepted) != 0 {
				t.Fatal("losing Vertex was accepted")
			}
			tx.Commit()
		}
		if got, _ := c.GetVertex("item"); got != "winner" {
			t.Fatal(got)
		}
		if got, _ := c.GetWeight("collection", "item"); got != 2 {
			t.Fatal(got)
		}
		// A relay's locally losing Vertex must not suppress the origin Add.
		tx, err := c.BeginReplicatedGraphMutation(context.Background(), nil, []EdgeItem[string]{{Tail: "absent", Head: "item", Weight: 1, ContribID: ContribID{3}}}, hlc.Timestamp{WallNs: 3}, 0)
		if err != nil {
			t.Fatal(err)
		}
		tx.Commit()
		if _, ok := c.GetVertex("absent"); ok {
			t.Fatal("trusted Add revived an endpoint")
		}
	}
	// Negative control: rejecting an entire older call after a newer Vertex
	// loses contribution 1 in one order, while accepting both retains it.
	wholeCall := func(order []int) int {
		latest, total := 0, 0
		for _, v := range order {
			if v > latest {
				latest = v
				total++
			}
		}
		return total
	}
	if wholeCall([]int{1, 2}) == wholeCall([]int{2, 1}) {
		t.Fatal("whole-call rejection counterexample lost")
	}
}

func TestGraphMutationPreparationReentryAndBounds(t *testing.T) {
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	c.EnablePrefixIndex(func(key string) string { c.GetVertex("observe"); return key })
	c.EnableSearchIndex(func(_ string, value string) search.Document { c.GetVertex("observe"); return search.Text(value) }, strings.Compare)
	vertices := []VertexItem[string, string]{{Key: "a", Value: "searchable"}, {Key: "b", Value: "target"}}
	edges := []EdgeItem[string]{{Tail: "a", Head: "b", Weight: 1, ContribID: ContribID{1}}}
	tx, err := c.BeginGraphMutation(context.Background(), vertices, edges, hlc.Timestamp{WallNs: 1}, 0)
	if err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if tx, err := c.BeginGraphMutation(ctx, vertices, edges, hlc.Timestamp{WallNs: 2}, 0); tx != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(tx, err)
	}
	for _, bad := range [][]VertexItem[string, string]{nil, {vertices[0], vertices[0]}, make([]VertexItem[string, string], GraphMutationMaxItems)} {
		if tx, err := c.BeginGraphMutation(context.Background(), bad, edges, hlc.Timestamp{WallNs: 2}, 0); tx != nil || !errors.Is(err, ErrGraphMutationShape) {
			t.Fatal(tx, err)
		}
	}
	// Retained expired rows count before clone or opportunistic pruning.
	c.mu.Lock()
	bucket := c.edges.bucket("a", "b")
	bucket.mu.Lock()
	bucket.values = make([]weightValue, GraphMutationMaxBucketRows+1)
	bucket.mu.Unlock()
	c.mu.Unlock()
	if tx, err := c.BeginGraphMutation(context.Background(), vertices, edges, hlc.Timestamp{WallNs: 2}, 0); tx != nil || !errors.Is(err, ErrGraphMutationBudget) {
		t.Fatal(tx, err)
	}
}
