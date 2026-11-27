package graphcache

import (
	"errors"
	"fmt"
	"maps"
	"strings"
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
			put("anchor", "v:missing", -100), put(smallest, smallest, 3), put(other, smallest, -4),
			rows.MarkIncomingEdgeIndex(w, gen))
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
	degrees, err := reader.Degree(ctx, "v:", 3, DegreeOut, false)
	if err != nil || len(degrees) != 3 {
		t.Fatalf("degrees = %v, %v", degrees, err)
	}
	for _, d := range degrees {
		if d.Key == smallest {
			if d.Degree != 1 || d.WeightedDegree != 3 {
				t.Fatalf("OUT self-loop degree = %+v", d)
			}
		} else if d.Key == other {
			if d.Degree != 1 || d.WeightedDegree != -4 {
				t.Fatalf("OUT negative degree = %+v", d)
			}
		} else if d.Degree != 0 || d.WeightedDegree != 0 {
			t.Fatalf("isolated degree = %+v", d)
		}
	}
	reader = NewStoredTraversal(rows, view, now, scope, PPRWorkBudget{})
	in, err := reader.Degree(ctx, smallest, 1, DegreeIn, true)
	if err != nil || len(in) != 1 || in[0].Degree != 3 || in[0].WeightedDegree != -3 {
		t.Fatalf("IN degree = %v, %v", in, err)
	}
	reader = NewStoredTraversal(rows, view, now, scope, PPRWorkBudget{})
	both, err := reader.Degree(ctx, smallest, 1, DegreeBoth, false)
	if err != nil || len(both) != 1 || both[0].Degree != 4 || both[0].WeightedDegree != 0 {
		t.Fatalf("BOTH degree (self-loop contributes twice) = %v, %v", both, err)
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
	if err := reader.PrepareWeighting(WeightingBM25); !errors.Is(err, graphstore.ErrInvalidRow) {
		t.Fatalf("fresh generation without topology stats = %v", err)
	}
	if got, err := reader.Degree(ctx, "v:", 1, DegreeIn, false); got != nil || !errors.Is(err, graphstore.ErrInvalidRow) {
		t.Fatalf("fresh generation without incoming projection = %v, %v", got, err)
	}
	if g, _, err := reader.Neighbor(ctx, "anchor", "v:", 1, 1, false); err != nil || len(g.Vertices) != 0 {
		t.Fatalf("new generation = %v, %v", g, err)
	}
}

func TestStoredTraversalMatchesProtectedRAM(t *testing.T) {
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
	ram := NewGraphCache[string, string](0)
	now := time.Now()
	if err := ram.PutVertexWithExpiration("origin", "seed", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateRows(ctx, func(gen uint64, _ *tarp.Snapshot, writer graphstore.Writer) error {
		if err := rows.PutVertex(writer, gen, "origin", graphstore.Vertex[string]{Value: "seed"}); err != nil {
			return err
		}
		// More neighbors than the stored PPR's normalization buffer.
		for i := range 40 {
			key := fmt.Sprintf("head:%02d", i)
			weight := float32(i%3 + 1)
			if err := ram.PutVertexWithExpiration(key, key, time.Time{}); err != nil {
				return err
			}
			ram.PutEdgeWithExpiration("origin", key, weight, time.Time{})
			ram.PutEdgeWithExpiration(key, "origin", 1, time.Time{})
			if err := rows.PutVertex(writer, gen, key, graphstore.Vertex[string]{Value: key}); err != nil {
				return err
			}
			if err := rows.PutEdge(writer, gen, graphstore.EdgeKey[string]{Tail: "origin", Head: key},
				graphstore.Edge{Base: &graphstore.Source{Weight: weight}}); err != nil {
				return err
			}
			if err := rows.PutEdge(writer, gen, graphstore.EdgeKey[string]{Tail: key, Head: "origin"},
				graphstore.Edge{Base: &graphstore.Source{Weight: 1}}); err != nil {
				return err
			}
		}
		// Retained edges count toward the corpus even when an endpoint is
		// outside the admitted view, missing, or the edge source has expired.
		for _, edge := range []struct {
			tail, head string
			deadline   time.Time
		}{
			{"hidden", "head:00", time.Time{}},
			{"missing", "head:01", time.Time{}},
			{"head:00", "head:01", now.Add(-time.Hour)},
		} {
			ram.edges.addWithExpirationContribAt(edge.tail, edge.head, 11, edge.deadline, ContribID{}, now.Add(-2*time.Hour))
			if err := rows.PutEdge(writer, gen, graphstore.EdgeKey[string]{Tail: edge.tail, Head: edge.head},
				graphstore.Edge{Base: &graphstore.Source{Weight: 11, Expiration: edge.deadline}}); err != nil {
				return err
			}
		}
		if err := ram.PutVertexWithExpiration("hidden", "hidden", time.Time{}); err != nil {
			return err
		}
		if err := rows.PutVertex(writer, gen, "hidden", graphstore.Vertex[string]{Value: "hidden"}); err != nil {
			return err
		}
		ram.AddEdgeWithExpiration("head:01", "head:02", 1, time.Time{})
		ram.AddEdgeWithExpiration("head:01", "head:02", -1, time.Time{})
		zero := graphstore.EdgeKey[string]{Tail: "head:01", Head: "head:02"}
		if err := rows.PutEdge(writer, gen, zero, graphstore.Edge{LastOrdinal: 1, Base: &graphstore.Source{Weight: 1}}); err != nil {
			return err
		}
		if err := rows.PutSource(writer, gen, zero, graphstore.SourceID{Ordinal: 1}, graphstore.Source{Weight: -1}); err != nil {
			return err
		}
		return rows.MarkIncomingEdgeIndex(writer, gen)
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureTopologyStats(ctx); err != nil {
		t.Fatal(err)
	}
	view, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	admitted := []KeyRange{{Lower: "head:", Upper: "head;"}, {Lower: "origin", Upper: "origin\x00"}}
	scope, err := NewQueryView(admitted, admitted)
	if err != nil {
		t.Fatal(err)
	}
	ctx = WithQueryView(ctx, scope)
	keep := func(key string) bool { return strings.HasPrefix(key, "head:") }
	for _, tc := range []struct {
		name      string
		weighting EdgeWeighting
	}{{"RAW", WeightingRaw}, {"TFIDF", WeightingTFIDF}, {"BM25", WeightingBM25}} {
		t.Run(tc.name, func(t *testing.T) {
			newReader := func() *StoredTraversal[string] {
				reader := NewStoredTraversal(rows, view, now, scope, PPRWorkBudget{})
				if err := reader.PrepareWeighting(tc.weighting); err != nil {
					t.Fatal(err)
				}
				return reader
			}
			want, wantExpires, err := ram.NeighborWithExpirationsContext(ctx, "origin", 2, 10, tc.weighting, false, keep)
			if err != nil {
				t.Fatal(err)
			}
			// The public response contains only actual Edge records.
			maps.DeleteFunc(want.Edges, func(_ string, row map[string]float32) bool { return len(row) == 0 })
			maps.DeleteFunc(wantExpires, func(_ string, row map[string]time.Time) bool { return len(row) == 0 })
			got, gotExpires, err := newReader().Neighbor(ctx, "origin", "head:", 2, 10, false)
			if err != nil || !maps.Equal(want.Vertices, got.Vertices) || !maps.EqualFunc(want.Edges, got.Edges, maps.Equal) ||
				!maps.EqualFunc(wantExpires, gotExpires, func(a, b map[string]time.Time) bool { return maps.EqualFunc(a, b, time.Time.Equal) }) {
				t.Fatalf("stored BFS=%v %v err=%v, want %v %v", got, gotExpires, err, want, wantExpires)
			}
			want, err = ram.PersonalizedPageRankContext(ctx, "origin", 10, DefaultPPRAlpha, DefaultPPREpsilon, tc.weighting, keep)
			if err != nil {
				t.Fatal(err)
			}
			got, err = newReader().PageRank(ctx, "origin", "head:", 10, DefaultPPRAlpha, DefaultPPREpsilon)
			if err != nil || !maps.Equal(want.Vertices, got.Vertices) || !maps.Equal(want.Edges["origin"], got.Edges["origin"]) {
				t.Fatalf("stored PageRank=%v err=%v, want %v", got, err, want)
			}
			want, wantExpires, err = ram.LocalCommunityContext(ctx, "origin", 10, DefaultPPRAlpha, DefaultPPREpsilon, tc.weighting, keep)
			if err != nil {
				t.Fatal(err)
			}
			got, gotExpires, err = newReader().Community(ctx, "origin", "head:", 10, DefaultPPRAlpha, DefaultPPREpsilon)
			if err != nil || !maps.Equal(want.Vertices, got.Vertices) || !maps.EqualFunc(want.Edges, got.Edges, maps.Equal) ||
				!maps.EqualFunc(wantExpires, gotExpires, func(a, b map[string]time.Time) bool { return maps.EqualFunc(a, b, time.Time.Equal) }) {
				t.Fatalf("stored Community=%v %v err=%v, want %v %v", got, gotExpires, err, want, wantExpires)
			}
			for key := range got.Vertices {
				if key != "origin" && got.Edges[key]["origin"] <= 0 {
					t.Fatalf("induced community omitted the edge from %s to the prefix-exempt seed", key)
				}
			}
		})
	}
}
