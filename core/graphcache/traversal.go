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
	// targets / seen are accessed only from the main goroutine (between
	// wg.Wait barriers), so plain maps suffice — no need for the locked
	// set.Set wrapper. mu protects concurrent writes to g.Edges and
	// (when requested) expirations.
	targets := map[S]struct{}{seed: {}}
	seen := make(map[S]struct{})
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
			sum, latest, nonZero := w.snapshot()
			if !nonZero {
				continue
			}
			switch weighting {
			case WeightingTFIDF:
				edges[head] = sum / float32(math.Log2(float64(1+c.edges.docFreq(headID))))
			case WeightingBM25:
				edges[head] = float32(search.BM25Score(
					float64(sum), bm25AvgLen,
					c.edges.docFreq(headID), bm25N, docLen,
					search.DefaultBM25K1, search.DefaultBM25B,
				))
			default: // WeightingRaw
				edges[head] = sum
			}
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

		// Collect this step's frontier (targets not yet processed). Marking
		// happens after wg.Wait so the goroutines need not touch `seen`.
		frontier := make([]S, 0, len(targets))
		for t := range targets {
			if _, ok := seen[t]; ok {
				continue
			}
			frontier = append(frontier, t)
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

		// Mark this step's frontier as processed.
		for _, t := range frontier {
			seen[t] = struct{}{}
		}

		// Find all next targets
		for _, heads := range g.Edges {
			for head := range heads {
				if _, ok := seen[head]; !ok {
					targets[head] = struct{}{}
				}
			}
		}
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
