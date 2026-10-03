package graphcache

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestQueryEdgeFiltersDirectedIntersectionAndOwnership(t *testing.T) {
	all := []KeyRange{{Lower: "data:", Upper: "data;"}}
	pair := func(tail, head string) KeyPairRange {
		return KeyPairRange{Tail: KeyRange{Lower: tail, Upper: binaryPrefixEnd(tail)}, Head: KeyRange{Lower: head, Upper: binaryPrefixEnd(head)}}
	}
	filters := []EdgeRangeFilter{{Included: []KeyPairRange{pair("data:a:", "data:b:"), pair("data:c:", "data:d:")}, Excluded: []KeyPairRange{pair("data:a:private:", "data:b:")}}, {Endpoints: all}}
	view, err := NewQueryViewWithEdgeFilters(all, all, filters)
	if err != nil {
		t.Fatal(err)
	}
	filters[0].Included[0] = pair("data:", "data:")
	filters[0].Excluded = nil
	all[0].Lower = "wrong"
	for _, tc := range []struct {
		tail, head string
		want       bool
	}{
		{"data:a:1", "data:b:1", true}, {"data:b:1", "data:a:1", false},
		{"data:a:1", "data:d:1", false}, {"data:c:1", "data:d:1", true},
		{"data:a:private:1", "data:b:1", false}, {"sys:a", "data:b:1", false},
	} {
		if got := view.Edge(tc.tail, tc.head); got != tc.want {
			t.Fatalf("%+v got %t", tc, got)
		}
	}
	empty, err := NewQueryViewWithEdgeFilters([]KeyRange{{}}, []KeyRange{{}}, []EdgeRangeFilter{{}})
	if err != nil || empty.Edge("a", "b") {
		t.Fatal("empty inclusion broadened constraints")
	}
}

func TestQueryEdgeFiltersScanPageDeleteAndSnapshot(t *testing.T) {
	for _, indexed := range []bool{false, true} {
		c := NewGraphCache[string, string](time.Hour)
		c.EnablePrefixIndex(identityExtract)
		if !indexed {
			c.disableHeadIndexForTesting()
		}
		for _, pair := range [][2]string{{"a:1", "b:1"}, {"a:1", "b:2"}, {"a:1", "b:private:1"}, {"b:1", "a:1"}, {"a:1", "a:2"}} {
			c.AddEdge(pair[0], pair[1], 1)
		}
		all := []KeyRange{{}}
		view, err := NewQueryViewWithEdgeFilters(all, all, []EdgeRangeFilter{{Included: []KeyPairRange{{Tail: KeyRange{Lower: "a:", Upper: "a;"}, Head: KeyRange{Lower: "b:", Upper: "b;"}}}, Excluded: []KeyPairRange{{Tail: KeyRange{Lower: "a:", Upper: "a;"}, Head: KeyRange{Lower: "b:private:", Upper: "b:private;"}}}}})
		if err != nil {
			t.Fatal(err)
		}
		ctx := WithQueryView(context.Background(), view)
		var rows [][2]string
		afterTail, afterHead := "", ""
		for {
			more, ok := c.ScanEdgesByPrefixPage(ctx, "", "", afterTail, afterHead, 1, func(_, tail, _, head string, _ float32, _ time.Time) bool {
				rows = append(rows, [2]string{tail, head})
				afterTail, afterHead = tail, head
				return true
			})
			if !ok {
				t.Fatal("pair scan incomplete", indexed)
			}
			if !more {
				break
			}
		}
		if !reflect.DeepEqual(rows, [][2]string{{"a:1", "b:1"}, {"a:1", "b:2"}}) {
			t.Fatal("pair scan/page exposed reversed or denied edges", indexed, rows)
		}
		snapshot, err := c.SnapshotGraphContext(ctx)
		if err != nil || len(snapshot.Edges) != 2 {
			t.Fatal("pair snapshot bypass", indexed, snapshot, err)
		}
		if count := c.DeleteEdgesByPrefix(ctx, "a:", "", 0); count != 2 {
			t.Fatal("pair delete selected wrong victims", indexed, count)
		}
		if _, live := c.GetWeight("b:1", "a:1"); !live {
			t.Fatal("pair delete removed reversed edge")
		}
		if _, live := c.GetWeight("a:1", "b:private:1"); !live {
			t.Fatal("pair delete removed denied edge")
		}
	}
}
func TestQueryEdgeFiltersRejectUnboundedOrInvalidInput(t *testing.T) {
	for _, filters := range [][]EdgeRangeFilter{
		make([]EdgeRangeFilter, 17),
		{{Included: []KeyPairRange{{Tail: KeyRange{Lower: "z", Upper: "a"}}}}},
		{{Endpoints: []KeyRange{{Lower: "a", Upper: "z"}, {Lower: "b", Upper: "c"}}}},
		{{Included: make([]KeyPairRange, 8193)}},
	} {
		if _, err := NewQueryViewWithEdgeFilters(nil, nil, filters); err == nil {
			t.Fatal("invalid range set accepted")
		}
	}
}
