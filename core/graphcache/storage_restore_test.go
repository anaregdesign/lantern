package graphcache

import (
	"errors"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestRestoreStorageRejectsUnownedSources(t *testing.T) {
	for _, tail := range []string{"a", "m", "z"} {
		t.Run(tail, func(t *testing.T) {
			db, err := graphstore.Open(t.Context(), t.TempDir(), hlc.NodeID{1})
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			rows, err := graphstore.NewRows[string, string](tarp.StringCodec{}, tarp.StringCodec{})
			if err != nil {
				t.Fatal(err)
			}
			key := graphstore.EdgeKey[string]{Tail: "m", Head: "h"}
			if err := db.UpdateRows(t.Context(), func(g uint64, _ *tarp.Snapshot, b graphstore.Writer) error {
				if err := rows.PutEdge(b, g, key, graphstore.Edge{LastOrdinal: 1}); err != nil {
					return err
				}
				// a/z have no header. m has a header but its local ordinal
				// exceeds the high-water mark even though the Add expired.
				sourceKey := graphstore.EdgeKey[string]{Tail: tail, Head: "h"}
				return rows.PutSource(b, g, sourceKey, graphstore.SourceID{Ordinal: 2}, graphstore.Source{Expiration: time.Unix(1, 0)})
			}); err != nil {
				t.Fatal(err)
			}
			view, err := db.Read()
			if err != nil {
				t.Fatal(err)
			}
			defer view.Close()
			c := NewGraphCacheWithStaging[string, string](time.Hour)
			if err := c.RestoreStorage(t.Context(), rows, view); !errors.Is(err, graphstore.ErrInvalidRow) {
				t.Fatalf("restore = %v, want invalid row", err)
			}
		})
	}
}
