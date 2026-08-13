package graphcache

import (
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

// vertexByKey indexes a vertex snapshot by key for order-independent
// comparison (Range / rangeBuckets iteration order is unspecified).
func vertexByKey(vs []SnapshotVertex[string, string]) map[string]SnapshotVertex[string, string] {
	m := make(map[string]SnapshotVertex[string, string], len(vs))
	for _, v := range vs {
		m[v.Key] = v
	}
	return m
}

// edgeByEndpoints indexes an edge snapshot by (tail, head).
func edgeByEndpoints(es []SnapshotEdge[string]) map[EdgeKey[string]]SnapshotEdge[string] {
	m := make(map[EdgeKey[string]]SnapshotEdge[string], len(es))
	for _, e := range es {
		m[EdgeKey[string]{Tail: e.Tail, Head: e.Head}] = e
	}
	return m
}

func replayReplicationSnapshot(dst *GraphCache[string, string], snapshot ReplicationSnapshot[string, string]) {
	for _, barrier := range snapshot.Barriers.Vertices {
		dst.ApplyVertexCausalBarrierHLC(barrier.Key, barrier.HLC)
	}
	for _, barrier := range snapshot.Barriers.Edges {
		dst.ApplyEdgeCausalBarrierHLC(barrier.Tail, barrier.Head, barrier.HLC)
	}
	for _, vertex := range snapshot.Graph.Vertices {
		dst.PutVertexWithExpirationHLC(vertex.Key, vertex.Value, vertex.Expiration, vertex.HLC)
	}
	for _, edge := range snapshot.Graph.Edges {
		for _, contribution := range edge.Contributions {
			if contribution.ContribID.IsZero() {
				dst.PutEdgeWithExpirationHLC(edge.Tail, edge.Head, contribution.Weight, contribution.Expiration, edge.HLC)
				continue
			}
			dst.AddEdgeWithExpirationContribHLC(edge.Tail, edge.Head, contribution.Weight, contribution.Expiration, contribution.ContribID, edge.HLC)
		}
	}
}

func TestGraphCache_SnapshotGraph(t *testing.T) {
	// SnapshotGraph must return exactly what SnapshotVertices and
	// SnapshotEdges return — it is the same materialise logic taken under a
	// single lock instead of two.
	t.Run("MatchesSingleMethods", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		future := time.Now().Add(time.Hour)
		c.PutVerticesWithExpiration([]VertexItem[string, string]{
			{Key: "a", Value: "A", Expiration: future},
			{Key: "b", Value: "B", Expiration: future},
			{Key: "c", Value: "C", Expiration: future},
		})
		c.PutEdgesWithExpiration([]EdgeItem[string]{
			{Tail: "a", Head: "b", Weight: 1.5, Expiration: future},
			{Tail: "b", Head: "c", Weight: 2, Expiration: future},
		})

		got := c.SnapshotGraph()
		wantV := c.SnapshotVertices()
		wantE := c.SnapshotEdges()

		if !reflect.DeepEqual(vertexByKey(got.Vertices), vertexByKey(wantV)) {
			t.Errorf("SnapshotGraph().Vertices = %v, want (SnapshotVertices) %v", got.Vertices, wantV)
		}
		if !reflect.DeepEqual(edgeByEndpoints(got.Edges), edgeByEndpoints(wantE)) {
			t.Errorf("SnapshotGraph().Edges = %v, want (SnapshotEdges) %v", got.Edges, wantE)
		}
		if len(got.Vertices) != 3 {
			t.Errorf("got %d vertices, want 3", len(got.Vertices))
		}
		if len(got.Edges) != 2 {
			t.Errorf("got %d edges, want 2", len(got.Edges))
		}
	})

	t.Run("SkipsExpiredVerticesAndEdges", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		now := time.Now()
		future := now.Add(time.Hour)
		past := now.Add(-time.Hour)
		c.PutVerticesWithExpiration([]VertexItem[string, string]{
			{Key: "a", Value: "A", Expiration: future},
			{Key: "b", Value: "B", Expiration: future},
			{Key: "gone", Value: "X", Expiration: past},
		})
		c.PutEdgesWithExpiration([]EdgeItem[string]{
			{Tail: "a", Head: "b", Weight: 1, Expiration: future},
			{Tail: "b", Head: "gone", Weight: 1, Expiration: past},
		})

		got := c.SnapshotGraph()

		vs := vertexByKey(got.Vertices)
		if _, ok := vs["gone"]; ok {
			t.Errorf("expired vertex 'gone' present in snapshot: %v", got.Vertices)
		}
		if _, ok := vs["a"]; !ok {
			t.Errorf("live vertex 'a' missing from snapshot: %v", got.Vertices)
		}
		if len(got.Vertices) != 2 {
			t.Errorf("got %d vertices, want 2 (a,b)", len(got.Vertices))
		}

		es := edgeByEndpoints(got.Edges)
		if _, ok := es[EdgeKey[string]{Tail: "b", Head: "gone"}]; ok {
			t.Errorf("expired edge b->gone present in snapshot: %v", got.Edges)
		}
		if _, ok := es[EdgeKey[string]{Tail: "a", Head: "b"}]; !ok {
			t.Errorf("live edge a->b missing from snapshot: %v", got.Edges)
		}
		if len(got.Edges) != 1 {
			t.Errorf("got %d edges, want 1 (a->b)", len(got.Edges))
		}
	})

	t.Run("EmptyCache", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		got := c.SnapshotGraph()
		if len(got.Vertices) != 0 {
			t.Errorf("got %d vertices, want 0", len(got.Vertices))
		}
		if len(got.Edges) != 0 {
			t.Errorf("got %d edges, want 0", len(got.Edges))
		}
	})

	// Exercise SnapshotGraph's single-lock pass against concurrent writers.
	// Run under -race (CI default) this catches a missing or wrong lock.
	t.Run("ConcurrentWritesNoRace", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		future := time.Now().Add(time.Hour)
		var wg sync.WaitGroup

		for w := 0; w < 4; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				key := string(rune('a' + w))
				for i := 0; i < 200; i++ {
					c.PutVerticesWithExpiration([]VertexItem[string, string]{
						{Key: key, Value: "v", Expiration: future},
					})
					c.PutEdgesWithExpiration([]EdgeItem[string]{
						{Tail: key, Head: "hub", Weight: 1, Expiration: future},
					})
				}
			}(w)
		}
		for s := 0; s < 2; s++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 200; i++ {
					_ = c.SnapshotGraph()
				}
			}()
		}
		wg.Wait()

		if got := c.SnapshotGraph(); len(got.Vertices) == 0 {
			t.Error("expected some vertices after concurrent writes")
		}
	})
}

func TestGraphCache_SnapshotReplicationRetainsCausalFloors(t *testing.T) {
	newer := hlc.Timestamp{WallNs: 20, NodeID: hlc.NodeID{0x20}}
	older := hlc.Timestamp{WallNs: 10, NodeID: hlc.NodeID{0x10}}
	live := time.Now().Add(time.Hour)

	t.Run("expired but unflushed live Put becomes terminal barrier", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		expiration := time.Now().Add(30 * time.Millisecond)
		if !c.PutVertexWithExpirationHLC("v", "value", expiration, newer) {
			t.Fatal("vertex Put rejected")
		}
		if !c.PutEdgeWithExpirationHLC("tail", "head", 2, expiration, newer) {
			t.Fatal("edge Put rejected")
		}
		time.Sleep(60 * time.Millisecond)

		snapshot := c.SnapshotReplication()
		if len(snapshot.Graph.Vertices) != 0 || len(snapshot.Graph.Edges) != 0 ||
			len(snapshot.Barriers.Vertices) != 1 || len(snapshot.Barriers.Edges) != 1 {
			t.Fatalf("replication snapshot = %+v", snapshot)
		}
		// PutEdge auto-created expired endpoint slots that carry no Put HLC;
		// they are unrelated to v's causal floor and are reclaimed by ordinary
		// vertex GC. The HLC-tracked payload and edge bucket themselves must be
		// gone so a backward clock jump cannot reveal them again.
		if _, ok := c.vertices.GetAt("v", expiration.Add(-time.Second)); ok || c.edges.count() != 0 {
			t.Fatalf("migration left rollback-visible HLC state: vertex=%v edges=%d", ok, c.edges.count())
		}
		if c.PutVertexWithExpirationHLC("v", "older", live, older) {
			t.Fatal("older vertex resurrected after snapshot migration")
		}
		if c.PutEdgeWithExpirationHLC("tail", "head", 9, live, older) {
			t.Fatal("older edge resurrected after snapshot migration")
		}
	})

	t.Run("dangling Put edge is represented by barrier", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		if !c.PutEdgeWithExpirationHLC("tail", "head", 2, live, newer) {
			t.Fatal("edge Put rejected")
		}
		if !c.DeleteVertex("head") {
			t.Fatal("head delete failed")
		}
		snapshot := c.SnapshotReplication()
		if len(snapshot.Graph.Edges) != 0 || len(snapshot.Barriers.Edges) != 1 {
			t.Fatalf("dangling replication snapshot = %+v", snapshot)
		}
		if c.PutEdgeWithExpirationHLC("tail", "head", 9, live, older) {
			t.Fatal("older edge resurrected after dangling snapshot migration")
		}
	})

	t.Run("Put floor survives after its row expires but Add remains live", func(t *testing.T) {
		source := NewGraphCache[string, string](time.Hour)
		if err := source.PutVerticesWithExpiration([]VertexItem[string, string]{
			{Key: "tail", Value: "tail", Expiration: live},
			{Key: "head", Value: "head", Expiration: live},
		}); err != nil {
			t.Fatalf("Put endpoints: %v", err)
		}
		putExpiration := time.Now().Add(40 * time.Millisecond)
		if !source.PutEdgeWithExpirationHLC("tail", "head", 2, putExpiration, newer) {
			t.Fatal("Put edge rejected")
		}
		var addID ContribID
		addID[0] = 0x30
		if !source.AddEdgeWithExpirationContribHLC("tail", "head", 3, live, addID, hlc.Timestamp{WallNs: 30}) {
			t.Fatal("Add edge rejected")
		}
		time.Sleep(80 * time.Millisecond)

		snapshot := source.SnapshotReplication()
		if len(snapshot.Barriers.Edges) != 1 || len(snapshot.Graph.Edges) != 1 {
			t.Fatalf("snapshot barrier/edge counts = %d/%d, want 1/1", len(snapshot.Barriers.Edges), len(snapshot.Graph.Edges))
		}
		edge := snapshot.Graph.Edges[0]
		if edge.HLC != newer {
			t.Fatalf("SnapshotEdge.HLC = %+v, want Put floor %+v", edge.HLC, newer)
		}
		if len(edge.Contributions) != 1 || edge.Contributions[0].ContribID != addID {
			t.Fatalf("SnapshotEdge contributions = %+v, want surviving Add only", edge.Contributions)
		}

		follower := NewGraphCache[string, string](time.Hour)
		replayReplicationSnapshot(follower, snapshot)
		if got, ok := follower.GetWeight("tail", "head"); !ok || got != 3 {
			t.Fatalf("follower edge = (%v,%v), want (3,true)", got, ok)
		}
		if _, edges := follower.CausalBarrierCounts(); edges != 1 {
			t.Fatalf("follower edge barriers = %d, want 1", edges)
		}
		if follower.PutEdgeWithExpirationHLC("tail", "head", 9, live, older) {
			t.Fatal("follower accepted Put older than the expired Put floor")
		}

		// A second bootstrap is idempotent: the Add's ContribID deduplicates,
		// and the retained barrier is emitted again by the follower.
		repeated := follower.SnapshotReplication()
		replayReplicationSnapshot(follower, repeated)
		if got, ok := follower.GetWeight("tail", "head"); !ok || got != 3 {
			t.Fatalf("edge after repeated replay = (%v,%v), want (3,true)", got, ok)
		}
	})

	t.Run("barrier coexists with equal Add and frame uses greatest Put floor", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		if !c.ApplyEdgeCausalBarrierHLC("tail", "head", newer) {
			t.Fatal("barrier rejected")
		}
		var addID ContribID
		addID[0] = 0x20
		if !c.AddEdgeWithExpirationContribHLC("tail", "head", 1, live, addID, newer) {
			t.Fatal("Add equal to barrier should be accepted")
		}
		snapshot := c.SnapshotReplication()
		if len(snapshot.Barriers.Edges) != 1 || len(snapshot.Graph.Edges) != 1 || snapshot.Graph.Edges[0].HLC != newer {
			t.Fatalf("equal Add snapshot = %+v", snapshot)
		}

		higher := hlc.Timestamp{WallNs: 30, NodeID: hlc.NodeID{0x30}}
		if !c.PutEdgeWithExpirationHLC("tail", "head", 4, live, higher) {
			t.Fatal("newer Put rejected")
		}
		snapshot = c.SnapshotReplication()
		if len(snapshot.Barriers.Edges) != 1 || snapshot.Barriers.Edges[0].HLC != higher || snapshot.Graph.Edges[0].HLC != higher {
			t.Fatalf("live Put snapshot did not carry greatest floor: %+v", snapshot)
		}
	})

	t.Run("hidden Add bucket beside retained barrier is terminally removed", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		if !c.ApplyEdgeCausalBarrierHLC("tail", "head", newer) {
			t.Fatal("barrier rejected")
		}
		var addID ContribID
		addID[0] = 0x31
		expiration := time.Now().Add(40 * time.Millisecond)
		if !c.AddEdgeWithExpirationContribHLC("tail", "head", 3, expiration, addID, hlc.Timestamp{WallNs: 30}) {
			t.Fatal("Add rejected")
		}
		time.Sleep(80 * time.Millisecond)
		snapshot := c.SnapshotReplication()
		if len(snapshot.Graph.Edges) != 0 || len(snapshot.Barriers.Edges) != 1 {
			t.Fatalf("hidden Add snapshot = %+v", snapshot)
		}
		if c.edges.count() != 0 {
			t.Fatalf("hidden Add bucket remains physical: %d", c.edges.count())
		}
		if _, _, ok := c.GetEdgeDetail("tail", "head"); ok {
			t.Fatal("hidden Add edge visible after terminal migration")
		}
	})

	t.Run("implicit Add endpoint preserves an older vertex Put floor", func(t *testing.T) {
		source := NewGraphCache[string, string](time.Hour)
		if !source.ApplyVertexCausalBarrierHLC("tail", newer) {
			t.Fatal("vertex barrier rejected")
		}
		var addID ContribID
		addID[0] = 0x32
		if !source.AddEdgeWithExpirationContribHLC("tail", "head", 1, live, addID, hlc.Timestamp{WallNs: 30}) {
			t.Fatal("Add rejected")
		}
		if !source.DeleteEdge("tail", "head") {
			t.Fatal("DeleteEdge rejected")
		}

		snapshot := source.SnapshotReplication()
		if len(snapshot.Barriers.Vertices) != 1 {
			t.Fatalf("vertex barrier count = %d, want 1", len(snapshot.Barriers.Vertices))
		}
		vertices := vertexByKey(snapshot.Graph.Vertices)
		endpoint, ok := vertices["tail"]
		if !ok || endpoint.HLC != newer {
			t.Fatalf("tail endpoint snapshot = %+v, present=%v; want HLC %+v", endpoint, ok, newer)
		}

		follower := NewGraphCache[string, string](time.Hour)
		replayReplicationSnapshot(follower, snapshot)
		if _, ok := follower.GetVertex("tail"); !ok {
			t.Fatal("follower lost implicit endpoint")
		}
		if follower.PutVertexWithExpirationHLC("tail", "older", live, older) {
			t.Fatal("follower accepted Put older than endpoint's retained floor")
		}
		repeated := follower.SnapshotReplication()
		replayReplicationSnapshot(follower, repeated)
		if follower.PutVertexWithExpirationHLC("tail", "older-again", live, older) {
			t.Fatal("repeated snapshot lost implicit endpoint floor")
		}
	})
}

// TestGraphCache_Snapshot_ReferentialClosure pins the snapshot half of the
// referential-closure contract (#750): a LIVE edge whose endpoint vertex has
// been deleted (or has expired but not yet been flushed) must not appear in
// SnapshotGraph or SnapshotEdges, even before the dangling-edge GC sweep, so a
// restored backup is always referentially closed.
func TestGraphCache_Snapshot_ReferentialClosure(t *testing.T) {
	live := time.Now().Add(time.Hour)
	build := func() *GraphCache[string, string] {
		c := NewGraphCache[string, string](time.Hour)
		c.PutVerticesWithExpiration([]VertexItem[string, string]{
			{Key: "a", Value: "A", Expiration: live},
			{Key: "b", Value: "B", Expiration: live},
		})
		c.PutEdgesWithExpiration([]EdgeItem[string]{
			{Tail: "a", Head: "b", Weight: 2, Expiration: live},
		})
		return c
	}
	danglingKey := EdgeKey[string]{Tail: "a", Head: "b"}

	t.Run("SnapshotGraphHidesDanglingEdge", func(t *testing.T) {
		c := build()
		if !c.DeleteVertex("b") {
			t.Fatal("DeleteVertex(b) reported false")
		}
		got := c.SnapshotGraph()
		if _, ok := vertexByKey(got.Vertices)["b"]; ok {
			t.Errorf("deleted vertex b present in SnapshotGraph: %v", got.Vertices)
		}
		if _, ok := edgeByEndpoints(got.Edges)[danglingKey]; ok {
			t.Errorf("dangling edge a->b present in SnapshotGraph: %v", got.Edges)
		}
		if c.edges.count() != 1 {
			t.Fatalf("edge bucket count = %d, want 1 (edge still physical before sweep)", c.edges.count())
		}
	})

	t.Run("SnapshotEdgesHidesDanglingEdge", func(t *testing.T) {
		c := build()
		if !c.DeleteVertex("b") {
			t.Fatal("DeleteVertex(b) reported false")
		}
		for _, e := range c.SnapshotEdges() {
			if e.Tail == "a" && e.Head == "b" {
				t.Fatalf("SnapshotEdges streamed dangling edge a->b: %+v", e)
			}
		}
	})

	t.Run("ExpiredNotFlushedEndpointHidden", func(t *testing.T) {
		c := build()
		// Make b expired-but-not-flushed without touching the edge bucket.
		c.mu.Lock()
		c.putVertexLocked("b", "B", time.Now().Add(-time.Minute))
		c.mu.Unlock()
		got := c.SnapshotGraph()
		if _, ok := edgeByEndpoints(got.Edges)[danglingKey]; ok {
			t.Errorf("snapshot kept edge to expired-not-flushed endpoint: %v", got.Edges)
		}
	})
}

// TestSnapshotRLockContract pins the #843 lock downgrade: snapshots must be
// takeable while another goroutine holds the aggregate READ lock (a write
// lock would deadlock this test), must FILTER expired-but-unflushed entries
// without physically removing them (reclamation belongs to the GC tick),
// and must keep set-level vertex/edge co-existence under concurrent writers.
func TestSnapshotRLockContract(t *testing.T) {
	t.Run("snapshots proceed under a held read lock", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.PutVertexWithExpiration("a", "va", time.Now().Add(time.Minute))

		c.mu.RLock()
		defer c.mu.RUnlock()
		done := make(chan GraphSnapshot[string, string], 1)
		go func() { done <- c.SnapshotGraph() }()
		select {
		case snap := <-done:
			if len(snap.Vertices) != 1 {
				t.Fatalf("Vertices = %d, want 1", len(snap.Vertices))
			}
		case <-time.After(5 * time.Second):
			t.Fatal("SnapshotGraph blocked behind a concurrent RLock — write lock regression")
		}
	})

	t.Run("expired entries are filtered, not flushed", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.PutVertexWithExpiration("live", "v", time.Now().Add(time.Minute))
		c.PutVertexWithExpiration("dead", "v", time.Now().Add(20*time.Millisecond))
		time.Sleep(30 * time.Millisecond)

		snap := c.SnapshotGraph()
		if len(snap.Vertices) != 1 || snap.Vertices[0].Key != "live" {
			t.Fatalf("snapshot vertices = %+v, want only live", snap.Vertices)
		}
		// The expired slot must still be physically present afterwards —
		// the snapshot pass no longer mutates; only the GC tick reclaims.
		if got := c.vertices.Count(); got != 2 {
			t.Fatalf("physical vertex count after snapshot = %d, want 2 (no flush side effect)", got)
		}
		if _, ok := c.GetVertex("dead"); ok {
			t.Fatal("expired vertex visible to point reads")
		}
	})

	t.Run("vertex-edge co-existence holds under concurrent writers", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		exp := time.Now().Add(time.Minute)
		stop := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				k := "w" + strconv.Itoa(i%64)
				c.PutVertexWithExpiration(k, "v", exp)
				c.AddEdgeWithExpiration(k, "hub-"+strconv.Itoa(i%8), 1, exp)
				if i%16 == 0 {
					c.DeleteVertex(k)
				}
			}
		}()

		for i := 0; i < 50; i++ {
			snap := c.SnapshotGraph()
			present := make(map[string]bool, len(snap.Vertices))
			for _, v := range snap.Vertices {
				present[v.Key] = true
			}
			for _, e := range snap.Edges {
				if !present[e.Tail] || !present[e.Head] {
					t.Fatalf("snapshot %d: edge %s->%s references a vertex absent from the SAME snapshot", i, e.Tail, e.Head)
				}
			}
		}
		close(stop)
		wg.Wait()
	})
}
