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

// fakeBackend is a hand-rolled Backend stub exercised by tests that want to
// drive specific success/error paths without standing up a real GraphCache.
// Unset fields default to zero/empty so each test fills only what it needs.
type fakeBackend struct {
	vertices    map[string]*pb.Vertex
	edges       map[string]map[string]float32
	neighborErr error

	// neighborBlockUntilCtxDone makes NeighborWithExpirationsContext park on
	// <-ctx.Done() and return ctx.Err(), simulating a traversal that outlives
	// its budget so handler tests can pin the #842 server-side timeout.
	// lastNeighborHadDeadline records whether the handler-supplied ctx
	// carried a deadline at all (the budget-disabled assertion).
	neighborBlockUntilCtxDone bool
	lastNeighborHadDeadline   bool

	// captured args from the most recent NeighborWithExpirationsContext
	// call, so tests can assert how the Illuminate handler maps Objective
	// onto the per-hop pruning direction (#560) and threads the optional
	// frontier predicate (#601).
	neighborCalls           int
	lastNeighborWeighting   graphcache.EdgeWeighting
	lastNeighborSelectSmall bool
	lastNeighborKeep        func(string) bool
	lastNeighborStep        int
	lastNeighborK           int

	// LocalCommunityContext bookkeeping (#845).
	communityCalls       int
	lastCommunityMaxSize int
	lastCommunityAlpha   float64
	lastCommunityEpsilon float64
	lastCommunityWeight  graphcache.EdgeWeighting
	lastCommunityKeep    func(string) bool
	communityGraph       *coregraph.Graph[string, *pb.Vertex]
	communityExpirations map[string]map[string]time.Time
	communityErr         error

	// captured args from the most recent PersonalizedPageRankContext call
	// (#801), so tests can assert the Illuminate handler routes algorithm=ppr
	// to the forward-push path (not the BFS+reduction one), resolves the
	// restart_prob/epsilon defaults, threads weighting + keep, and passes k
	// through as the top-N cap.
	pprCalls         int
	lastPPRSeed      string
	lastPPRTopN      int
	lastPPRAlpha     float64
	lastPPREpsilon   float64
	lastPPRWeighting graphcache.EdgeWeighting
	lastPPRKeep      func(string) bool
	pprErr           error

	putVerticesCalls int
	deleteVertices   int
	addEdgesCalls    int
	putEdgesCalls    int
	deleteEdges      int

	// #624 SearchVertices wiring. searchResults is returned verbatim by
	// SearchVertices so handler tests can drive ranking/mapping without a
	// real index; the call captures its args for clamp/prefix assertions.
	searchResults    []search.Result[string]
	searchCalls      int
	lastSearchQuery  string
	lastSearchLimit  int
	lastSearchPrefix string
	lastSearchOpts   search.MatchOptions
	lastSearchPhrase bool

	// #588 idempotent-AddEdge wiring capture. The canonical AddEdges path
	// now calls AddEdgesWithExpirationContrib; tests assert contrib_ids are
	// mapped index-aligned onto EdgeItem.ContribID and that the returned
	// dedup count is forwarded to HotPathMetrics.OnEdgeContribDeduped.
	addEdgesContribCalls int
	lastAddEdgesItems    []graphcache.EdgeItem[string]
	dedupReturn          int
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		vertices: map[string]*pb.Vertex{},
		edges:    map[string]map[string]float32{},
	}
}

func (f *fakeBackend) GetVertex(key string) (*pb.Vertex, bool) {
	v, ok := f.vertices[key]
	return v, ok
}

func (f *fakeBackend) PutVerticesWithExpiration(items []graphcache.VertexItem[string, *pb.Vertex]) {
	f.putVerticesCalls++
	for _, it := range items {
		f.vertices[it.Key] = it.Value
	}
}

func (f *fakeBackend) DeleteVertices(keys []string) int {
	n := 0
	for _, k := range keys {
		if _, ok := f.vertices[k]; ok {
			delete(f.vertices, k)
			n++
		}
	}
	f.deleteVertices += n
	return n
}

func (f *fakeBackend) GetEdgeDetail(tail, head string) (float32, time.Time, bool) {
	if row, ok := f.edges[tail]; ok {
		if w, ok := row[head]; ok {
			return w, time.Time{}, true
		}
	}
	return 0, time.Time{}, false
}

func (f *fakeBackend) AddEdgesWithExpiration(items []graphcache.EdgeItem[string]) {
	f.addEdgesCalls++
	for _, it := range items {
		if f.edges[it.Tail] == nil {
			f.edges[it.Tail] = map[string]float32{}
		}
		f.edges[it.Tail][it.Head] += it.Weight
	}
}

// AddEdgesWithExpirationContrib is the path the production AddEdges handler
// now drives (#588). The fake applies weight additively (mirroring the
// zero-ContribID legacy path), captures the items so tests can assert the
// contrib_id mapping, and returns the configurable dedupReturn so the
// OnEdgeContribDeduped metric wiring is observable. Real dedup convergence
// is covered by the core tests and tests/integration.
func (f *fakeBackend) AddEdgesWithExpirationContrib(items []graphcache.EdgeItem[string]) ([]float32, int) {
	f.addEdgesContribCalls++
	f.lastAddEdgesItems = items
	effective := make([]float32, len(items))
	for i, it := range items {
		if f.edges[it.Tail] == nil {
			f.edges[it.Tail] = map[string]float32{}
		}
		f.edges[it.Tail][it.Head] += it.Weight
		effective[i] = f.edges[it.Tail][it.Head]
	}
	return effective, f.dedupReturn
}

func (f *fakeBackend) PutEdgesWithExpiration(items []graphcache.EdgeItem[string]) {
	f.putEdgesCalls++
	for _, it := range items {
		if f.edges[it.Tail] == nil {
			f.edges[it.Tail] = map[string]float32{}
		}
		f.edges[it.Tail][it.Head] = it.Weight
	}
}

func (f *fakeBackend) DeleteEdges(keys []graphcache.EdgeKey[string]) int {
	n := 0
	for _, k := range keys {
		if row, ok := f.edges[k.Tail]; ok {
			if _, ok := row[k.Head]; ok {
				delete(row, k.Head)
				n++
			}
		}
	}
	f.deleteEdges += n
	return n
}

func (f *fakeBackend) NeighborWithExpirationsContext(
	ctx context.Context,
	seed string,
	step, k int,
	weighting graphcache.EdgeWeighting,
	selectSmallest bool,
	keep func(string) bool,
) (*coregraph.Graph[string, *pb.Vertex], map[string]map[string]time.Time, error) {
	f.neighborCalls++
	f.lastNeighborWeighting = weighting
	f.lastNeighborSelectSmall = selectSmallest
	f.lastNeighborKeep = keep
	f.lastNeighborStep, f.lastNeighborK = step, k
	_, f.lastNeighborHadDeadline = ctx.Deadline()
	if f.neighborBlockUntilCtxDone {
		<-ctx.Done()
		return nil, nil, ctx.Err()
	}
	if f.neighborErr != nil {
		return nil, nil, f.neighborErr
	}
	g := coregraph.NewGraph[string, *pb.Vertex]()
	if v, ok := f.vertices[seed]; ok {
		g.Vertices[seed] = v
	}
	return g, map[string]map[string]time.Time{}, nil
}

func (f *fakeBackend) LocalCommunityContext(
	_ context.Context,
	seed string,
	maxSize int,
	alpha, epsilon float64,
	weighting graphcache.EdgeWeighting,
	keep func(string) bool,
) (*coregraph.Graph[string, *pb.Vertex], map[string]map[string]time.Time, error) {
	f.communityCalls++
	f.lastCommunityMaxSize = maxSize
	f.lastCommunityAlpha = alpha
	f.lastCommunityEpsilon = epsilon
	f.lastCommunityWeight = weighting
	f.lastCommunityKeep = keep
	if f.communityErr != nil {
		return nil, nil, f.communityErr
	}
	if f.communityGraph != nil {
		return f.communityGraph, f.communityExpirations, nil
	}
	g := coregraph.NewGraph[string, *pb.Vertex]()
	g.Vertices[seed] = &pb.Vertex{Key: seed}
	return g, nil, nil
}

func (f *fakeBackend) PersonalizedPageRankContext(
	ctx context.Context,
	seed string,
	topN int,
	alpha, epsilon float64,
	weighting graphcache.EdgeWeighting,
	keep func(string) bool,
) (*coregraph.Graph[string, *pb.Vertex], error) {
	f.pprCalls++
	f.lastPPRSeed = seed
	f.lastPPRTopN = topN
	f.lastPPRAlpha = alpha
	f.lastPPREpsilon = epsilon
	f.lastPPRWeighting = weighting
	f.lastPPRKeep = keep
	if f.pprErr != nil {
		return nil, f.pprErr
	}
	g := coregraph.NewGraph[string, *pb.Vertex]()
	if v, ok := f.vertices[seed]; ok {
		g.Vertices[seed] = v
	}
	// Synthesise a relevance star over the seed's stored out-edges (honouring
	// the keep frontier predicate) so handler tests can assert the g.Edges →
	// pb.Edge mapping without running a real forward-push.
	if heads := f.edges[seed]; len(heads) > 0 {
		star := make(map[string]float32, len(heads))
		for head, w := range heads {
			if keep != nil && !keep(head) {
				continue
			}
			star[head] = w
			if v, ok := f.vertices[head]; ok {
				g.Vertices[head] = v
			}
		}
		if len(star) > 0 {
			g.Edges[seed] = star
		}
	}
	return g, nil
}

func (f *fakeBackend) Watch(ctx context.Context, interval time.Duration) {
	<-ctx.Done()
}

// Prefix-scan stubs. Tests that exercise the prefix RPCs use the real
// GraphCache via the bufconn harness; the fake just walks its in-memory
// map in lexicographic order so unit tests can still hit the wrappers
// without standing up the cache.
func (f *fakeBackend) ScanByPrefixPage(_ context.Context, prefix, after string, limit int, fn func(string, string, *pb.Vertex) bool) (bool, bool) {
	keys := make([]string, 0, len(f.vertices))
	for k := range f.vertices {
		if prefix != "" && !(len(k) >= len(prefix) && k[:len(prefix)] == prefix) {
			continue
		}
		if after != "" && k <= after {
			continue
		}
		keys = append(keys, k)
	}
	// Sort to match the radix index's lexicographic walk order.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	more := false
	if limit > 0 && len(keys) > limit {
		more = true
		keys = keys[:limit]
	}
	for _, k := range keys {
		if !fn(k, k, f.vertices[k]) {
			return more, false
		}
	}
	return more, true
}

func (f *fakeBackend) CountByPrefix(prefix string) int {
	n := 0
	for k := range f.vertices {
		if prefix == "" || (len(k) >= len(prefix) && k[:len(prefix)] == prefix) {
			n++
		}
	}
	return n
}

// SearchVertices returns the pre-seeded searchResults verbatim and records
// the args so handler tests can assert the clamped limit and prefix are
// threaded through. Real ranking/index behaviour is covered by the core
// graphcache tests and the bufconn integration suite.
func (f *fakeBackend) SearchVertices(query string, limit int, keyPrefix string) []search.Result[string] {
	f.searchCalls++
	f.lastSearchQuery = query
	f.lastSearchLimit = limit
	f.lastSearchPrefix = keyPrefix
	return f.searchResults
}

// SearchVerticesMatch records the match options and phrase flag alongside the
// base args and returns the same pre-seeded results, so handler tests can assert
// the options threaded through (#892).
func (f *fakeBackend) SearchVerticesMatch(query string, limit int, keyPrefix string, opts search.MatchOptions, phrase bool) []search.Result[string] {
	f.lastSearchOpts = opts
	f.lastSearchPhrase = phrase
	return f.SearchVertices(query, limit, keyPrefix)
}

func (f *fakeBackend) DeleteByPrefix(_ context.Context, prefix string, limit int) int {
	victims := []string{}
	for k := range f.vertices {
		if prefix == "" || (len(k) >= len(prefix) && k[:len(prefix)] == prefix) {
			victims = append(victims, k)
			if limit > 0 && len(victims) >= limit {
				break
			}
		}
	}
	for _, k := range victims {
		delete(f.vertices, k)
	}
	f.deleteVertices += len(victims)
	return len(victims)
}

func (f *fakeBackend) ScanEdgesByPrefixPage(_ context.Context, tailPrefix, headPrefix, afterTail, afterHead string, limit int,
	fn func(string, string, string, string, float32, time.Time) bool,
) (bool, bool) {
	type row struct{ t, h string }
	var rows []row
	for t, hs := range f.edges {
		if tailPrefix != "" && !(len(t) >= len(tailPrefix) && t[:len(tailPrefix)] == tailPrefix) {
			continue
		}
		for h := range hs {
			if headPrefix != "" && !(len(h) >= len(headPrefix) && h[:len(headPrefix)] == headPrefix) {
				continue
			}
			if afterTail != "" || afterHead != "" {
				if t < afterTail || (t == afterTail && h <= afterHead) {
					continue
				}
			}
			rows = append(rows, row{t, h})
		}
	}
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && (rows[j-1].t > rows[j].t || (rows[j-1].t == rows[j].t && rows[j-1].h > rows[j].h)); j-- {
			rows[j-1], rows[j] = rows[j], rows[j-1]
		}
	}
	more := false
	if limit > 0 && len(rows) > limit {
		more = true
		rows = rows[:limit]
	}
	for _, r := range rows {
		if !fn(r.t, r.t, r.h, r.h, f.edges[r.t][r.h], time.Time{}) {
			return more, false
		}
	}
	return more, true
}

// Compile-time check that fakeBackend really satisfies Backend.
var _ Backend = (*fakeBackend)(nil)

// Replicated-write entry points (#182). The fake mirrors the production
// semantics with a minimal in-memory model so service-level tests that
// touch ApplyMutation can run against the fake without spinning up a
// real GraphCache. It does not enforce HLC/ContribID dedup — tests that
// care about convergence use the real backend.
func (f *fakeBackend) AddEdgeWithExpirationContrib(tail, head string, w float32, _ time.Time, _ graphcache.ContribID) bool {
	if f.edges[tail] == nil {
		f.edges[tail] = map[string]float32{}
	}
	f.edges[tail][head] += w
	return true
}

func (f *fakeBackend) PutVertexWithExpirationHLC(key string, value *pb.Vertex, _ time.Time, _ hlc.Timestamp) bool {
	f.vertices[key] = value
	return true
}

func (f *fakeBackend) PutEdgeWithExpirationHLC(tail, head string, w float32, _ time.Time, _ hlc.Timestamp) bool {
	if f.edges[tail] == nil {
		f.edges[tail] = map[string]float32{}
	}
	f.edges[tail][head] = w
	return true
}

// batch HLC siblings driven by the LOCAL write path when replication is
// enabled. The fake collapses the HLC bookkeeping into the non-HLC batch
// siblings so the existing call counters and captured items stay observable
// whether or not the service takes the replicated branch; real LWW / tombstone
// convergence is covered by the core and integration tests against the real
// backend.
func (f *fakeBackend) PutVerticesWithExpirationHLC(items []graphcache.VertexItem[string, *pb.Vertex], _ hlc.Timestamp) int {
	f.PutVerticesWithExpiration(items)
	return 0
}

func (f *fakeBackend) PutEdgesWithExpirationHLC(items []graphcache.EdgeItem[string], _ hlc.Timestamp) int {
	f.PutEdgesWithExpiration(items)
	return 0
}

func (f *fakeBackend) AddEdgesWithExpirationContribHLC(items []graphcache.EdgeItem[string], _ hlc.Timestamp) ([]float32, int) {
	return f.AddEdgesWithExpirationContrib(items)
}

// Tombstone-aware entry points (#183). The fake intentionally collapses
// the tombstone bookkeeping into the underlying delete; service-level
// tests that exercise tombstone semantics use the real backend.
func (f *fakeBackend) AddEdgeWithExpirationContribHLC(tail, head string, w float32, exp time.Time, c graphcache.ContribID, _ hlc.Timestamp) bool {
	return f.AddEdgeWithExpirationContrib(tail, head, w, exp, c)
}

func (f *fakeBackend) DeleteVertexHLC(key string, _ hlc.Timestamp, _ time.Time) bool {
	return f.DeleteVertices([]string{key}) > 0
}

func (f *fakeBackend) DeleteVerticesHLC(keys []string, _ hlc.Timestamp, _ time.Time) int {
	return f.DeleteVertices(keys)
}

func (f *fakeBackend) DeleteEdgeHLC(tail, head string, _ hlc.Timestamp, _ time.Time) bool {
	return f.DeleteEdges([]graphcache.EdgeKey[string]{{Tail: tail, Head: head}}) > 0
}

func (f *fakeBackend) DeleteEdgesHLC(keys []graphcache.EdgeKey[string], _ hlc.Timestamp, _ time.Time) int {
	return f.DeleteEdges(keys)
}

func (f *fakeBackend) DeleteByPrefixHLC(ctx context.Context, prefix string, limit uint32, _ hlc.Timestamp, _ time.Time) (int, error) {
	lim := 0
	if limit > 0 {
		lim = int(limit)
	}
	return f.DeleteByPrefix(ctx, prefix, lim), nil
}

// Snapshot* implement the bootstrap surface (#184). The fake backend
// returns the in-memory vertex/edge maps as flat slices with zero HLC
// stamps and a single zero-ContribID contribution per edge — enough for
// service-level wiring tests; convergence tests use the real backend.
func (f *fakeBackend) SnapshotVertices() []graphcache.SnapshotVertex[string, *pb.Vertex] {
	out := make([]graphcache.SnapshotVertex[string, *pb.Vertex], 0, len(f.vertices))
	for k, v := range f.vertices {
		out = append(out, graphcache.SnapshotVertex[string, *pb.Vertex]{Key: k, Value: v})
	}
	return out
}

func (f *fakeBackend) SnapshotGraph() graphcache.GraphSnapshot[string, *pb.Vertex] {
	return graphcache.GraphSnapshot[string, *pb.Vertex]{
		Vertices: f.SnapshotVertices(),
		Edges:    f.SnapshotEdges(),
	}
}

func (f *fakeBackend) SnapshotEdges() []graphcache.SnapshotEdge[string] {
	var out []graphcache.SnapshotEdge[string]
	for tail, heads := range f.edges {
		for head, w := range heads {
			out = append(out, graphcache.SnapshotEdge[string]{
				Tail: tail,
				Head: head,
				Contributions: []graphcache.SnapshotContribution{{
					Weight: w,
				}},
			})
		}
	}
	return out
}

// VertexCount returns the in-memory vertex map size. Sufficient for the
// service-layer tests that exercise GetServerStatus (#314) without
// standing up a real GraphCache.
func (f *fakeBackend) VertexCount() int {
	return len(f.vertices)
}

// EdgeCount returns the total number of (tail, head) pairs across the
// fake adjacency map.
func (f *fakeBackend) EdgeCount() int {
	n := 0
	for _, heads := range f.edges {
		n += len(heads)
	}
	return n
}
