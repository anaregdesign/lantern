package graphcache

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
)

func TestMixedEdgeResetAddPermutations(t *testing.T) {
	live := time.Now().Add(time.Hour)
	type event struct {
		name  string
		apply func(*GraphCache[string, string])
	}
	ts := func(wall int64) hlc.Timestamp { return hlc.Timestamp{WallNs: wall, NodeID: hlc.NodeID{byte(wall)}} }
	add := func(wall int64, value float32, id byte) event {
		return event{fmt.Sprintf("Add%d", wall), func(c *GraphCache[string, string]) {
			c.AddEdgeWithExpirationContribHLC("tail", "head", value, live, ContribID{id}, ts(wall))
		}}
	}
	put := func(wall int64, value float32) event {
		return event{fmt.Sprintf("Put%d", wall), func(c *GraphCache[string, string]) {
			c.PutEdgeWithExpirationHLC("tail", "head", value, live, ts(wall))
		}}
	}
	deleteAt := func(wall int64) event {
		return event{fmt.Sprintf("Delete%d", wall), func(c *GraphCache[string, string]) {
			c.DeleteEdgeHLC("tail", "head", ts(wall), live)
		}}
	}
	for _, tc := range []struct {
		name   string
		events []event
		want   float32
	}{
		{"PutAdd", []event{put(20, 5), add(30, 1, 1), add(40, 2, 2)}, 8},
		{"DeleteAdd", []event{add(10, 7, 1), deleteAt(20), add(30, 3, 2)}, 3},
		{"PutDeleteAdd", []event{put(10, 5), add(20, 1, 1), deleteAt(30), add(40, 4, 2)}, 4},
		{"DeletePutAdd", []event{deleteAt(20), add(30, 3, 1), put(40, 9), add(50, 4, 2)}, 13},
	} {
		t.Run(tc.name, func(t *testing.T) {
			order := make([]int, len(tc.events))
			for i := range order {
				order[i] = i
			}
			var visit func(int)
			visit = func(start int) {
				if start == len(order) {
					c := NewGraphCache[string, string](time.Hour)
					for _, i := range order {
						tc.events[i].apply(c)
					}
					// Re-delivery after every reset must be idempotent too.
					for _, i := range order {
						tc.events[i].apply(c)
					}
					if got, ok := c.GetWeight("tail", "head"); !ok || got != tc.want {
						t.Errorf("order %v: weight=%g/%v, want %g", order, got, ok, tc.want)
					}
					c.flush()
					if got, ok := c.GetWeight("tail", "head"); !ok || got != tc.want {
						t.Errorf("order %v after GC: weight=%g/%v, want %g", order, got, ok, tc.want)
					}
					return
				}
				for i := start; i < len(order); i++ {
					order[start], order[i] = order[i], order[start]
					visit(start + 1)
					order[start], order[i] = order[i], order[start]
				}
			}
			visit(0)
		})
	}
}

func TestMixedEdgeResetAddRandomizedConvergence(t *testing.T) {
	// Every tape has one total HLC order. Delivery is shuffled independently;
	// duplicates exercise ContribID dedup and equal-HLC reset replay.
	rng := rand.New(rand.NewSource(1203))
	live := time.Now().Add(time.Hour)
	for tapeIndex := 0; tapeIndex < 80; tapeIndex++ {
		type event struct {
			kind   int // 0=Put, 1=Delete, 2=Add
			weight float32
			stamp  hlc.Timestamp
			id     ContribID
		}
		length := 3 + rng.Intn(5)
		events := make([]event, length)
		lastReset := -1
		for i := range events {
			events[i] = event{
				kind:   rng.Intn(3),
				weight: float32(1 + rng.Intn(7)),
				stamp:  hlc.Timestamp{WallNs: int64(i + 1), NodeID: hlc.NodeID{byte(tapeIndex + 1)}},
				id:     ContribID{byte(tapeIndex + 1), byte(i + 1)},
			}
			if events[i].kind != 2 {
				lastReset = i
			}
		}
		want := float32(0)
		present := false
		if lastReset >= 0 && events[lastReset].kind == 0 {
			want, present = events[lastReset].weight, true
		}
		for i, e := range events {
			if e.kind == 2 && i > lastReset {
				want += e.weight
				present = true
			}
		}
		for delivery := 0; delivery < 20; delivery++ {
			cache := NewGraphCache[string, string](time.Hour)
			order := rng.Perm(length)
			for _, index := range order {
				e := events[index]
				switch e.kind {
				case 0:
					cache.PutEdgeWithExpirationHLC("tail", "head", e.weight, live, e.stamp)
				case 1:
					cache.DeleteEdgeHLC("tail", "head", e.stamp, live)
				case 2:
					cache.AddEdgeWithExpirationContribHLC("tail", "head", e.weight, live, e.id, e.stamp)
				}
			}
			for _, index := range order {
				e := events[index]
				if e.kind == 2 {
					cache.AddEdgeWithExpirationContribHLC("tail", "head", e.weight, live, e.id, e.stamp)
				}
			}
			cache.flush()
			got, ok := cache.GetWeight("tail", "head")
			if ok != present || (present && got != want) {
				t.Fatalf("tape=%d delivery=%d order=%v: weight=%g/%v, want %g/%v; events=%+v", tapeIndex, delivery, order, got, ok, want, present, events)
			}
		}
	}
}

func TestStaleCausalAddDoesNotReviveEndpointBehindPutFloor(t *testing.T) {
	c := NewGraphCache[string, string](time.Hour)
	live := time.Now().Add(time.Hour)
	put := hlc.Timestamp{WallNs: 20, NodeID: hlc.NodeID{1}}
	staleAdd := hlc.Timestamp{WallNs: 10, NodeID: hlc.NodeID{1}}
	if !c.PutEdgeWithExpirationHLC("tail", "head", 5, live, put) {
		t.Fatal("Put did not establish an edge floor")
	}
	if err := c.PutVertexWithExpiration("tail", "", time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.GetVertex("tail"); ok {
		t.Fatal("test setup left tail live")
	}
	if c.AddEdgeWithExpirationContribHLC("tail", "head", 1, live, ContribID{1}, staleAdd) {
		t.Fatal("Add older than the Put floor was accepted")
	}
	if _, ok := c.GetVertex("tail"); ok {
		t.Fatal("fenced Add revived an absent endpoint vertex")
	}
}

func TestZeroSumCausalAddsSurviveGCAndReplay(t *testing.T) {
	c := NewGraphCache[string, string](time.Hour)
	live := time.Now().Add(time.Hour)
	first := hlc.Timestamp{WallNs: 10, NodeID: hlc.NodeID{1}}
	second := hlc.Timestamp{WallNs: 20, NodeID: hlc.NodeID{2}}
	c.AddEdgeWithExpirationContribHLC("tail", "head", 1, live, ContribID{1}, first)
	c.AddEdgeWithExpirationContribHLC("tail", "head", -1, live, ContribID{2}, second)
	if _, ok := c.GetWeight("tail", "head"); ok {
		t.Fatal("zero-sum edge should be hidden from reads")
	}
	c.flush()
	if c.edges.count() != 1 {
		t.Fatal("GC discarded live zero-sum ContribIDs")
	}
	snapshot := c.SnapshotReplication()
	if len(snapshot.Graph.Edges) != 1 || len(snapshot.Graph.Edges[0].Contributions) != 2 {
		t.Fatalf("zero-sum dedup evidence missing from replication Snapshot: %+v", snapshot.Graph.Edges)
	}
	restored := NewGraphCache[string, string](time.Hour)
	replayReplicationSnapshot(restored, snapshot)
	if restored.AddEdgeWithExpirationContribHLC("tail", "head", 1, live, ContribID{1}, first) {
		t.Fatal("duplicate Add was re-applied after Snapshot bootstrap")
	}
	if _, ok := restored.GetWeight("tail", "head"); ok {
		t.Fatal("Snapshot replay exposed a zero-sum edge")
	}
	if c.AddEdgeWithExpirationContribHLC("tail", "head", 1, live, ContribID{1}, first) {
		t.Fatal("duplicate Add was re-applied after GC")
	}
	if _, ok := c.GetWeight("tail", "head"); ok {
		t.Fatal("duplicate resurrected zero-sum edge")
	}
}

func TestCausalAddFloat32SumUsesCanonicalOrder(t *testing.T) {
	live := time.Now().Add(time.Hour)
	for _, order := range [][]int{{0, 1, 2}, {0, 2, 1}, {2, 1, 0}} {
		c := NewGraphCache[string, string](time.Hour)
		rows := []struct {
			weight float32
			id     ContribID
			hlc    hlc.Timestamp
		}{
			{16777216, ContribID{1}, hlc.Timestamp{WallNs: 10}},
			{1, ContribID{2}, hlc.Timestamp{WallNs: 20}},
			{-16777215, ContribID{3}, hlc.Timestamp{WallNs: 30}},
		}
		for _, i := range order {
			row := rows[i]
			c.AddEdgeWithExpirationContribHLC("tail", "head", row.weight, live, row.id, row.hlc)
		}
		if got, ok := c.GetWeight("tail", "head"); !ok || got != 1 {
			t.Errorf("order %v: canonical float32 sum = %g/%v, want 1/true", order, got, ok)
		}
	}
}

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

func TestZeroHLCAcceptedExpiredPutRetainsNoCausalBarrier(t *testing.T) {
	live := time.Now().Add(time.Hour)
	expired := time.Now().Add(-time.Hour)

	t.Run("vertex", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		if err := c.PutVertexWithExpiration("v", "old", live); err != nil {
			t.Fatal(err)
		}
		if !c.PutVertexWithExpirationHLC("v", "expired restore", expired, hlc.Timestamp{}) {
			t.Fatal("zero-HLC expired vertex Put was rejected")
		}
		if _, ok := c.GetVertex("v"); ok {
			t.Fatal("zero-HLC expired vertex Put did not remove the old value")
		}
		if vertices, edges := c.CausalBarrierCounts(); vertices != 0 || edges != 0 {
			t.Fatalf("barriers after zero-HLC expired vertex Put = %d/%d, want 0/0", vertices, edges)
		}
	})

	t.Run("edge", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		c.PutEdgeWithExpiration("tail", "head", 1, live)
		if !c.PutEdgeWithExpirationHLC("tail", "head", 2, expired, hlc.Timestamp{}) {
			t.Fatal("zero-HLC expired edge Put was rejected")
		}
		if _, _, ok := c.GetEdgeDetail("tail", "head"); ok {
			t.Fatal("zero-HLC expired edge Put did not remove the old bucket")
		}
		if vertices, edges := c.CausalBarrierCounts(); vertices != 0 || edges != 0 {
			t.Fatalf("barriers after zero-HLC expired edge Put = %d/%d, want 0/0", vertices, edges)
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

func TestPutVertexWithExpirationHLCRevalidatesReplacedSearchIndex(t *testing.T) {
	entered, resume := make(chan struct{}), make(chan struct{})
	first := true
	c := NewGraphCache[string, string](time.Hour)
	c.EnableSearchIndex(func(_ string, value string) search.Document {
		if first {
			first = false
			close(entered)
			<-resume
		}
		return search.Text(value)
	}, strings.Compare)
	result := make(chan bool, 1)
	go func() {
		result <- c.PutVertexWithExpirationHLC("k", "oversized", time.Now().Add(time.Hour), hlc.Timestamp{WallNs: 20})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("singular HLC writer did not reach out-of-lock projection")
	}
	replacement := newSearchIndex[string](true, search.SearchAnalysisLimits{MaxDocumentBytes: 4}, strings.Compare)
	c.mu.Lock()
	c.searchCommitMu.Lock()
	c.searchIndex = replacement
	c.searchCommitMu.Unlock()
	c.mu.Unlock()
	close(resume)
	select {
	case applied := <-result:
		if !applied {
			t.Fatal("replication apply rejected a causally admissible write")
		}
	case <-time.After(time.Second):
		t.Fatal("singular HLC writer did not retry against replacement index")
	}
	if got, ok := c.GetVertex("k"); !ok || got != "oversized" {
		t.Fatalf("replicated vertex = %q/%v, want oversized/true", got, ok)
	}
	if got := c.SearchIndexMemoryStats().Health; got != search.IndexIncomplete {
		t.Fatalf("replacement index health = %q, want incomplete after analysis rejection", got)
	}
}
