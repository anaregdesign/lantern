package graphcache

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
)

func TestGraphMutationScratchReservation(t *testing.T) {
	m := &stagedGraphMutation[string, string]{scratchBytes: GraphMutationMaxScratchBytes - 1}
	if err := m.reserveScratch(1); err != nil {
		t.Fatal(err)
	}
	if err := m.reserveScratch(1); !errors.Is(err, ErrGraphMutationBudget) {
		t.Fatal(err)
	}
	if m.scratchBytes != GraphMutationMaxScratchBytes {
		t.Fatal("failed reservation changed ledger")
	}
}

func TestGraphMutationSearchScratchIncludesOldAndNew(t *testing.T) {
	for _, oldLarge := range []bool{false, true} {
		c := newVertexTransactionTestCache()
		old, newValue := "old", strings.Repeat("large ", 1024)
		if oldLarge {
			old, newValue = newValue, old
		}
		if err := c.PutVertex("a", old); err != nil {
			t.Fatal(err)
		}
		if err := c.PutVertex("b", "endpoint"); err != nil {
			t.Fatal(err)
		}
		before := captureVertexTransactionState(c)
		tx, err := c.BeginGraphMutation(context.Background(), []VertexItem[string, string]{{Key: "a", Value: newValue}}, []EdgeItem[string]{{Tail: "a", Head: "b", Weight: 1, ContribID: ContribID{1}}}, hlc.Timestamp{WallNs: 1}, 0)
		if tx != nil {
			tx.Abort()
		}
		if tx != nil || !errors.Is(err, search.ErrPreparationScratch) {
			t.Fatal(tx, err)
		}
		if after := captureVertexTransactionState(c); !equalVertexTransactionAbortState(before, after) {
			t.Fatal("failed analysis budget changed graph/index")
		}
	}
}

func TestGraphMutationCallerPayloadReservation(t *testing.T) {
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	vertices := []VertexItem[string, string]{{Key: "a"}, {Key: "b"}}
	edges := []EdgeItem[string]{{Tail: "a", Head: "b", Weight: 1, ContribID: ContribID{1}}}
	for _, bytes := range []uint64{GraphMutationMaxScratchBytes, GraphMutationMaxScratchBytes + 1, ^uint64(0)} {
		tx, err := c.BeginGraphMutation(context.Background(), vertices, edges, hlc.Timestamp{WallNs: 1}, bytes)
		if tx != nil || !errors.Is(err, ErrGraphMutationBudget) {
			t.Fatal(tx, err)
		}
	}
	if len(c.SnapshotVertices()) != 0 {
		t.Fatal("reservation published a Vertex")
	}
}
