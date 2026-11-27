package graphcache

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestStoredPrefixesReplaceBothRadixIndexes(t *testing.T) {
	db, err := graphstore.Open(t.Context(), t.TempDir(), hlc.NodeID{1})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := graphstore.NewRows[string, string](tarp.StringCodec{}, tarp.StringCodec{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateRows(t.Context(), func(g uint64, _ *tarp.Snapshot, b graphstore.Writer) error {
		for _, key := range []string{"a", "a\x00", "b", "c"} {
			if err := rows.PutVertex(b, g, key, graphstore.Vertex[string]{Value: "searchable"}); err != nil {
				return err
			}
		}
		for _, head := range []string{"a", "a\x00", "b", "c"} {
			if err := rows.PutEdge(b, g, graphstore.EdgeKey[string]{Tail: "a", Head: head}, graphstore.Edge{Base: &graphstore.Source{Weight: 1}}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	c.EnableStorageKeyPrefixIndex(func(s string) string { return s })
	c.EnableSearchIndex(textExtract, compareStringID)
	if err := errors.Join(c.RestoreStorage(t.Context(), rows, view), view.Close()); err != nil {
		t.Fatal(err)
	}
	if c.prefixIndex != nil || c.headByTail != nil {
		t.Fatal("stored prefix or head index still has a resident radix")
	}
	ranges := []KeyRange{{Lower: "a", Upper: "b"}, {Lower: "c", Upper: "d"}}
	scope, err := NewQueryView(ranges, ranges)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithQueryView(t.Context(), scope)
	if got := c.CountByPrefixContext(ctx, ""); got != 3 {
		t.Fatalf("scoped count = %d", got)
	}
	var keys []string
	if more, ok := c.ScanByPrefixPage(ctx, "", "c", 3, true, func(_ string, k, _ string) bool {
		keys = append(keys, k)
		return true
	}); more || !ok || !reflect.DeepEqual(keys, []string{"a\x00", "a"}) {
		t.Fatalf("reverse cursor scan = %q, more=%v ok=%v", keys, more, ok)
	}
	var heads []string
	if more, ok := c.ScanEdgesByPrefixPage(ctx, "a", "", "a", "a", 4, func(_ string, _ string, _ string, head string, _ float32, _ time.Time) bool {
		heads = append(heads, head)
		return true
	}); more || !ok || !reflect.DeepEqual(heads, []string{"a\x00", "c"}) {
		t.Fatalf("exclusive head cursor = %q, more=%v ok=%v", heads, more, ok)
	}
	if results := c.SearchVertices("searchable", 5, "a"); len(results) != 2 {
		t.Fatalf("search with stored prefix = %+v", results)
	}
	victims, err := c.DeleteByPrefixKeysWithPreflight(ctx, "a", 1, func([]string) error { return nil })
	if err != nil || !reflect.DeepEqual(victims, []string{"a"}) {
		t.Fatalf("prefix delete = %q, %v", victims, err)
	}
}

func TestStoredPrefixDeleteCapsLiveCandidatesBeforeCausalFiltering(t *testing.T) {
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
	expiration := time.Now().Add(time.Hour)
	old, incoming := hlc.Timestamp{WallNs: 9}, hlc.Timestamp{WallNs: 10}
	newer := hlc.Timestamp{WallNs: 11}
	const first, second, third = "p:a", "p:a\x00", "p:b"
	if err := db.UpdateRows(ctx, func(g uint64, _ *tarp.Snapshot, b graphstore.Writer) error {
		for _, key := range []string{first, second, third} {
			if err := rows.PutVertex(b, g, key, graphstore.Vertex[string]{Value: key, HLC: old}); err != nil {
				return err
			}
		}
		return rows.PutFloor(b, g, graphstore.FloorKey[string]{Kind: graphstore.VertexBarrier, Key: first}, graphstore.Floor{HLC: newer})
	}); err != nil {
		t.Fatal(err)
	}
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	c.EnableStorageKeyPrefixIndex(func(key string) string { return key })
	view, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(c.RestoreStorage(ctx, rows, view), view.Close()); err != nil {
		t.Fatal(err)
	}
	// Leave only the durable causal floor to decide the first row.
	delete(c.vertexCausalBarriers, first)
	preflight := func(keys []string) {
		t.Helper()
		if !slices.Equal(keys, []string{second}) {
			t.Fatalf("preflight victims = %q, want %q", keys, second)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	defer cancel()
	if victims, err := c.DeleteByPrefixFromCommittedCut(cancelled, "p:", 2, incoming, expiration, func(keys []string) error {
		preflight(keys)
		cancel()
		return nil
	}); victims != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled prefix apply = %q, %v", victims, err)
	}
	if _, ok := c.GetVertex(second); !ok {
		t.Fatal("preflight cancellation deleted a Vertex")
	}
	victims, err := c.DeleteByPrefixFromCommittedCut(ctx, "p:", 2, incoming, expiration, func(keys []string) error {
		preflight(keys)
		return nil
	})
	if err != nil || !slices.Equal(victims, []string{second}) {
		t.Fatalf("prefix HLC victims = %q, %v", victims, err)
	}
	if err := db.UpdateRows(ctx, func(gen uint64, snap *tarp.Snapshot, w graphstore.Writer) error {
		return c.StoreRows(ctx, rows, snap, w, gen, StorageKeys[string]{Vertices: victims})
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.WithCommittedSnapshot(func(gen uint64, snap *tarp.Snapshot) error {
		for _, key := range []string{first, third} {
			if _, found, err := rows.GetVertexMetadata(snap, gen, key); !found || err != nil {
				t.Fatalf("retained %q: found=%t err=%v", key, found, err)
			}
		}
		floor, found, err := rows.GetFloor(snap, gen, graphstore.FloorKey[string]{Kind: graphstore.VertexTombstone, Key: second})
		if !found || err != nil || floor.HLC != incoming || !floor.Expiration.Equal(expiration) {
			t.Fatalf("deleted Vertex floor = %+v, found=%t err=%v", floor, found, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
