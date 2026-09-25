package backup

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/prototime"
)

func TestReceiptWholeStateArchiveStageKeepsDetachedReceiptAndCut(t *testing.T) {
	archive := wholeStateArchiveFixture(t)
	raw := encodedWholeStateArchive(t, archive)
	stage, err := stageReceiptWholeStateArchive(context.Background(), bytes.NewReader(raw), archive.Policy, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stage.policy != archive.Policy || stage.cutoffLocalSeq != archive.Graph[0].GetHeader().GetCutoffLocalSeq() ||
		!reflect.DeepEqual(stage.origins, archive.Origins) || stage.cutoffHLC != archive.Origins[0].LastHLC {
		t.Fatalf("staged cut drift: %+v", stage)
	}
	gotReceipts, err := stage.receipts.Snapshot()
	if err != nil || !reflect.DeepEqual(gotReceipts, archive.Receipts) {
		t.Fatalf("staged receipts = %+v, %v", gotReceipts, err)
	}
	if got, _, ok := stage.graph.GetEdgeDetail("tail", "head"); !ok || got != 1.5 {
		t.Fatalf("staged Add contribution = %v, %t", got, ok)
	}
	if got := stage.graph.CountByPrefix(""); got != 2 {
		t.Fatalf("staged prefix index count = %d, want 2", got)
	}
	// Decoder, Store, and graph own their data after staging. The archived
	// bytes and decoded frame pointers cannot be used to change the result.
	raw[0] ^= 1
	archive.Receipts.Receipts[0].Result[0] ^= 1
	archive.Graph[1].GetVertex().Vertex.Key = "changed"
	archive.Origins[0].LastSeq++
	again, err := stage.receipts.Snapshot()
	if err != nil || !reflect.DeepEqual(again, gotReceipts) || stage.origins[0].LastSeq != 7 {
		t.Fatalf("staged receipt/origin alias: %+v, %v", again, err)
	}
	if _, ok := stage.graph.GetVertex("tail"); !ok {
		t.Fatal("staged graph aliased input frame")
	}
}

func TestReceiptWholeStateArchiveStageReconstructsCausalGraphAndIndexes(t *testing.T) {
	f := newReceiptArchiveFixture(t, nil)
	future := time.Now().Add(2 * time.Hour)
	past := time.Now().Add(-time.Hour)
	base := time.Now().Add(-time.Minute).UnixNano()
	origin := hlc.NodeID{0x92}
	stamp := func(n int64) hlc.Timestamp { return hlc.Timestamp{WallNs: base + n, NodeID: origin} }
	if !f.cache.PutVertexWithExpirationHLC("barrier-endpoint", &pb.Vertex{Key: "barrier-endpoint"}, past, stamp(1)) {
		t.Fatal("failed to seed vertex barrier")
	}
	if !f.cache.AddEdgeWithExpirationContribHLC("barrier-endpoint", "other", 1, future, graphcache.ContribID{1}, stamp(2)) {
		t.Fatal("failed to revive endpoint above barrier")
	}
	f.cache.DeleteEdgeHLC("barrier-endpoint", "other", stamp(3), future)
	f.cache.DeleteVertexHLC("tombstone-endpoint", stamp(4), future)
	if !f.cache.AddEdgeWithExpirationContribHLC("tombstone-endpoint", "other", 1, future, graphcache.ContribID{2}, stamp(5)) {
		t.Fatal("failed to revive endpoint after Vertex Delete")
	}
	f.cache.DeleteEdgeHLC("tombstone-endpoint", "other", stamp(6), future)
	if !f.cache.PutEdgeWithExpirationHLC("tail", "head", 1, past, stamp(7)) ||
		!f.cache.AddEdgeWithExpirationContribHLC("tail", "head", 2, future, graphcache.ContribID{3}, stamp(8)) {
		t.Fatal("failed to seed edge barrier and newer Add")
	}
	if !f.cache.PutEdgeWithExpirationHLC("put-tail", "put-head", 4, future, stamp(9)) ||
		!f.cache.AddEdgeWithExpirationContribHLC("put-tail", "put-head", 5, future, graphcache.ContribID{4}, stamp(10)) {
		t.Fatal("failed to seed live Put plus newer Add")
	}
	raw, err := produceReceiptWholeStateArchive(context.Background(), f.source, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	configure := func(c *graphcache.GraphCache[string, *pb.Vertex]) error {
		c.EnableSearchIndex(func(key string, _ *pb.Vertex) search.Document { return search.Text(key) }, strings.Compare)
		return nil
	}
	stage, err := stageReceiptWholeStateArchive(context.Background(), bytes.NewReader(raw), f.policy, time.Hour, configure)
	if err != nil {
		t.Fatal(err)
	}
	before := decodedProducerArchive(t, raw)
	if stage.cutoffLocalSeq != before.Graph[0].GetHeader().GetCutoffLocalSeq() || len(stage.origins) != len(before.Origins) {
		t.Fatalf("staged cutoff/origins drift: %+v", stage)
	}
	graph := stage.graph.SnapshotReplication()
	assertStagedLiveGraphMatchesArchive(t, graph, before.Graph)
	if len(graph.Barriers.Vertices) != 1 || len(graph.Tombstones.Vertices) != 1 ||
		len(graph.Barriers.Edges) != 2 || len(graph.Tombstones.Edges) != 2 {
		t.Fatalf("staged causal floors drift: %+v", graph)
	}
	for _, marker := range graph.Tombstones.Vertices {
		if !marker.Expiration.Equal(future) {
			t.Fatalf("Vertex D4 deadline changed: %v, want %v", marker.Expiration, future)
		}
	}
	for _, marker := range graph.Tombstones.Edges {
		if !marker.Expiration.Equal(future) {
			t.Fatalf("Edge D4 deadline changed: %v, want %v", marker.Expiration, future)
		}
	}
	if got := stage.graph.CountByPrefix("barrier-endpoint"); got != 1 {
		t.Fatalf("barrier endpoint prefix count = %d", got)
	}
	if got := stage.graph.CountByPrefix("tombstone-endpoint"); got != 1 {
		t.Fatalf("tombstone endpoint prefix count = %d", got)
	}
	if got := stage.graph.SearchIndexMemoryStats(); got.Health != search.IndexHealthy || got.Documents == 0 {
		t.Fatalf("staged search index = %+v", got)
	}
	if stage.graph.PutVertexWithExpirationHLC("barrier-endpoint", &pb.Vertex{Key: "barrier-endpoint"}, future, stamp(0)) {
		t.Fatal("staged vertex barrier did not reject older Put")
	}
	if stage.graph.PutVertexWithExpirationHLC("tombstone-endpoint", &pb.Vertex{Key: "tombstone-endpoint"}, future, stamp(0)) {
		t.Fatal("staged vertex tombstone did not reject older Put")
	}
	if stage.graph.PutEdgeWithExpirationHLC("tail", "head", 9, future, stamp(0)) {
		t.Fatal("staged edge barrier did not reject older Put")
	}
	if weight, _, ok := stage.graph.GetEdgeDetail("tail", "head"); !ok || weight != 2 {
		t.Fatalf("staged Add after barrier = %v, %t", weight, ok)
	}
	if weight, _, ok := stage.graph.GetEdgeDetail("put-tail", "put-head"); !ok || weight != 9 {
		t.Fatalf("staged Put plus Add = %v, %t", weight, ok)
	}
	// The installed Add identity is still effective: replaying it must not
	// accumulate a second contribution.
	if stage.graph.AddEdgeWithExpirationContribHLC("tail", "head", 2, future, graphcache.ContribID{3}, stamp(8)) {
		t.Fatal("staged Add ContribID was lost")
	}
	if weight, _, ok := stage.graph.GetEdgeDetail("tail", "head"); !ok || weight != 2 {
		t.Fatalf("staged edge changed after duplicate Add: %v, %t", weight, ok)
	}
}

func assertStagedLiveGraphMatchesArchive(t *testing.T, staged graphcache.ReplicationSnapshot[string, *pb.Vertex], frames []*pb.SnapshotResponse) {
	t.Helper()
	vertices := make(map[string]*pb.SnapshotVertex)
	edges := make(map[archiveEdgeKey]*pb.SnapshotEdge)
	for _, frame := range frames[1 : len(frames)-1] {
		if item := frame.GetVertex(); item != nil {
			vertices[item.GetVertex().GetKey()] = item
		}
		if item := frame.GetEdge(); item != nil {
			edges[archiveEdgeKey{item.GetTail(), item.GetHead()}] = item
		}
	}
	if len(staged.Graph.Vertices) != len(vertices) || len(staged.Graph.Edges) != len(edges) {
		t.Fatalf("staged live graph count = %d/%d, archive = %d/%d",
			len(staged.Graph.Vertices), len(staged.Graph.Edges), len(vertices), len(edges))
	}
	for _, got := range staged.Graph.Vertices {
		want, ok := vertices[got.Key]
		wantHLC, _ := archiveHLC(want.GetHlc())
		if !ok || !proto.Equal(got.Value, want.GetVertex()) || got.HLC != wantHLC ||
			!got.Expiration.Equal(prototime.Expiration(want.GetVertex().GetExpiration())) {
			t.Fatalf("staged vertex %q differs from archive: %+v versus %+v", got.Key, got, want)
		}
	}
	for _, got := range staged.Graph.Edges {
		want, ok := edges[archiveEdgeKey{got.Tail, got.Head}]
		wantHLC, _ := archiveHLC(want.GetHlc())
		if !ok || got.HLC != wantHLC || len(got.Contributions) != len(want.GetContributions()) {
			t.Fatalf("staged edge %q→%q differs from archive: %+v versus %+v", got.Tail, got.Head, got, want)
		}
		contributions := make(map[graphcache.ContribID]*pb.SnapshotEdgeContribution, len(want.GetContributions()))
		for _, item := range want.GetContributions() {
			var id graphcache.ContribID
			copy(id[:], item.GetContribId())
			contributions[id] = item
		}
		for _, item := range got.Contributions {
			wantContribution, ok := contributions[item.ContribID]
			wantContributionHLC, _ := archiveHLC(wantContribution.GetHlc())
			if !ok || item.Weight != wantContribution.GetWeight() || item.HLC != wantContributionHLC ||
				!item.Expiration.Equal(prototime.Expiration(wantContribution.GetExpiration())) {
				t.Fatalf("staged contribution %q→%q (%x) differs from archive: %+v versus %+v",
					got.Tail, got.Head, item.ContribID, item, wantContribution)
			}
		}
	}
}

func TestReceiptWholeStateArchiveStageFailsWithoutCandidate(t *testing.T) {
	archive := wholeStateArchiveFixture(t)
	raw := encodedWholeStateArchive(t, archive)
	badPolicy := archive.Policy
	badPolicy.MaxEntries++
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name      string
		ctx       context.Context
		raw       []byte
		policy    mutationreceipt.Config
		configure func(*graphcache.GraphCache[string, *pb.Vertex]) error
	}{
		{"truncated late footer", context.Background(), raw[:len(raw)-1], archive.Policy, nil},
		{"wrong policy", context.Background(), raw, badPolicy, nil},
		{"canceled", canceled, raw, archive.Policy, nil},
		{"configurator populated graph", context.Background(), raw, archive.Policy, func(c *graphcache.GraphCache[string, *pb.Vertex]) error {
			return c.PutVertexWithExpiration("extra", &pb.Vertex{Key: "extra"}, time.Now().Add(time.Hour))
		}},
		{"configurator hid dangling edge", context.Background(), raw, archive.Policy, func(c *graphcache.GraphCache[string, *pb.Vertex]) error {
			// The archive has tail/head vertices and a 1.5-weight Add. A
			// hidden 99-weight bucket would become visible during their replay.
			c.PutEdgeWithExpiration("tail", "head", 99, time.Now().Add(time.Hour))
			c.DeleteVertices([]string{"tail", "head"})
			if visible := c.SnapshotGraph(); len(visible.Vertices) != 0 || len(visible.Edges) != 0 || c.EdgeCount() != 1 {
				t.Errorf("dangling-edge fixture: visible vertices=%d, edges=%d, physical edges=%d",
					len(visible.Vertices), len(visible.Edges), c.EdgeCount())
			}
			return nil
		}},
		{"search budget", context.Background(), raw, archive.Policy, func(c *graphcache.GraphCache[string, *pb.Vertex]) error {
			c.EnableSearchIndex(func(key string, _ *pb.Vertex) search.Document { return search.Text(key) }, strings.Compare,
				graphcache.WithSearchAnalysisLimits(search.SearchAnalysisLimits{MaxDocumentBytes: 1}))
			return nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate, err := stageReceiptWholeStateArchive(tc.ctx, bytes.NewReader(tc.raw), tc.policy, time.Hour, tc.configure)
			if err == nil || candidate != nil {
				t.Fatalf("failed staging returned candidate=%p, err=%v", candidate, err)
			}
		})
	}
	if candidate, err := stageReceiptWholeStateArchive(context.Background(), nil, archive.Policy, time.Hour, nil); candidate != nil || !errors.Is(err, errWholeStateArchive) {
		t.Fatalf("nil reader returned candidate=%p, err=%v", candidate, err)
	}
	// A failed attempt cannot mutate the encoded archive or any prior
	// detached candidate; the entire staging graph is private to that call.
	valid, err := stageReceiptWholeStateArchive(context.Background(), bytes.NewReader(raw), archive.Policy, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _, ok := valid.graph.GetEdgeDetail("tail", "head"); !ok || got != 1.5 {
		t.Fatalf("valid prior candidate changed: %v, %t", got, ok)
	}
	if !proto.Equal(archive.Graph[3], wholeStateArchiveFixture(t).Graph[3]) {
		t.Fatal("archive frame changed during failed staging")
	}
}

func TestReceiptWholeStateArchiveStageRejectsLostOrInconsistentFloors(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*wholeStateArchive)
	}{
		{"expired vertex tombstone", func(a *wholeStateArchive) {
			a.Graph = append(a.Graph[:1], append([]*pb.SnapshotResponse{{Entry: &pb.SnapshotResponse_VertexTombstone{
				VertexTombstone: &pb.SnapshotVertexTombstone{Key: "old", Hlc: a.Graph[0].GetHeader().GetCutoffHlc(),
					Expiration: timestamppb.New(time.Now().Add(-time.Minute))},
			}}}, a.Graph[1:]...)...)
			a.Graph[len(a.Graph)-1].GetFooter().VertexTombstoneCount++
		}},
		{"expired edge tombstone", func(a *wholeStateArchive) {
			a.Graph = append(a.Graph[:1], append([]*pb.SnapshotResponse{{Entry: &pb.SnapshotResponse_EdgeTombstone{
				EdgeTombstone: &pb.SnapshotEdgeTombstone{Tail: "old-tail", Head: "old-head", Hlc: a.Graph[0].GetHeader().GetCutoffHlc(),
					Expiration: timestamppb.New(time.Now().Add(-time.Minute))},
			}}}, a.Graph[1:]...)...)
			a.Graph[len(a.Graph)-1].GetFooter().EdgeTombstoneCount++
		}},
		{"live vertex newer than barrier", func(a *wholeStateArchive) {
			stamp := a.Graph[0].GetHeader().GetCutoffHlc()
			a.Graph = append(a.Graph[:1], append([]*pb.SnapshotResponse{{Entry: &pb.SnapshotResponse_VertexCausalBarrier{
				VertexCausalBarrier: &pb.SnapshotVertexCausalBarrier{Key: "tail", Hlc: stamp},
			}}}, a.Graph[1:]...)...)
			newer := proto.Clone(stamp).(*pb.HLCTimestamp)
			newer.WallNs++
			a.Graph[2].GetVertex().Hlc = newer
			a.Graph[len(a.Graph)-1].GetFooter().VertexCausalBarrierCount++
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := wholeStateArchiveFixture(t)
			tc.edit(&archive)
			raw := encodedWholeStateArchive(t, archive) // Valid container and checksum.
			candidate, err := stageReceiptWholeStateArchive(context.Background(), bytes.NewReader(raw), archive.Policy, time.Hour, nil)
			if !errors.Is(err, errWholeStateArchive) || candidate != nil {
				t.Fatalf("inconsistent archive yielded candidate=%p, err=%v", candidate, err)
			}
		})
	}
}
