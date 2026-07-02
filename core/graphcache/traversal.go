package graphcache

import (
	"context"
	"math"
	"runtime"
	"sync"
	"time"

	"github.com/anaregdesign/lantern/core/collection/pq"
	"github.com/anaregdesign/lantern/core/graph"
	"github.com/anaregdesign/lantern/core/search"
)

// EdgeWeighting selects the transform applied to a raw edge weight BEFORE the
// directional per-hop top/bottom-k prune (#410, #800). It replaces the
// historical tfidf bool threaded through the Neighbor* surface so a third
// strategy — and any future one — is a single enum arm rather than another
// boolean parameter.
type EdgeWeighting uint8

const (
	// WeightingRaw uses the live additive edge weight verbatim.
	WeightingRaw EdgeWeighting = iota
	// WeightingTFIDF applies the crude hub-suppressor w / log2(1 + df(head)).
	// It is cheap and O(1) per edge but corpus-size-blind; kept alongside
	// BM25 for its distinct, faster semantics.
	WeightingTFIDF
	// WeightingBM25 re-scores with Okapi BM25 over the per-vertex out-edge
	// distribution: TF = the live edge weight, DF = df(head) (distinct tails
	// into head), N = number of tails, DocLen = the tail's out-degree, and
	// AvgLen = mean out-degree. It runs through the shared search.BM25Score
	// kernel so graph edge weighting stays numerically identical to full-text
	// SearchVertices ranking, adding TF saturation, document-length
	// normalization, and a real N-aware IDF that TFIDF lacks.
	WeightingBM25
)

// scoreEdge applies the EdgeWeighting transform to one raw edge weight `sum`
// for a tail→head edge. headID and docLen (the tail's raw out-degree) feed the
// df- and length-dependent transforms; bm25N/bm25AvgLen are the corpus stats
// consulted only for WeightingBM25. It is the single scoring kernel shared by
// the per-hop Neighbor prune (neighborContext) and the PPR forward-push
// (PersonalizedPageRankContext) so both paths weight edges identically. The
// caller must hold GraphCache.mu — docFreq reads c.edges without taking its own
// lock, under the same contract as headsOf.
func (c *GraphCache[S, T]) scoreEdge(weighting EdgeWeighting, sum float32, headID vertexID, docLen, bm25N int, bm25AvgLen float64) float32 {
	switch weighting {
	case WeightingTFIDF:
		return sum / float32(math.Log2(float64(1+c.edges.docFreq(headID))))
	case WeightingBM25:
		return float32(search.BM25Score(
			float64(sum), bm25AvgLen,
			c.edges.docFreq(headID), bm25N, docLen,
			search.DefaultBM25K1, search.DefaultBM25B,
		))
	default: // WeightingRaw
		return sum
	}
}

// Neighbor walks the graph from seed and returns the visited subgraph. The
// per-hop top-k pruning keeps the k largest-weight edges when selectSmallest
// is false and the k smallest-weight edges when it is true (#560) — the
// caller picks the direction that matches its Objective so a cost-minimiser
// is not handed the costliest edges. weighting re-scores edge weights BEFORE
// the directional top/bottom-k selection (#800).
//
// keep is an optional frontier predicate (nil = accept all): a candidate head
// is expanded into the result only when keep(head) is true. It is applied at
// frontier materialisation, BEFORE scoring and the directional top/bottom-k
// prune, so top-k selects the k best *accepted* neighbours per hop. The seed
// is the anchor exemption — it is always retained and never passed through
// keep. Because the next-hop frontier is derived from the surviving edges, a
// matching vertex reachable only through a rejected "bridge" is not reached
// (induced-subgraph semantics). core stays generic: keep is just a predicate
// over S; the concrete prefix/string logic lives in the caller.
func (c *GraphCache[S, T]) Neighbor(seed S, step int, k int, weighting EdgeWeighting, selectSmallest bool, keep func(S) bool) *graph.Graph[S, T] {
	g, _ := c.NeighborContext(context.Background(), seed, step, k, weighting, selectSmallest, keep)
	return g
}

// NeighborContext is the context-aware variant of Neighbor. It returns
// ctx.Err() as soon as the context is cancelled or its deadline has expired
// — checked between BFS expansion steps — so handlers can short-circuit
// large traversals when the caller has given up. keep is the optional frontier
// predicate documented on Neighbor (nil = accept all).
func (c *GraphCache[S, T]) NeighborContext(ctx context.Context, seed S, step int, k int, weighting EdgeWeighting, selectSmallest bool, keep func(S) bool) (*graph.Graph[S, T], error) {
	g, _, err := c.neighborContext(ctx, seed, step, k, weighting, selectSmallest, keep, false)
	return g, err
}

// NeighborWithExpirationsContext returns the same subgraph as
// NeighborContext together with a parallel expirations map keyed by
// (tail, head). Both are computed under a single RLock so handlers can
// compose responses without re-acquiring the cache lock per edge.
//
// The expirations map only contains entries for edges that ended up in
// the returned graph; a missing or zero value means the edge has no
// known expiration. keep is the optional frontier predicate documented on
// Neighbor (nil = accept all).
func (c *GraphCache[S, T]) NeighborWithExpirationsContext(ctx context.Context, seed S, step int, k int, weighting EdgeWeighting, selectSmallest bool, keep func(S) bool) (*graph.Graph[S, T], map[S]map[S]time.Time, error) {
	return c.neighborContext(ctx, seed, step, k, weighting, selectSmallest, keep, true)
}

func (c *GraphCache[S, T]) neighborContext(ctx context.Context, seed S, step int, k int, weighting EdgeWeighting, selectSmallest bool, keep func(S) bool, collectExpirations bool) (*graph.Graph[S, T], map[S]map[S]time.Time, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	g := graph.NewGraph[S, T]()

	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	if v, ok := c.vertices.Get(seed); !ok {
		return g, nil, nil
	} else {
		g.Vertices[seed] = v
	}

	var mu sync.Mutex
	// seen marks every vertex ever queued into a frontier so each vertex is
	// expanded at most once; the frontier slices below replace the historical
	// grow-only targets map, whose full rescan each step cost
	// O(steps x discovered) (#838). Both are accessed only from the main
	// goroutine (between wg.Wait barriers), so plain structures suffice.
	// mu protects concurrent writes to g.Edges and (when requested)
	// expirations.
	seen := map[S]struct{}{seed: {}}
	frontier := []S{seed}
	// expirations is allocated up front so per-tail goroutines can publish
	// their surviving heads without an extra coordination pass.
	var expirations map[S]map[S]time.Time
	if collectExpirations {
		expirations = make(map[S]map[S]time.Time)
	}
	// We deliberately do NOT clone the entire edge table here: with c.mu
	// held read-locked, no writer can mutate c.edges.tf, so the per-tail
	// edgeCache.headsOf / docFreq accessors are safe to call directly.
	// This turns the per-call cost from O(V+E) (snapshotTF + snapshotDF)
	// into O(sum of degrees of visited tails).

	// One liveness instant for the whole walk (#838): every edge snapshot in
	// this traversal decides expiration against the same clock reading.
	now := time.Now()

	// BM25 corpus statistics are read once here (O(#tails), and only for the
	// BM25 weighting) under the same RLock the per-edge accessors rely on.
	// N = number of tails (documents); avgLen = mean out-degree. Each tail's
	// own out-degree is the per-document length, computed inside processTail.
	var bm25N int
	var bm25AvgLen float64
	if weighting == WeightingBM25 {
		tails, totalEdges := c.edges.corpusStats()
		bm25N = tails
		if tails > 0 {
			bm25AvgLen = float64(totalEdges) / float64(tails)
		}
	}

	// processTail computes one tail's top-k neighbor edges and publishes
	// them to g.Edges (and expirations, if requested) under mu. It is the
	// shared body used by both the sequential and worker-pool paths.
	processTail := func(t S) {
		// Referential closure (#750): a tail whose vertex is no longer live
		// contributes no edges, even if its buckets survive until the next
		// dangling-edge sweep.
		if !c.vertices.Has(t) {
			return
		}
		heads, ok := c.edges.headsOf(t)
		if !ok || len(heads) == 0 {
			return
		}
		// docLen is this tail's out-degree — the BM25 document length. Read
		// once here so BM25 length-normalises every edge of the tail against
		// the corpus mean (bm25AvgLen).
		docLen := len(heads)
		edges := make(pq.SortableMap[S, float32], len(heads))
		var expRow map[S]time.Time
		if collectExpirations {
			expRow = make(map[S]time.Time, len(heads))
		}
		for headID, w := range heads {
			head, ok := c.edges.resolveID(headID)
			if !ok {
				continue
			}
			// Hide an edge to a head whose vertex is not live (deleted or
			// expired-but-not-flushed) before it can be scored or selected by
			// the top-k prune below (#750).
			if !c.vertices.Has(head) {
				continue
			}
			// Frontier predicate: reject non-matching heads here, BEFORE
			// scoring and the Top(k)/Bottom(k) prune below, so top-k selects
			// the k best *accepted* neighbours. The seed is set on g.Vertices
			// before the walk and never reaches this loop, so it is exempt.
			if keep != nil && !keep(head) {
				continue
			}
			sum, latest, nonZero := w.snapshotAt(now)
			if !nonZero {
				continue
			}
			edges[head] = c.scoreEdge(weighting, sum, headID, docLen, bm25N, bm25AvgLen)
			if expRow != nil {
				expRow[head] = latest
			}
		}

		// Prune to the k edges at the Objective-selected extreme — the k
		// smallest weights when selectSmallest (MINIMIZE), the k largest
		// otherwise (#560) — then trim expirations to the survivors.
		if selectSmallest {
			edges = edges.Bottom(k)
		} else {
			edges = edges.Top(k)
		}
		if expRow != nil {
			filtered := make(map[S]time.Time, len(edges))
			for head := range edges {
				if exp, has := expRow[head]; has {
					filtered[head] = exp
				}
			}
			expRow = filtered
		}

		mu.Lock()
		g.Edges[t] = edges
		if expRow != nil {
			expirations[t] = expRow
		}
		mu.Unlock()
	}

	for range step {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if len(frontier) == 0 {
			break
		}

		// Small frontiers run sequentially: goroutine startup and the
		// shared mu.Lock round-trip dominate per-tail sort work below the
		// threshold. Larger frontiers fan out across a bounded worker pool
		// (capped at GOMAXPROCS) so we keep parallelism without unbounded
		// goroutine spawning.
		if len(frontier) < neighborParallelThreshold {
			for _, tail := range frontier {
				processTail(tail)
			}
		} else {
			workers := runtime.GOMAXPROCS(0)
			if workers > len(frontier) {
				workers = len(frontier)
			}
			tailCh := make(chan S, len(frontier))
			for _, tail := range frontier {
				tailCh <- tail
			}
			close(tailCh)
			var wg sync.WaitGroup
			wg.Add(workers)
			for i := 0; i < workers; i++ {
				go func() {
					defer wg.Done()
					for t := range tailCh {
						processTail(t)
					}
				}()
			}
			wg.Wait()
		}

		// The next frontier is exactly the surviving heads of the tails just
		// processed that have never been queued before — no rescan of the
		// whole discovered graph per step (#838).
		var next []S
		for _, tail := range frontier {
			for head := range g.Edges[tail] {
				if _, ok := seen[head]; !ok {
					seen[head] = struct{}{}
					next = append(next, head)
				}
			}
		}
		frontier = next
	}

	// Add vertices to the graph. Every endpoint reached here was gated on
	// vertices.Has in processTail, so under the held RLock Get returns ok; the
	// ok check is a defensive guard that keeps a dead endpoint out of the
	// result rather than inserting a zero value (#750).
	for tail, heads := range g.Edges {
		if v, ok := c.vertices.Get(tail); ok {
			g.Vertices[tail] = v
		}
		for head := range heads {
			if v, ok := c.vertices.Get(head); ok {
				g.Vertices[head] = v
			}
		}
	}

	return g, expirations, nil
}

// PPR (Personalized PageRank) defaults and guards (#801).
const (
	// DefaultPPRAlpha is the restart/teleport-to-seed probability used when a
	// caller passes a non-positive (or out-of-range) alpha. 0.15 is the
	// canonical PageRank damping complement — a broad, well-behaved locality.
	DefaultPPRAlpha = 0.15
	// DefaultPPREpsilon is the forward-push residual threshold used when a
	// caller passes a non-positive epsilon. 1e-4 keeps the push budget small
	// (O(1/(alpha*eps))) while staying accurate enough for ranking.
	DefaultPPREpsilon = 1e-4
	// pprCtxCheckInterval is how often (in pushes) the forward-push loop polls
	// ctx for cancellation, amortising the check off the hot path the same way
	// neighborContext checks once per BFS step.
	pprCtxCheckInterval = 1024
)

// pprMaxPushes is a hard guard on the forward-push budget against pathological
// inputs (a tiny eps or alpha). The Andersen–Chung–Lang bound is ~1/(alpha*eps)
// pushes; we allow a generous multiple plus a floor so a small graph with a
// modest eps is never truncated before it converges, and clamp to a hard cap so
// an adversarial request cannot spin indefinitely.
func pprMaxPushes(alpha, epsilon float64) int {
	const (
		floor   = 1 << 16 // 65k — never truncate a small, well-behaved walk
		hardCap = 1 << 27 // ~134M — absolute ceiling regardless of eps/alpha
	)
	if alpha <= 0 || epsilon <= 0 {
		return floor
	}
	bound := 8.0 / (alpha * epsilon)
	if bound >= hardCap {
		return hardCap
	}
	n := int(bound) + floor
	if n > hardCap {
		return hardCap
	}
	return n
}

// PersonalizedPageRank is the context-free convenience wrapper over
// PersonalizedPageRankContext using context.Background(). See that method for
// the full contract.
func (c *GraphCache[S, T]) PersonalizedPageRank(seed S, topN int, alpha, epsilon float64, weighting EdgeWeighting, keep func(S) bool) *graph.Graph[S, T] {
	g, _ := c.PersonalizedPageRankContext(context.Background(), seed, topN, alpha, epsilon, weighting, keep)
	return g
}

// PersonalizedPageRankContext ranks vertices by their Personalized PageRank
// (a.k.a. Random-Walk-with-Restart) relevance to seed and returns a relevance
// star: a graph whose seed→v edge weight is π[v], the stationary mass a
// seed-anchored random surfer accumulates on v. It replaces the greedy,
// per-hop Top(k) walk of Neighbor* with a single global, seed-relative score
// (#801), so a vertex reached via many paths outranks one reached via a single
// weak path, and a globally popular hub is discounted relative to this seed.
//
// It is computed by local forward-push (Andersen–Chung–Lang): residual mass is
// pushed only through vertices whose residual exceeds eps·deg, touching
// O(1/(alpha·eps)) vertices independent of |V|/|E|. alpha is the restart
// probability (locality knob: higher = tighter, seed-proximate); epsilon the
// residual threshold (smaller = more accurate, more work). Non-positive or
// out-of-range alpha/epsilon fall back to DefaultPPRAlpha / DefaultPPREpsilon.
//
// weighting transforms each transition affinity BEFORE row-normalization
// (raw / tfidf / bm25, via the shared scoreEdge kernel) so BM25-weighted PPR
// composes for free. keep is the optional frontier predicate (#601): a head is
// only walked into (and ranked) when keep(head) is true; the seed is the
// anchor exemption. ctx is polled every pprCtxCheckInterval pushes and returns
// ctx.Err() when cancelled. topN caps the ranked vertices returned (the request
// k); topN <= 0 returns every vertex with positive mass. An unknown seed yields
// an empty graph (the seed vertex alone is never emitted without neighbours).
func (c *GraphCache[S, T]) PersonalizedPageRankContext(ctx context.Context, seed S, topN int, alpha, epsilon float64, weighting EdgeWeighting, keep func(S) bool) (*graph.Graph[S, T], error) {
	if alpha <= 0 || alpha >= 1 {
		alpha = DefaultPPRAlpha
	}
	if epsilon <= 0 {
		epsilon = DefaultPPREpsilon
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	g := graph.NewGraph[S, T]()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	sv, ok := c.vertices.Get(seed)
	if !ok {
		return g, nil
	}
	g.Vertices[seed] = sv

	// BM25 corpus statistics are read once, only for the BM25 weighting, under
	// the same RLock the per-edge accessors rely on — identical to
	// neighborContext so PPR transitions and the BFS prune weight edges alike.
	var bm25N int
	var bm25AvgLen float64
	if weighting == WeightingBM25 {
		tails, totalEdges := c.edges.corpusStats()
		bm25N = tails
		if tails > 0 {
			bm25AvgLen = float64(totalEdges) / float64(tails)
		}
	}

	// Local forward-push. p is the approximate PPR vector, r the residual.
	// We drain each vertex's residual into p (the alpha share) and across its
	// weighting-transformed, row-normalized out-edges (the 1-alpha share)
	// until every residual falls below eps·deg.
	type scoredEdge struct {
		head S
		a    float64
	}
	p := make(map[S]float64)
	r := map[S]float64{seed: 1}
	queue := []S{seed}
	inQueue := map[S]bool{seed: true}
	maxPushes := pprMaxPushes(alpha, epsilon)

	// One liveness instant for the whole push (#838), mirroring
	// neighborContext; and an index cursor instead of queue = queue[1:], so
	// the loop does not re-slice while retaining the consumed backing head.
	now := time.Now()
	pushes := 0
	for qi := 0; qi < len(queue); qi++ {
		u := queue[qi]
		inQueue[u] = false
		ru := r[u]

		// Gather u's live, kept, weighting-transformed out-edges and their
		// affinity sum wu. A tail whose vertex is no longer live contributes
		// nothing (referential closure, #750), exactly as in neighborContext.
		var outs []scoredEdge
		var wu float64
		var docLen int
		if c.vertices.Has(u) {
			if heads, ok := c.edges.headsOf(u); ok {
				docLen = len(heads) // BM25 document length = raw out-degree
				outs = make([]scoredEdge, 0, len(heads))
				for headID, w := range heads {
					head, ok := c.edges.resolveID(headID)
					if !ok {
						continue
					}
					if !c.vertices.Has(head) {
						continue
					}
					if keep != nil && !keep(head) {
						continue
					}
					sum, _, nonZero := w.snapshotAt(now)
					if !nonZero {
						continue
					}
					a := float64(c.scoreEdge(weighting, sum, headID, docLen, bm25N, bm25AvgLen))
					if a <= 0 {
						continue
					}
					outs = append(outs, scoredEdge{head: head, a: a})
					wu += a
				}
			}
		}

		// ACL stopping rule: skip vertices whose residual is too small to
		// matter. A sink (no live kept out-edges) is still drained once — its
		// alpha share is absorbed into p and the remaining mass leaks — using a
		// degree floor of 1 so a non-trivial residual is not stranded forever.
		if ru < epsilon*float64(max(len(outs), 1)) {
			continue
		}

		r[u] = 0
		p[u] += alpha * ru
		if wu > 0 {
			spread := (1 - alpha) * ru
			for _, e := range outs {
				r[e.head] += spread * (e.a / wu)
				if !inQueue[e.head] {
					queue = append(queue, e.head)
					inQueue[e.head] = true
				}
			}
		}

		pushes++
		if pushes >= maxPushes {
			break
		}
		if pushes%pprCtxCheckInterval == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
	}

	// Rank by PPR mass, excluding the seed, and keep the top-N. Reusing
	// pq.SortableMap.Top mirrors the per-hop prune in neighborContext so the
	// tie-breaking semantics are identical across both traversal paths.
	scores := make(pq.SortableMap[S, float32], len(p))
	for key, mass := range p {
		if key == seed || mass <= 0 {
			continue
		}
		scores[key] = float32(mass)
	}
	if topN > 0 {
		scores = scores.Top(topN)
	}
	if len(scores) > 0 {
		row := make(map[S]float32, len(scores))
		for key, mass := range scores {
			if v, ok := c.vertices.Get(key); ok {
				g.Vertices[key] = v
				row[key] = mass
			}
		}
		if len(row) > 0 {
			g.Edges[seed] = row
		}
	}

	return g, nil
}
