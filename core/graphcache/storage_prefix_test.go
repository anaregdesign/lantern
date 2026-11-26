package graphcache

import (
	"errors"
	"reflect"
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
