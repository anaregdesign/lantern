package backup

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/prototime"
	"github.com/anaregdesign/lantern/server/service"
)

// receiptWholeStateStage is detached from every serving component. In
// particular, its Store is not the service-bound Store and its graph is not
// the cache used by any RPC, replication pump, GC watcher, or metric sampler.
// A later installer must prove a WAL/clock frontier or rotate the epoch and
// publish all components under one new serving generation.
type receiptWholeStateStage struct {
	graph          *graphcache.GraphCache[string, *pb.Vertex]
	receipts       *mutationreceipt.Store
	policy         mutationreceipt.Config
	origins        []service.OriginState
	cutoffLocalSeq uint64
	cutoffHLC      hlc.Timestamp
}

type archiveEdgeKey struct{ tail, head string }

// A nil HLC represents a local-only live row with no recorded causal floor.
// Every explicit floor must be a complete nonzero timestamp and node identity.
func archiveHLC(stamp *pb.HLCTimestamp) (hlc.Timestamp, bool) {
	if stamp == nil || stamp.GetWallNs() <= 0 || len(stamp.GetNodeId()) != 16 {
		return hlc.Timestamp{}, false
	}
	var id hlc.NodeID
	copy(id[:], stamp.GetNodeId())
	if id == (hlc.NodeID{}) {
		return hlc.Timestamp{}, false
	}
	return hlc.Timestamp{WallNs: stamp.GetWallNs(), Logical: stamp.GetLogical(), NodeID: id}, true
}

// stageReceiptWholeStateArchive validates the complete private archive before
// creating an isolated graph. configureGraph may enable the same optional
// indexes and limits as the future serving cache, but must leave this newly
// constructed cache empty. The identity prefix/head index is always enabled.
// No caller in the production restore path invokes this prerequisite.
func stageReceiptWholeStateArchive(
	ctx context.Context,
	r io.Reader,
	expected mutationreceipt.Config,
	defaultTTL time.Duration,
	configureGraph func(*graphcache.GraphCache[string, *pb.Vertex]) error,
) (*receiptWholeStateStage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r == nil {
		return nil, wholeStateArchiveError("archive reader is nil")
	}
	archive, err := decodeWholeStateArchive(r)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := mutationreceipt.New(expected); err != nil {
		return nil, fmt.Errorf("backup: invalid expected receipt policy: %w", err)
	}
	if archive.Policy.Epoch != expected.Epoch || archive.Policy.Retention != expected.Retention ||
		archive.Policy.MaxEntries != expected.MaxEntries || archive.Policy.MaxBytes != expected.MaxBytes ||
		(!expected.ClockHighWater.IsZero() && expected.ClockHighWater.UnixMilli() > archive.Receipts.ClockHighWaterMillis) {
		return nil, wholeStateArchiveError("archive policy differs from expected policy")
	}
	store, err := mutationreceipt.NewFromSnapshot(archive.Policy, archive.Receipts)
	if err != nil {
		return nil, fmt.Errorf("backup: stage receipt Store: %w", err)
	}
	graph := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](defaultTTL)
	graph.EnablePrefixIndex(func(key string) string { return key })
	if configureGraph != nil {
		if err := configureGraph(graph); err != nil {
			return nil, fmt.Errorf("backup: configure staged graph: %w", err)
		}
	}
	if !emptyReceiptStageGraph(graph) {
		return nil, wholeStateArchiveError("graph configurator populated the staged graph")
	}
	// The cache is detached, so no reader can see an intermediate index.
	// Keep the index healthy for checked local Put replay (implicit endpoint
	// vertices can coexist with retained causal floors), then require a full
	// bounded rebuild before returning the candidate.
	if err := replayReceiptArchiveGraph(ctx, graph, archive.Graph, time.Now()); err != nil {
		return nil, err
	}
	if err := graph.CompleteSearchIndexRecovery(); err != nil {
		return nil, fmt.Errorf("backup: rebuild staged search index: %w", err)
	}
	if err := validateStagedTombstones(graph, archive.Graph); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cutoff, ok := archiveHLC(archive.Graph[0].GetHeader().GetCutoffHlc())
	if !ok {
		return nil, wholeStateArchiveError("invalid staged HLC cutoff")
	}
	return &receiptWholeStateStage{
		graph: graph, receipts: store, policy: archive.Policy,
		origins:        append([]service.OriginState(nil), archive.Origins...),
		cutoffLocalSeq: archive.Graph[0].GetHeader().GetCutoffLocalSeq(), cutoffHLC: cutoff,
	}, nil
}

func emptyReceiptStageGraph(graph *graphcache.GraphCache[string, *pb.Vertex]) bool {
	// SnapshotReplication intentionally hides dangling edges and expired
	// storage. Counts and index/causal usage close that gap for a caller-
	// supplied configurator before the private replay starts.
	terms, documents := graph.SearchIndexStats()
	causal := graph.CausalMetadataStats()
	if graph.VertexCount() != 0 || graph.EdgeCount() != 0 || graph.VertexHLCCount() != 0 ||
		terms != 0 || documents != 0 || causal.VertexEntries != 0 || causal.EdgeEntries != 0 {
		return false
	}
	snapshot := graph.SnapshotReplication()
	return len(snapshot.Barriers.Vertices) == 0 && len(snapshot.Barriers.Edges) == 0 &&
		len(snapshot.Tombstones.Vertices) == 0 && len(snapshot.Tombstones.Edges) == 0 &&
		len(snapshot.Graph.Vertices) == 0 && len(snapshot.Graph.Edges) == 0
}

// replayReceiptArchiveGraph runs only against a fresh, unpublished cache.
// decodeWholeStateArchive already validated frame shape, order, and cross-frame
// relationships. Apply failures still fail closed: no partial cache escapes.
func replayReceiptArchiveGraph(
	ctx context.Context,
	graph *graphcache.GraphCache[string, *pb.Vertex],
	frames []*pb.SnapshotResponse,
	restoreNow time.Time,
) error {
	vertexBarriers := make(map[string]hlc.Timestamp)
	vertexTombstones := make(map[string]struct{})
	for _, frame := range frames[1 : len(frames)-1] {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch entry := frame.GetEntry().(type) {
		case *pb.SnapshotResponse_VertexCausalBarrier:
			item := entry.VertexCausalBarrier
			ts, _ := archiveHLC(item.GetHlc())
			if !graph.ApplyVertexCausalBarrierHLC(item.GetKey(), ts) {
				return wholeStateArchiveError("staged vertex barrier was rejected")
			}
			vertexBarriers[item.GetKey()] = ts
		case *pb.SnapshotResponse_EdgeCausalBarrier:
			item := entry.EdgeCausalBarrier
			ts, _ := archiveHLC(item.GetHlc())
			if !graph.ApplyEdgeCausalBarrierHLC(item.GetTail(), item.GetHead(), ts) {
				return wholeStateArchiveError("staged edge barrier was rejected")
			}
		case *pb.SnapshotResponse_VertexTombstone:
			item := entry.VertexTombstone
			ts, _ := archiveHLC(item.GetHlc())
			expiration := item.GetExpiration().AsTime()
			if restoreNow.Before(expiration) {
				graph.ApplySnapshotVertexTombstoneHLC(item.GetKey(), ts, expiration)
			}
			vertexTombstones[item.GetKey()] = struct{}{}
		case *pb.SnapshotResponse_EdgeTombstone:
			item := entry.EdgeTombstone
			expiration := item.GetExpiration().AsTime()
			if restoreNow.Before(expiration) {
				ts, _ := archiveHLC(item.GetHlc())
				graph.ApplySnapshotEdgeTombstoneHLC(item.GetTail(), item.GetHead(), ts, expiration)
			}
		case *pb.SnapshotResponse_Vertex:
			item := entry.Vertex
			vertex := item.GetVertex()
			key := vertex.GetKey()
			// An implicit endpoint can coexist with an older retained Put
			// barrier or Vertex Delete tombstone. Its snapshot HLC is the
			// inherited floor, not a new Vertex Put that may clear that floor.
			barrier, hasBarrier := vertexBarriers[key]
			_, hasTombstone := vertexTombstones[key]
			if hasBarrier || hasTombstone {
				if hasBarrier {
					liveHLC, _ := archiveHLC(item.GetHlc())
					if liveHLC != barrier {
						return wholeStateArchiveError("implicit vertex HLC differs from its barrier")
					}
				} else if item.GetHlc() != nil {
					return wholeStateArchiveError("implicit vertex retained HLC after Delete")
				}
				if err := graph.PutVertexWithExpiration(key, vertex, prototime.Expiration(vertex.GetExpiration())); err != nil {
					return fmt.Errorf("backup: stage implicit vertex: %w", err)
				}
				continue
			}
			ts := hlc.Timestamp{}
			if item.GetHlc() != nil {
				ts, _ = archiveHLC(item.GetHlc())
			}
			if !graph.PutVertexWithExpirationHLC(key, vertex, prototime.Expiration(vertex.GetExpiration()), ts) {
				return wholeStateArchiveError("staged vertex was causally rejected")
			}
		case *pb.SnapshotResponse_Edge:
			item := entry.Edge
			putHLC := hlc.Timestamp{}
			if item.GetHlc() != nil {
				putHLC, _ = archiveHLC(item.GetHlc())
			}
			for _, contribution := range item.GetContributions() {
				expiration := prototime.Expiration(contribution.GetExpiration())
				if len(contribution.GetContribId()) == 0 {
					if !graph.PutEdgeWithExpirationHLC(item.GetTail(), item.GetHead(), contribution.GetWeight(), expiration, putHLC) {
						return wholeStateArchiveError("staged edge Put was causally rejected")
					}
					continue
				}
				var id graphcache.ContribID
				copy(id[:], contribution.GetContribId())
				ts, _ := archiveHLC(contribution.GetHlc())
				if !graph.AddEdgeWithExpirationContribHLC(item.GetTail(), item.GetHead(), contribution.GetWeight(), expiration, id, ts) {
					return wholeStateArchiveError("staged edge Add was causally rejected")
				}
			}
		default:
			return wholeStateArchiveError("unexpected staged graph frame")
		}
	}
	return nil
}

// The snapshot replay API deliberately treats an expired Delete marker as a
// no-op and returns no outcome. For a private whole-state candidate, silently
// losing a retained D4 floor is unacceptable: verify exact deadlines and HLCs
// after all replay/index work, before any candidate can escape.
func validateStagedTombstones(graph *graphcache.GraphCache[string, *pb.Vertex], frames []*pb.SnapshotResponse) error {
	type marker struct {
		hlc        hlc.Timestamp
		expiration time.Time
	}
	vertices := make(map[string]marker)
	edges := make(map[archiveEdgeKey]marker)
	for _, frame := range frames[1 : len(frames)-1] {
		switch entry := frame.GetEntry().(type) {
		case *pb.SnapshotResponse_VertexTombstone:
			item := entry.VertexTombstone
			ts, _ := archiveHLC(item.GetHlc())
			vertices[item.GetKey()] = marker{ts, item.GetExpiration().AsTime()}
		case *pb.SnapshotResponse_EdgeTombstone:
			item := entry.EdgeTombstone
			ts, _ := archiveHLC(item.GetHlc())
			edges[archiveEdgeKey{item.GetTail(), item.GetHead()}] = marker{ts, item.GetExpiration().AsTime()}
		}
	}
	snapshot := graph.SnapshotReplication().Tombstones
	now := time.Now()
	for _, item := range snapshot.Vertices {
		want, ok := vertices[item.Key]
		if !ok || want.hlc != item.HLC || !want.expiration.Equal(item.Expiration) {
			return wholeStateArchiveError("staged vertex tombstone differs from archive")
		}
		delete(vertices, item.Key)
	}
	for _, item := range snapshot.Edges {
		key := archiveEdgeKey{item.Tail, item.Head}
		want, ok := edges[key]
		if !ok || want.hlc != item.HLC || !want.expiration.Equal(item.Expiration) {
			return wholeStateArchiveError("staged edge tombstone differs from archive")
		}
		delete(edges, key)
	}
	for _, want := range vertices {
		if now.Before(want.expiration) {
			return wholeStateArchiveError("live staged vertex tombstone is missing")
		}
	}
	for _, want := range edges {
		if now.Before(want.expiration) {
			return wholeStateArchiveError("live staged edge tombstone is missing")
		}
	}
	return nil
}
