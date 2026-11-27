package graphstore

import (
	"errors"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestEdgeRecoverySummariesRejectsDivergentProjection(t *testing.T) {
	for _, defect := range []string{"missing Source", "extra Source", "orphan Source", "ordinal high-water", "nonempty locator"} {
		t.Run(defect, func(t *testing.T) {
			ctx := t.Context()
			db, err := Open(ctx, t.TempDir(), hlc.NodeID{1})
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			rows, err := NewRows[string, string](tarp.StringCodec{}, tarp.StringCodec{})
			if err != nil {
				t.Fatal(err)
			}
			key := EdgeKey[string]{Tail: "a", Head: "b"}
			id := SourceID{Ordinal: 1}
			source := Source{Weight: 7}
			if err := db.UpdateRows(ctx, func(gen uint64, _ *tarp.Snapshot, b Writer) error {
				if defect != "orphan Source" {
					var edge Edge
					if defect != "ordinal high-water" {
						edge.LastOrdinal = 1
					}
					if err := rows.PutEdge(b, gen, key, edge); err != nil {
						return err
					}
				}
				if defect != "extra Source" {
					if err := rows.PutSource(b, gen, key, id, source); err != nil {
						return err
					}
				}
				if defect != "missing Source" {
					pair, err := rows.edgeTuple(gen, sourceFamily, key)
					if err != nil {
						return err
					}
					order, err := sourceOrderKey(pair, source.HLC, id)
					if err != nil {
						return err
					}
					var value []byte
					if defect == "nonempty locator" {
						value = []byte{1}
					}
					b.Put(order, value)
				}
				return rows.MarkSourceOrder(b, gen)
			}); err != nil {
				t.Fatal(err)
			}
			view, err := db.Read()
			if err != nil {
				t.Fatal(err)
			}
			defer view.Close()
			var got error
			for _, err := range rows.EdgeRecoverySummaries(ctx, view.Snapshot, view.Generation, time.Unix(100, 0)) {
				if err != nil {
					got = err
				}
			}
			if !errors.Is(got, ErrInvalidRow) {
				t.Fatalf("recovery accepted %s: %v", defect, got)
			}
		})
	}
}
