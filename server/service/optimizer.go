package service

import (
	"context"
	"errors"
	"math"

	"connectrpc.com/connect"
	coregraph "github.com/anaregdesign/lantern/core/graph"
	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// optimizer post-processes the illuminated subgraph for a given seed.
// Implementations may rebuild the graph (e.g. spanning trees, shortest-path
// trees) and must honour ctx for cancellation.
type optimizer func(ctx context.Context, g *coregraph.Graph[string, *pb.Vertex], seed string) (*coregraph.Graph[string, *pb.Vertex], error)

// resolveOptimizer returns the post-traversal reduction for a
// (reduction, objective) pair (#846: reductions are a BfsParams knob, not
// sibling traversals). Returns nil when no reduction is needed — the caller
// treats nil as "return the raw discovered subgraph".
//
// Note: Reduction UNSPECIFIED → nil (no reduction). Objective
// OBJECTIVE_UNSPECIFIED resolves to MAXIMIZE per the proto-level default
// (#560): a bare illuminate keeps the strongest edges both when pruning the
// per-hop top-k and when reducing the discovered subgraph.
func resolveOptimizer(red pb.Reduction, obj pb.Objective) optimizer {
	switch red {
	case pb.Reduction_REDUCTION_MINIMUM_SPANNING_TREE:
		if obj == pb.Objective_OBJECTIVE_MINIMIZE {
			return func(ctx context.Context, g *coregraph.Graph[string, *pb.Vertex], seed string) (*coregraph.Graph[string, *pb.Vertex], error) {
				return g.MinimumSpanningTreeContext(ctx, seed)
			}
		}
		return func(ctx context.Context, g *coregraph.Graph[string, *pb.Vertex], seed string) (*coregraph.Graph[string, *pb.Vertex], error) {
			return g.MaximumSpanningTreeContext(ctx, seed)
		}
	case pb.Reduction_REDUCTION_SHORTEST_PATH_TREE:
		if obj == pb.Objective_OBJECTIVE_MINIMIZE {
			return func(ctx context.Context, g *coregraph.Graph[string, *pb.Vertex], seed string) (*coregraph.Graph[string, *pb.Vertex], error) {
				return g.ShortestPathTreeContext(ctx, seed, identityCost)
			}
		}
		return func(ctx context.Context, g *coregraph.Graph[string, *pb.Vertex], seed string) (*coregraph.Graph[string, *pb.Vertex], error) {
			return g.ShortestPathTreeContext(ctx, seed, inverseCost)
		}
	}
	return nil
}

func identityCost(weight float32) float32 { return weight }

func inverseCost(weight float32) float32 {
	if weight == 0 {
		return math.MaxFloat32
	}
	return 1 / weight
}

// optimizerToConnect maps reduction-specific data preconditions to stable
// wire errors while retaining ctxToConnect's cancellation semantics. In
// particular, Dijkstra must not receive negative or non-finite costs: it can
// otherwise continue relaxing a negative cycle indefinitely.
func optimizerToConnect(err error) error {
	if errors.Is(err, coregraph.ErrInvalidShortestPathCost) {
		return connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return ctxToConnect(err)
}

// resolvePPRParams resolves the Personalized PageRank restart probability
// (alpha) and forward-push residual threshold (epsilon) from the wire request,
// applying the core defaults when the caller leaves a knob unset or out of
// range. The server owns the default policy here — referencing the single
// source of truth in core — so the metric label space and the documented
// behaviour agree; core's own clamping is a defensive backstop. restart_prob
// must lie in (0,1) to be honoured (0/unset or >=1 → DefaultPPRAlpha=0.15);
// epsilon must be positive (0/unset → DefaultPPREpsilon=1e-4).
func resolvePPRParams(restartProb, epsilon float32) (alpha, eps float64) {
	alpha = float64(restartProb)
	if alpha <= 0 || alpha >= 1 {
		alpha = graphcache.DefaultPPRAlpha
	}
	eps = float64(epsilon)
	if eps <= 0 {
		eps = graphcache.DefaultPPREpsilon
	}
	return alpha, eps
}

// reductionLabel / objectiveLabel / weightingLabel produce the canonical
// metric label string for each axis. The label VALUES predate the #846
// oneof redesign (they were derived from the retired Algorithm enum) and
// are kept verbatim so dashboards survive: "none"/"mst"/"spt" for the BFS
// family by reduction, "ppr" emitted directly by the PPR arm. UNSPECIFIED
// resolves to the same label the server resolves it to at execution time:
//   - Reduction UNSPECIFIED → "none"  (no reduction)
//   - Objective UNSPECIFIED → "maximize"
//   - Weighting UNSPECIFIED → "raw"
//
// Unknown enum values (a future axis added in proto without a metrics
// update) fall through to a synthetic "unknown" bucket so dashboards
// still surface them instead of crashing the pre-warm step.
func reductionLabel(r pb.Reduction) string {
	switch r {
	case pb.Reduction_REDUCTION_UNSPECIFIED:
		return "none"
	case pb.Reduction_REDUCTION_MINIMUM_SPANNING_TREE:
		return "mst"
	case pb.Reduction_REDUCTION_SHORTEST_PATH_TREE:
		return "spt"
	}
	return "unknown"
}

func objectiveLabel(o pb.Objective) string {
	switch o {
	case pb.Objective_OBJECTIVE_MINIMIZE:
		return "minimize"
	case pb.Objective_OBJECTIVE_UNSPECIFIED, pb.Objective_OBJECTIVE_MAXIMIZE:
		return "maximize"
	}
	return "unknown"
}

func weightingLabel(w pb.Weighting) string {
	switch w {
	case pb.Weighting_WEIGHTING_UNSPECIFIED, pb.Weighting_WEIGHTING_RAW:
		return "raw"
	case pb.Weighting_WEIGHTING_TFIDF:
		return "tfidf"
	case pb.Weighting_WEIGHTING_BM25:
		return "bm25"
	}
	return "unknown"
}

// weightingToCore maps the wire weighting axis to the core edge-weight
// transform. UNSPECIFIED/RAW collapse to the verbatim weight; an unknown
// future enum value also falls back to RAW so a proto-only addition never
// silently mis-weights — it degrades to the raw graph until the core path
// learns the new transform.
func weightingToCore(w pb.Weighting) graphcache.EdgeWeighting {
	switch w {
	case pb.Weighting_WEIGHTING_TFIDF:
		return graphcache.WeightingTFIDF
	case pb.Weighting_WEIGHTING_BM25:
		return graphcache.WeightingBM25
	}
	return graphcache.WeightingRaw
}
