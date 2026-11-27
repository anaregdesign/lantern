package graphcache

import (
	"errors"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestStoredTraversalPinnedCutAndAdmission(t *testing.T) {
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
	now := time.Unix(100, 0)
	deadline := now.Add(time.Minute)
	const smallest, other, denied = "v:ä\x00x", "v:東京", "v:hidden"
	if err := db.UpdateRows(ctx, func(gen uint64, _ *tarp.Snapshot, w graphstore.Writer) error {
		for _, key := range []string{"anchor", smallest, other, denied, "v:expired", "v:isolated"} {
			expiration := time.Time{}
			if key == "v:expired" {
				expiration = now
			}
			if err := rows.PutVertex(w, gen, key, graphstore.Vertex[string]{Value: key, Expiration: expiration}); err != nil {
				return err
			}
		}
		put := func(tail, head string, weight float32) error {
			return rows.PutEdge(w, gen, graphstore.EdgeKey[string]{Tail: tail, Head: head},
				graphstore.Edge{Base: &graphstore.Source{Weight: weight, Expiration: deadline}})
		}
		return errors.Join(put("anchor", smallest, -2), put("anchor", other, -2),
			put("anchor", denied, -100), put("anchor", "v:expired", -100),
			put("anchor", "v:missing", -100), put(smallest, smallest, 3))
	}); err != nil {
		t.Fatal(err)
	}
	view, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	admitted := []KeyRange{{Lower: "anchor", Upper: "anchor\x00"}, {Lower: "v:expired", Upper: "v:expired\x00"},
		{Lower: "v:isolated", Upper: "w"}}
	scope, err := NewQueryView(admitted, admitted)
	if err != nil {
		t.Fatal(err)
	}

	stage, err := db.BeginGeneration()
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Abort()
	if err := stage.Publish(ctx, func(_, _ uint64, _ *tarp.Snapshot) error { return nil }); err != nil {
		t.Fatal(err)
	}

	reader := NewStoredTraversal(rows, view, now, scope, PPRWorkBudget{})
	g, expires, err := reader.Neighbor(ctx, "anchor", "v:", 2, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if w, ok := g.Edges["anchor"][smallest]; !ok || w != -2 || !expires["anchor"][smallest].Equal(deadline) {
		t.Fatalf("stable top-k from the old cut = %v, %v", g.Edges, expires)
	}
	if len(g.Vertices) != 2 || g.Vertices[smallest] != smallest || g.Edges[smallest][smallest] != 3 {
		t.Fatalf("result after the second hop = %+v", g)
	}

	reader = NewStoredTraversal(rows, view, now, scope, PPRWorkBudget{})
	degrees, err := reader.OutDegree(ctx, "v:", 3, false)
	if err != nil || len(degrees) != 3 {
		t.Fatalf("degrees = %v, %v", degrees, err)
	}
	for _, d := range degrees {
		if d.Key == smallest {
			if d.Degree != 1 || d.WeightedDegree != 3 {
				t.Fatalf("OUT self-loop degree = %+v", d)
			}
		} else if d.Degree != 0 || d.WeightedDegree != 0 {
			t.Fatalf("isolated degree = %+v", d)
		}
	}

	reader = NewStoredTraversal(rows, view, now, scope, PPRWorkBudget{MaxTouchedEdges: 3})
	if g, _, err := reader.Neighbor(ctx, "anchor", "v:", 1, 1, false); !errors.Is(err, ErrStoredTraversalBudgetExceeded) || g != nil {
		t.Fatalf("budget exhaustion = %v, %v", g, err)
	}
	fresh, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	reader = NewStoredTraversal(rows, fresh, now, scope, PPRWorkBudget{})
	if g, _, err := reader.Neighbor(ctx, "anchor", "v:", 1, 1, false); err != nil || len(g.Vertices) != 0 {
		t.Fatalf("new generation = %v, %v", g, err)
	}
}
