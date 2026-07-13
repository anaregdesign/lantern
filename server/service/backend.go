package service

import (
	"context"
	"time"

	coregraph "github.com/anaregdesign/lantern/core/graph"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// Backend is the narrow seam the service depends on instead of the concrete
// *graphcache.GraphCache. The interface is consumer-defined here so adding new
// service-layer RPCs widens it deliberately, and tests can supply a fake.
//
// The batch types (graphcache.VertexItem, graphcache.EdgeItem, graphcache.EdgeKey) remain
// imported as plain value structs — they describe data, not behavior, so
// re-declaring them would just shuffle conversions without buying anything.
type Backend interface {
	// vertex reads/writes
	GetVertex(key string) (*pb.Vertex, bool)
	PutVerticesWithExpirationChecked(items []graphcache.VertexItem[string, *pb.Vertex]) error
	// PutVerticesWithExpirationIfAbsentChecked writes each vertex only when no live
	// vertex exists at its key (SET NX, #896). It returns the number written
	// and the keys skipped because a live vertex already existed. Used by the
	// LOCAL write path when replication is off (clock nil).
	PutVerticesWithExpirationIfAbsentChecked(items []graphcache.VertexItem[string, *pb.Vertex]) (written int, skipped []string, err error)
	// PutVerticesWithExpirationIfAbsentHLCChecked is the replication-aware sibling:
	// it stamps each accepted write with ts so the origin participates in LWW,
	// and returns the request-order indices of items that passed the absence
	// check (so the caller can replicate only that subset) plus the skipped
	// keys. Used when replication is enabled (clock non-nil).
	PutVerticesWithExpirationIfAbsentHLCChecked(items []graphcache.VertexItem[string, *pb.Vertex], ts hlc.Timestamp) (writtenIdx []int, skipped []string, err error)
	DeleteVertices(keys []string) int

	// edge reads/writes
	GetEdgeDetail(tail, head string) (float32, time.Time, bool)
	AddEdgesWithExpiration(items []graphcache.EdgeItem[string])
	// AddEdgesWithExpirationContrib is the dedup-aware batch sibling of
	// AddEdgesWithExpiration: a per-item non-zero ContribID makes that
	// contribution idempotent (a retried batch is an exact no-op). It
	// returns, index-aligned with items, the post-apply LIVE weight sum for
	// each edge (#897) plus the count of items suppressed by a matching live
	// ContribID. Items with a zero ContribID keep legacy additive semantics.
	AddEdgesWithExpirationContrib(items []graphcache.EdgeItem[string]) (effective []float32, deduped int)
	PutEdgesWithExpiration(items []graphcache.EdgeItem[string])
	DeleteEdges(keys []graphcache.EdgeKey[string]) int

	// replicated-write entry points used by ApplyMutation (#182).
	//
	// AddEdgeWithExpirationContrib records an additive edge contribution
	// stamped with contribID; re-applying a mutation with the same id is
	// a no-op (returns false). Local non-replicated writes keep using
	// AddEdgesWithExpiration with a zero contribID.
	//
	// PutVertexWithExpirationHLC / PutEdgeWithExpirationHLC compare ts
	// against the stored last-write HLC and silently drop strictly-older
	// writes (LWW). Equal-ts writes apply (idempotent for value-equal
	// payloads).
	AddEdgeWithExpirationContrib(tail, head string, w float32, expiration time.Time, contribID graphcache.ContribID) bool
	PutVertexWithExpirationHLC(key string, value *pb.Vertex, expiration time.Time, ts hlc.Timestamp) bool
	PutEdgeWithExpirationHLC(tail, head string, w float32, expiration time.Time, ts hlc.Timestamp) bool

	// batch HLC siblings used by the LOCAL write path (PutVertices /
	// PutEdges / AddEdges) when replication is enabled. They stamp every
	// item in the batch with the SAME ts the originating mutation is logged
	// under, so the origin's own writes participate in LWW (vertices, edges)
	// or edge-tombstone fencing (additive contributions) on equal footing
	// with the values its peers later apply from that log entry. Without the
	// watermark a concurrently-written strictly-older Put replayed from a
	// peer would clobber the origin's newer value on the origin alone while
	// every other replica kept the newer one — permanent divergence. The
	// local path keeps using the non-HLC PutVerticesWithExpiration /
	// PutEdgesWithExpiration / AddEdgesWithExpirationContrib when replication
	// is off (clock nil) so non-replicated workloads pay nothing.
	// AddEdgesWithExpirationContribHLC returns, index-aligned with items, the
	// post-apply LIVE weight sum for each edge (#897) plus the count of items
	// that added no weight (tombstone-dropped or ContribID-deduped).
	//
	// Since #840 these batch methods also serve the replication apply path
	// (ApplyMutation), which previously looped the singular HLC methods. The
	// Put* variants return the number of items rejected by the tombstone
	// fence or LWW watermark — the same set the singular methods report as
	// applied=false — so the apply path fires its clamp-reject metric once
	// per rejected item with unchanged meaning.
	PutVerticesWithExpirationHLC(items []graphcache.VertexItem[string, *pb.Vertex], ts hlc.Timestamp) int
	PutVerticesWithExpirationHLCChecked(items []graphcache.VertexItem[string, *pb.Vertex], ts hlc.Timestamp) (int, error)
	PutEdgesWithExpirationHLC(items []graphcache.EdgeItem[string], ts hlc.Timestamp) int
	AddEdgesWithExpirationContribHLC(items []graphcache.EdgeItem[string], ts hlc.Timestamp) (effective []float32, deduped int)

	// tombstone-aware Delete*/Add* entry points used by ApplyMutation
	// when LANTERN_TOMBSTONE_TTL is configured (#183). DeleteVertexHLC,
	// DeleteVerticesHLC, DeleteEdgeHLC, DeleteEdgesHLC and
	// DeleteByPrefixHLC stamp a tombstone keyed on the deleted entry so
	// late-arriving Put*/Add* with strictly-older HLC are rejected for
	// the tombstone window. AddEdgeWithExpirationContribHLC is the HLC
	// sibling of AddEdgeWithExpirationContrib that consults the edge
	// tombstone store before applying.
	AddEdgeWithExpirationContribHLC(tail, head string, w float32, expiration time.Time, contribID graphcache.ContribID, ts hlc.Timestamp) bool
	DeleteVertexHLC(key string, ts hlc.Timestamp, expiration time.Time) bool
	DeleteVerticesHLC(keys []string, ts hlc.Timestamp, expiration time.Time) int
	DeleteEdgeHLC(tail, head string, ts hlc.Timestamp, expiration time.Time) bool
	DeleteEdgesHLC(keys []graphcache.EdgeKey[string], ts hlc.Timestamp, expiration time.Time) int
	DeleteByPrefixHLC(ctx context.Context, prefix string, limit uint32, ts hlc.Timestamp, expiration time.Time) (int, error)
	DeleteEdgesByPrefixHLC(ctx context.Context, tailPrefix, headPrefix string, limit int, ts hlc.Timestamp, expiration time.Time) (int, error)

	// neighborhood traversal. selectSmallest steers the per-hop top-k
	// pruning: the k smallest-weight edges are kept when true, the k
	// largest when false (#560), so the caller's Objective governs which
	// edges survive both pruning and any later reduction. keep is an
	// optional frontier predicate (#601): when non-nil, a head is admitted
	// to the frontier only if keep(head) is true, evaluated BEFORE scoring
	// and the per-hop top-k prune so the surviving edges are the k strongest
	// *matching* neighbours. The seed is exempt. A nil keep accepts every
	// head (unfiltered traversal).
	NeighborWithExpirationsContext(
		ctx context.Context,
		seed string,
		step, k int,
		weighting graphcache.EdgeWeighting,
		selectSmallest bool,
		keep func(string) bool,
	) (*coregraph.Graph[string, *pb.Vertex], map[string]map[string]time.Time, error)

	// LocalCommunityWithWorkBudgetContext extracts the conductance-optimal local
	// community around seed (#845): the shared PPR forward-push followed by
	// a sweep cut (PageRank-Nibble). maxSize is an UPPER BOUND; the service
	// resolves wire zero to its configured result cap before calling this
	// backend. alpha/epsilon follow the PPR defaults; weighting steers
	// the walk and the sweep AND is applied to the returned edge weights
	// (WeightingRaw = the verbatim stored weight) — the returned graph is
	// the INDUCED SUBGRAPH on the selected members with those weights and
	// their expirations, in the same (graph, expirations) shape as the BFS
	// path. keep scopes both the walk and the community (seed exempt).
	LocalCommunityWithWorkBudgetContext(
		ctx context.Context,
		seed string,
		maxSize int,
		alpha, epsilon float64,
		weighting graphcache.EdgeWeighting,
		keep func(string) bool,
		budget graphcache.PPRWorkBudget,
	) (*coregraph.Graph[string, *pb.Vertex], map[string]map[string]time.Time, error)

	// PersonalizedPageRankWithWorkBudgetContext computes the seed-anchored Personalized
	// PageRank vector via Andersen–Chung–Lang forward-push (#801) and returns
	// it as a one-hop "relevance star": g.Edges[seed][v] carries v's PPR mass
	// pi[v] for the topN highest-mass vertices, with the seed excluded from its
	// own star. The service resolves wire top_n=0 to its configured result cap.
	// This is a distinct traversal path, NOT a post-traversal reduction of the BFS
	// neighbourhood — alpha is the restart/teleport-to-seed probability and
	// epsilon the residual threshold; out-of-range alpha (<=0 or >=1) and
	// non-positive epsilon fall back to the core defaults. weighting transforms
	// each out-edge BEFORE row-normalisation (so BM25-weighted PPR composes for
	// free, #800); keep is the same optional frontier predicate as the BFS path
	// (a non-matching head never accrues mass; the seed is exempt; nil accepts
	// every head). There is no expirations map — the star edges are synthetic
	// relevance weights, not stored edges.
	PersonalizedPageRankWithWorkBudgetContext(
		ctx context.Context,
		seed string,
		topN int,
		alpha, epsilon float64,
		weighting graphcache.EdgeWeighting,
		keep func(string) bool,
		budget graphcache.PPRWorkBudget,
	) (*coregraph.Graph[string, *pb.Vertex], error)

	// prefix scan / count / delete. ScanByPrefixPage (#836) invokes fn for
	// each live vertex whose key starts with prefix AND sorts strictly
	// after `after` (ascending) or strictly before it (descending, #898),
	// in the requested key order, up to limit rows (limit <= 0
	// = unbounded); fn returns false to stop early. desc selects reverse
	// lexicographic order; in descending mode `after` is the previous
	// page's smallest key and the walk resumes strictly below it. more
	// reports whether further matches exist past the page — the handler's
	// next-cursor signal. CountByPrefix counts only live matching vertices.
	// DeleteByPrefix removes matching vertices up to limit (limit <= 0
	// means unlimited) and returns how many were deleted.
	ScanByPrefixPage(ctx context.Context, prefix, after string, limit int, desc bool, fn func(projected string, key string, value *pb.Vertex) bool) (more, ok bool)
	CountByPrefix(prefix string) int
	DeleteByPrefix(ctx context.Context, prefix string, limit int) int

	// TopVerticesByDegree ranks the live vertices whose key starts with
	// prefix by their degree in dir and returns the top k in descending
	// order of the ranking metric (weighted edge-weight sum when weighted,
	// else live edge count). k <= 0 returns nil. Counts honour the
	// live-visibility rule (#750) and are point-in-time best-effort (#900).
	// The handler enforces the non-empty-prefix guard and the k clamp; this
	// method imposes neither.
	TopVerticesByDegree(prefix string, k int, dir graphcache.DegreeDirection, weighted bool) []graphcache.DegreeEntry[string]

	// SearchVertices returns vertices ranked by full-text relevance over
	// their indexed content, optionally scoped to keyPrefix, capped at
	// limit (limit <= 0 returns nil). The returned slice uses the stable
	// production order (score DESC, raw key ASC). It returns nil when the
	// search index is disabled, the query has no analysable terms, or nothing
	// matches — the three are indistinguishable here, so the handler decides
	// FAILED_PRECONDITION from its own SearchConfig.Enabled flag, not this return
	// value (#624).
	SearchVertices(query string, limit int, keyPrefix string) []search.Result[string]
	SearchVerticesMatch(query string, limit int, keyPrefix string, opts search.MatchOptions, phrase bool) []search.Result[string]
	SearchVerticesMatchContext(ctx context.Context, query string, limit int, keyPrefix string, opts search.MatchOptions, phrase bool, budget search.Budget) ([]search.Result[string], search.Stats, error)
	SearchVerticesSnapshotContext(ctx context.Context, query string, limit int, keyPrefix string, opts search.MatchOptions, phrase bool, budget search.Budget) ([]graphcache.SearchSnapshotHit[string, *pb.Vertex], search.Stats, error)

	// edge-side prefix scan. ScanEdgesByPrefixPage (#836) invokes fn for
	// each live edge whose tail starts with tailPrefix AND whose head
	// starts with headPrefix, in ascending (tail, head) order, resuming
	// strictly after the (afterTail, afterHead) pair and collecting at
	// most limit rows (limit <= 0 = unbounded). Either prefix may be
	// empty to disable the corresponding filter. more mirrors
	// ScanByPrefixPage. DeleteEdgesByPrefix (#899) removes matching edges
	// up to limit (limit <= 0 = unbounded) and returns how many were
	// deleted; the whole-graph-wipe guard (at least one prefix non-empty)
	// is enforced by the handler, not here.
	ScanEdgesByPrefixPage(ctx context.Context, tailPrefix, headPrefix, afterTail, afterHead string, limit int, fn func(tailProjected string, tail string, headProjected string, head string, weight float32, expiration time.Time) bool) (more, ok bool)
	DeleteEdgesByPrefix(ctx context.Context, tailPrefix, headPrefix string, limit int) int

	// background GC loop driven by LanternServer.
	Watch(ctx context.Context, interval time.Duration)

	// Snapshot* return materialised dumps of the live state for the
	// replication bootstrap RPC (#184). Both calls are taken under the
	// GraphCache write lock and are intended to be called once per
	// bootstrapping peer (not on the hot path). The returned slices own
	// their backing storage and are safe to iterate without further
	// locking.
	SnapshotVertices() []graphcache.SnapshotVertex[string, *pb.Vertex]
	SnapshotEdges() []graphcache.SnapshotEdge[string]

	// SnapshotGraph materialises both vertices and edges under a SINGLE
	// GraphCache lock (#689), so a whole-graph backup observes one
	// point-in-time. Used by BackupSnapshot.
	SnapshotGraph() graphcache.GraphSnapshot[string, *pb.Vertex]

	// VertexCount and EdgeCount return the current number of live entries
	// in the underlying graph cache. Backed by index sizes — O(1) and
	// safe to call from any RPC. Returned values are eventually-consistent
	// snapshots (may include not-yet-GC'd expired entries, bounded by the
	// LANTERN_GC_INTERVAL_SECONDS tick). Used by GetServerStatus (#314)
	// to populate the admin UI's at-a-glance counters.
	VertexCount() int
	EdgeCount() int
	SearchIndexMemoryStats() search.IndexMemoryStats
}
