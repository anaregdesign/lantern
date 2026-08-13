package graphcache

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
)

func TestAcceptedExpiredPutCausalBarrierSurvivesGC(t *testing.T) {
	newer := hlc.Timestamp{WallNs: 20, NodeID: hlc.NodeID{0x20}}
	older := hlc.Timestamp{WallNs: 10, NodeID: hlc.NodeID{0x10}}
	live := time.Now().Add(time.Hour)
	expired := time.Now().Add(-time.Hour)

	t.Run("vertex", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		if !c.PutVertexWithExpirationHLC("v", "expired", expired, newer) {
			t.Fatal("accepted-expired HLC20 vertex Put was rejected")
		}
		if _, ok := c.GetVertex("v"); ok || c.vertices.Count() != 0 {
			t.Fatalf("accepted-expired vertex was materialized: found=%v physical=%d", ok, c.vertices.Count())
		}

		c.flush()
		if got := c.vertexCausalBarriers["v"]; got != newer {
			t.Fatalf("barrier after GC = %+v, want %+v", got, newer)
		}
		if c.PutVertexWithExpirationHLC("v", "older live", live, older) {
			t.Fatal("cross-origin HLC10 live vertex resurrected HLC20 accepted-expired identity")
		}
		if _, ok := c.GetVertex("v"); ok {
			t.Fatal("older vertex became visible after GC")
		}

		barriers := c.SnapshotCausalBarriers()
		if len(barriers.Vertices) != 1 || barriers.Vertices[0].Key != "v" || barriers.Vertices[0].HLC != newer {
			t.Fatalf("vertex barrier snapshot = %+v", barriers.Vertices)
		}
		if !c.PutVertexWithExpirationHLC("v", "newer live", live, hlc.Timestamp{WallNs: 30}) {
			t.Fatal("HLC30 live vertex did not supersede HLC20 barrier")
		}
		if vertices, _ := c.CausalBarrierCounts(); vertices != 0 {
			t.Fatalf("vertex barrier count after newer live Put = %d, want 0", vertices)
		}
	})

	t.Run("edge", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		if !c.PutEdgeWithExpirationHLC("tail", "head", 2, expired, newer) {
			t.Fatal("accepted-expired HLC20 edge Put was rejected")
		}
		if _, _, ok := c.GetEdgeDetail("tail", "head"); ok || c.edges.count() != 0 {
			t.Fatalf("accepted-expired edge was materialized: found=%v physical=%d", ok, c.edges.count())
		}
		if _, ok := c.GetVertex("tail"); ok {
			t.Fatal("accepted-expired edge materialized tail endpoint")
		}
		if _, ok := c.GetVertex("head"); ok {
			t.Fatal("accepted-expired edge materialized head endpoint")
		}

		c.flush()
		key := EdgeKey[string]{Tail: "tail", Head: "head"}
		if got := c.edgeCausalBarriers[key]; got != newer {
			t.Fatalf("barrier after GC = %+v, want %+v", got, newer)
		}
		if c.PutEdgeWithExpirationHLC("tail", "head", 9, live, older) {
			t.Fatal("cross-origin HLC10 live edge resurrected HLC20 accepted-expired identity")
		}
		if _, _, ok := c.GetEdgeDetail("tail", "head"); ok {
			t.Fatal("older edge became visible after GC")
		}
		if _, ok := c.GetVertex("tail"); ok {
			t.Fatal("rejected older edge materialized endpoints")
		}

		barriers := c.SnapshotCausalBarriers()
		if len(barriers.Edges) != 1 || barriers.Edges[0].Tail != "tail" || barriers.Edges[0].Head != "head" || barriers.Edges[0].HLC != newer {
			t.Fatalf("edge barrier snapshot = %+v", barriers.Edges)
		}
		if !c.PutEdgeWithExpirationHLC("tail", "head", 4, live, hlc.Timestamp{WallNs: 30}) {
			t.Fatal("HLC30 live edge did not supersede HLC20 barrier")
		}
		if _, edges := c.CausalBarrierCounts(); edges != 0 {
			t.Fatalf("edge barrier count after newer live Put = %d, want 0", edges)
		}
	})
}

func TestAcceptedExpiredEdgeBarrierSurvivesNewerAdd(t *testing.T) {
	barrierTS := hlc.Timestamp{WallNs: 20, NodeID: hlc.NodeID{0x20}}
	addTS := hlc.Timestamp{WallNs: 30, NodeID: hlc.NodeID{0x30}}
	olderPutTS := hlc.Timestamp{WallNs: 10, NodeID: hlc.NodeID{0x10}}
	c := NewGraphCache[string, string](time.Hour)

	if !c.PutEdgeWithExpirationHLC("tail", "head", 1, time.Now().Add(-time.Hour), barrierTS) {
		t.Fatal("accepted-expired HLC20 Put was rejected")
	}
	if !c.AddEdgeWithExpirationContribHLC(
		"tail", "head", 2, time.Now().Add(time.Hour), ContribID{1}, addTS,
	) {
		t.Fatal("newer HLC30 Add was rejected")
	}
	if _, edges := c.CausalBarrierCounts(); edges != 1 {
		t.Fatalf("barrier count after Add = %d, want 1", edges)
	}
	if c.PutEdgeWithExpirationHLC("tail", "head", 9, time.Now().Add(time.Hour), olderPutTS) {
		t.Fatal("HLC10 Put reset edge after HLC20 barrier and HLC30 Add")
	}
	if got, ok := c.GetWeight("tail", "head"); !ok || got != 2 {
		t.Fatalf("edge after rejected older Put = %v/%v, want 2/true", got, ok)
	}
}

func TestExactDeleteWithoutTombstoneReclaimsCausalBarrier(t *testing.T) {
	ts := hlc.Timestamp{WallNs: 20}
	expired := time.Now().Add(-time.Hour)
	c := NewGraphCache[string, string](time.Hour)
	if !c.PutVertexWithExpirationHLC("v", "dead", expired, ts) {
		t.Fatal("vertex barrier Put rejected")
	}
	if !c.PutEdgeWithExpirationHLC("tail", "head", 1, expired, ts) {
		t.Fatal("edge barrier Put rejected")
	}
	if vertices, edges := c.CausalBarrierCounts(); vertices != 1 || edges != 1 {
		t.Fatalf("barriers before Delete = %d/%d, want 1/1", vertices, edges)
	}
	if deleted := c.DeleteVertices([]string{"v"}); deleted != 0 {
		t.Fatalf("DeleteVertices deleted = %d, want 0 absent payloads", deleted)
	}
	if deleted := c.DeleteEdges([]EdgeKey[string]{{Tail: "tail", Head: "head"}}); deleted != 0 {
		t.Fatalf("DeleteEdges deleted = %d, want 0 absent buckets", deleted)
	}
	if vertices, edges := c.CausalBarrierCounts(); vertices != 0 || edges != 0 {
		t.Fatalf("barriers after no-tombstone Delete = %d/%d, want 0/0", vertices, edges)
	}
}

func TestApplyVertexCausalBarrierKeepsSearchRecoveryFailClosed(t *testing.T) {
	c := NewGraphCache[string, string](time.Hour)
	c.EnableSearchIndex(
		func(_ string, value string) search.Document { return search.Text(value) },
		strings.Compare,
	)
	if err := c.PutVertexWithExpiration("v", "retiredterm", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	c.BeginSearchIndexRecovery()
	if !c.ApplyVertexCausalBarrierHLC("v", hlc.Timestamp{WallNs: 20}) {
		t.Fatal("causal barrier was rejected")
	}
	if got := c.searchIndex.Health(); got != search.IndexIncomplete {
		t.Fatalf("search health during snapshot recovery = %v, want incomplete", got)
	}
	if err := c.CompleteSearchIndexRecovery(); err != nil {
		t.Fatal(err)
	}
	if got := c.SearchVertices("retiredterm", 10, ""); len(got) != 0 {
		t.Fatalf("barrier-deleted vertex remained searchable: %v", got)
	}
}

func TestPutVertexWithExpirationHLCSearchClockRollback(t *testing.T) {
	realNow := time.Now()
	expiration := realNow.Add(-time.Minute)
	applicationTime := realNow.Add(-2 * time.Minute)
	c := NewGraphCache[string, string](time.Hour)
	c.EnableSearchIndex(
		func(_ string, value string) search.Document { return search.Text(value) },
		strings.Compare,
	)
	c.applicationClock = func() time.Time { return applicationTime }

	if !c.PutVertexWithExpirationHLC("v", "singularrollbackterm", expiration, hlc.Timestamp{WallNs: 20}) {
		t.Fatal("PutVertexWithExpirationHLC was rejected")
	}
	if got, ok := c.vertices.GetAt("v", applicationTime); !ok || got != "singularrollbackterm" {
		t.Fatalf("vertex storage at rolled-back application time = %q/%v", got, ok)
	}
	results, _, err := c.searchIndex.SearchMatchTopKContextAt(
		context.Background(), "singularrollbackterm", 10, nil,
		search.MatchOptions{}, search.Budget{}, applicationTime,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "v" {
		t.Fatalf("SearchVertices = %v, want v", results)
	}
}
