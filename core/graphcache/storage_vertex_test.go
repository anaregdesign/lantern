package graphcache

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestStagedVertexMutationUsesStoredPreimage(t *testing.T) {
	ctx := t.Context()
	db, err := graphstore.Open(ctx, t.TempDir(), hlc.NodeID{1})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := graphstore.NewRows[string, string](tarp.StringCodec{}, tarp.StringCodec{})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	deadline := start.Add(2 * time.Hour)
	stored := hlc.Timestamp{WallNs: 20}
	older := hlc.Timestamp{WallNs: 10}
	newer := hlc.Timestamp{WallNs: 30}
	const liveKey, barrierKey, deletedKey, expiringKey = "live\x00", "barrier", "deleted", "expiring"
	if err := db.UpdateRows(ctx, func(gen uint64, _ *tarp.Snapshot, w graphstore.Writer) error {
		return errors.Join(
			rows.PutVertex(w, gen, liveKey, graphstore.Vertex[string]{Value: "payload", Expiration: deadline, HLC: stored}),
			rows.PutVertex(w, gen, expiringKey, graphstore.Vertex[string]{Value: "retained", Expiration: start.Add(time.Second), HLC: stored}),
			rows.PutFloor(w, gen, graphstore.FloorKey[string]{Kind: graphstore.VertexBarrier, Key: barrierKey}, graphstore.Floor{HLC: stored}),
			rows.PutFloor(w, gen, graphstore.FloorKey[string]{Kind: graphstore.VertexTombstone, Key: deletedKey}, graphstore.Floor{HLC: stored, Expiration: deadline}),
		)
	}); err != nil {
		t.Fatal(err)
	}
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	now := start
	c.applicationClock = func() time.Time { return now }
	view, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(c.RestoreStorage(ctx, rows, view), view.Close()); err != nil {
		t.Fatal(err)
	}
	if err := c.CompleteStorageRecovery(ctx); err != nil {
		t.Fatal(err)
	}

	// Deliberately erase the mirror's admission inputs. The narrow Tarp
	// projection and floors still decide these operations.
	c.vertices.Delete(liveKey)
	delete(c.vertexHLC, liveKey)
	delete(c.vertexCausalBarriers, barrierKey)
	delete(c.vertexTombstones, deletedKey)
	tx, err := c.BeginVertexPutContext(ctx, []VertexItem[string, string]{
		{Key: liveKey, Value: "old"},
		{Key: barrierKey, Value: "old"},
		{Key: deletedKey, Value: "old"},
	}, older, false)
	if err != nil {
		t.Fatal(err)
	}
	result := tx.Result()
	tx.Abort()
	if len(result.Accepted) != 0 || !slices.Equal(result.Outcomes, []PutOutcome{PutOutcomeSuperseded, PutOutcomeSuperseded, PutOutcomeSuperseded}) {
		t.Fatalf("LWW outcomes = %+v", result)
	}

	tx, err = c.BeginVertexPutContext(ctx, []VertexItem[string, string]{{Key: liveKey, Value: "conditional"}}, newer, true)
	if err != nil {
		t.Fatal(err)
	}
	result = tx.Result()
	tx.Abort()
	if len(result.Accepted) != 0 || !slices.Equal(result.Outcomes, []PutOutcome{PutOutcomeConditionNotMet}) {
		t.Fatalf("conditional outcomes = %+v", result)
	}
	if tx, err := c.BeginVertexPutContext(ctx, []VertexItem[string, string]{
		{Key: liveKey, Value: "shorter", Expiration: start.Add(time.Hour), DenyLifecycleReduction: true},
	}, newer, false); tx != nil || !errors.Is(err, ErrLifecycleEffectDenied) {
		t.Fatalf("shortening result = %v, %v", tx, err)
	}

	// Delete's Existed result observes retained physical presence, including
	// an elapsed TTL, in original request order.
	now = start.Add(time.Minute)
	deleted, err := c.BeginVertexDeleteContext(ctx, []string{expiringKey, expiringKey, barrierKey}, newer, deadline)
	if err != nil {
		t.Fatal(err)
	}
	defer deleted.Abort()
	deletedResult := deleted.Result()
	if len(deletedResult.Accepted) != 3 || !slices.Equal(deletedResult.Existed, []bool{true, false, false}) {
		t.Fatalf("Delete outcomes = %+v", deletedResult)
	}
	selected := StorageKeys[string]{Vertices: []string{expiringKey, barrierKey}}
	if err := db.UpdateRows(ctx, func(gen uint64, snap *tarp.Snapshot, w graphstore.Writer) error {
		return deleted.StoreRows(ctx, rows, snap, w, gen, selected)
	}); err != nil {
		t.Fatal(err)
	}
	deleted.Commit()
	if err := db.WithCommittedSnapshot(func(gen uint64, snap *tarp.Snapshot) error {
		if _, found, err := rows.GetVertexMetadata(snap, gen, expiringKey); err != nil || found {
			t.Fatalf("deleted Vertex projection: found=%t err=%v", found, err)
		}
		floor, found, err := rows.GetFloor(snap, gen, graphstore.FloorKey[string]{Kind: graphstore.VertexTombstone, Key: expiringKey})
		if err != nil || !found || floor.HLC != newer || !floor.Expiration.Equal(deadline) {
			t.Fatalf("committed Delete floor = %+v, %t, %v", floor, found, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if tx, err := c.BeginVertexPutContext(cancelled, nil, newer, false); tx != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled stage = %v, %v", tx, err)
	}
	if c.StorageError() != nil {
		t.Fatalf("cancellation fenced storage: %v", c.StorageError())
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if tx, err := c.BeginVertexDeleteContext(ctx, []string{expiringKey}, newer, deadline); tx != nil || !errors.Is(err, tarp.ErrClosed) {
		t.Fatalf("failed projection read = %v, %v", tx, err)
	}
	if !errors.Is(c.StorageError(), tarp.ErrClosed) {
		t.Fatalf("storage fence lost read error: %v", c.StorageError())
	}
}
