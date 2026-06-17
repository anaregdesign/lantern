import type { Edge, Vertex } from "~/lib/client/infrastructure/api/illuminate";

/**
 * Graph node shape consumed by `IlluminateCanvas`. We keep the full
 * Vertex around so the tooltip / a11y table / Drawer can render typed
 * values without a second pass over the response.
 *
 * `isInitialSeed` replaces the legacy `isSeed`: in the additive model
 * (#466) the structurally privileged node is the URL-level initial seed
 * (`expansions[0].origin`), not whichever vertex the user clicked last.
 */
export interface GraphNode {
  id: string;
  label: string;
  vertex: Vertex;
  /** True iff this node is `initialSeed` — the first expansion's origin. */
  isInitialSeed: boolean;
  /** True iff this node was ever the origin of an expansion (D5 chip strip). */
  isExpansionOrigin: boolean;
  /** Edge-weight-derived score in [0, 1], 1.0 for the seed and expansion origins. */
  importance: number;
  /** Index into `state.expansions[]` where this vertex first appeared. */
  firstSeenExpansion: number;
  /**
   * Minimum hop distance from this vertex to ANY expansion origin
   * (#460). Computed via multi-source BFS over the undirected
   * projection of the accumulator's edge set: every origin starts at 0
   * and a single BFS relaxation gives every reachable vertex its
   * shortest hop. Vertices that are disconnected from every origin
   * receive `Number.POSITIVE_INFINITY` so the canvas reducer can pick
   * the "unreachable" tone.
   *
   * The undirected projection is deliberate: from the user's point of
   * view, an `Illuminate(origin=A, step=2)` call returns a 2-hop
   * neighbourhood regardless of edge direction, so a hop-distance
   * encoding that distinguished `A → B` from `B → A` would render
   * visually inconsistent with how the data arrived.
   *
   * Invariant (#460 acceptance criterion 2): adding a new expansion
   * can only ever shrink an existing vertex's hop distance, never
   * grow it — the multi-source frontier strictly widens as origins
   * are added.
   */
  hopDistance: number;
}

export interface GraphEdge {
  id: string;
  source: string;
  target: string;
  weight: number;
  edge: Edge;
}

export interface GraphView {
  nodes: GraphNode[];
  edges: GraphEdge[];
  /**
   * The origin of the most recent expansion, or null when the canvas is
   * empty. `IlluminateCanvas` uses this to seed new-node positions near
   * the parent click (#466 D7).
   */
  latestExpansionOrigin: string | null;
  /** All expansion origins, in order; used by #456 chip strip and #460 hop coloring. */
  expansionOrigins: string[];
  /** True when the accumulator is past the soft cap; UI surfaces a MessageBar. */
  overSoftCap: boolean;
  /**
   * Vertex keys belonging to the most recent expansion's result (#483).
   * The canvas hides every node whose id is absent from this set so the
   * view collapses to just the latest Illuminate result. Empty when
   * there are no expansions yet — the canvas treats an empty set as
   * "no filter" and shows everything (cold-start fallback).
   *
   * Keys match {@link GraphNode.id}; derived from the latest
   * `Expansion.vertexKeys`.
   */
  latestResultVertexKeys: Set<string>;
  /**
   * Edge ids belonging to the most recent expansion's result (#483).
   * The canvas hides every edge whose id is absent from this set. Empty
   * when there are no expansions yet (treated as "no filter").
   *
   * Ids match {@link GraphEdge.id} (`${tail}→${head}`); derived from the
   * latest `Expansion.edgeIds`.
   */
  latestResultEdgeIds: Set<string>;
}

/**
 * Multi-source BFS over the undirected projection of `edges`, seeded
 * by every key in `origins` that exists in `knownKeys`. Returns a map
 * of vertex id → minimum hop distance to ANY origin (#460). Vertices
 * unreachable from every origin are simply absent from the result —
 * callers should treat absence as `Number.POSITIVE_INFINITY`.
 *
 * Complexity is O(V + E) over the post-filter accumulator. With the
 * #466 D13 hard cap of 2000 vertices the BFS is comfortably under a
 * millisecond, well within the per-selector budget.
 *
 * The undirected projection is the intentional choice: each
 * `Illuminate(origin, step=2)` call returns a 2-hop neighbourhood
 * regardless of edge direction, so a directional hop encoding would
 * disagree with how the data arrived.
 */
export function computeHopDistances(
  knownKeys: Set<string>,
  edges: GraphEdge[],
  origins: string[],
): Map<string, number> {
  // Adjacency list over the undirected projection. Only includes
  // endpoints we actually have nodes for so the BFS can't dereference
  // an unknown id.
  const adjacency = new Map<string, string[]>();
  for (const e of edges) {
    if (!knownKeys.has(e.source) || !knownKeys.has(e.target)) continue;
    appendAdjacency(adjacency, e.source, e.target);
    appendAdjacency(adjacency, e.target, e.source);
  }

  const distance = new Map<string, number>();
  const frontier: string[] = [];
  for (const origin of origins) {
    if (!knownKeys.has(origin)) continue;
    if (distance.has(origin)) continue;
    distance.set(origin, 0);
    frontier.push(origin);
  }
  // Multi-source BFS: every origin starts at hop 0 in the same
  // queue, so the first time we visit a vertex we have its global
  // shortest hop. Using an index instead of `Array.shift()` keeps the
  // walk O(V + E) — `shift` is O(n).
  let head = 0;
  while (head < frontier.length) {
    const node = frontier[head++]!;
    const nextHop = (distance.get(node) ?? 0) + 1;
    const neighbours = adjacency.get(node);
    if (!neighbours) continue;
    for (const neighbour of neighbours) {
      if (distance.has(neighbour)) continue;
      distance.set(neighbour, nextHop);
      frontier.push(neighbour);
    }
  }
  return distance;
}

function appendAdjacency(
  map: Map<string, string[]>,
  key: string,
  neighbour: string,
) {
  const list = map.get(key);
  if (list) {
    list.push(neighbour);
  } else {
    map.set(key, [neighbour]);
  }
}

/**
 * The set of node/edge ids graphology keeps for the current frame.
 * {@link selectRenderTargets} resolves it; the reconcile effect and the
 * hop legend both consume it so they agree on exactly what is rendered.
 */
export interface RenderTargets {
  /** Node ids to keep this frame (everything else is dropped). */
  nodeIds: Set<string>;
  /** Edge ids to keep this frame (everything else is dropped). */
  edgeIds: Set<string>;
}

/**
 * Resolves which nodes and edges the canvas renders this frame (#491).
 *
 * The canvas renders ONLY the latest expansion result: once an expansion
 * produces a result, graphology is reconciled down to exactly those
 * nodes/edges and the rest are DROPPED (not hidden). An empty result set
 * — cold mount, reseed, or Clear — falls back to the full accumulator
 * (itself empty in that case), i.e. "no filter".
 *
 * Single membership authority: the reconcile diff (add/drop) and the hop
 * legend ({@link selectHopBucketCounts}) both derive their rendered set
 * from here, so the legend can never again tally vertices that have been
 * dropped from the canvas (the #491 desync). The `hasResult` branch
 * returns the SAME Set references the caller passed in, so reference
 * identity (e.g. the reconcile's `previousNodeIdsRef`) is preserved.
 */
export function selectRenderTargets({
  nodes,
  edges,
  latestResultVertexKeys,
  latestResultEdgeIds,
}: Pick<
  GraphView,
  "nodes" | "edges" | "latestResultVertexKeys" | "latestResultEdgeIds"
>): RenderTargets {
  const hasResult = latestResultVertexKeys.size > 0;
  return {
    nodeIds: hasResult
      ? latestResultVertexKeys
      : new Set(nodes.map((n) => n.id)),
    edgeIds: hasResult ? latestResultEdgeIds : new Set(edges.map((e) => e.id)),
  };
}

/**
 * The hop distance at which the per-step ramp collapses into the single
 * desaturated "far" tier (#460): every vertex `>= HOP_FAR_THRESHOLD`
 * hops from the nearest expansion origin shares one swatch, because
 * distinguishing 3-hop from 5-hop adds visual noise without
 * informational value when the canvas already encodes TTL alpha and
 * hover focus on the same pixel.
 *
 * Canonical home is the use-case layer so the legend read-model
 * ({@link selectHopBucketCounts}) and the canvas palette
 * (`IlluminateCanvas/hop-palette.ts`, which re-exports this constant)
 * agree on the boundary without duplicating the literal across the
 * component/use-case line.
 */
export const HOP_FAR_THRESHOLD = 3;

/**
 * Hop-distance legend tiers, in palette-ramp order (#460):
 * `origin` (0) → `1hop` → `2hop` → `far` (>= {@link HOP_FAR_THRESHOLD})
 * → `unreachable` (∞ / disconnected from every origin).
 */
export type HopBucketKey = "origin" | "1hop" | "2hop" | "far" | "unreachable";

/** One legend tier and how many rendered nodes fall into it. */
export interface HopBucketCount {
  key: HopBucketKey;
  count: number;
}

/**
 * Counts the rendered nodes per hop-distance tier for the legend (#460).
 *
 * `renderNodeIds` is the resolved render set from
 * {@link selectRenderTargets} — the SAME membership the reconcile uses —
 * so the legend can only ever tally nodes that are actually on the canvas
 * (the #491 desync came from re-deriving membership inline here). Nodes
 * absent from the set are skipped; pass every node id (the cold-start
 * fallback `selectRenderTargets` already returns) to count them all.
 *
 * Returns all five tiers in palette-ramp order with their counts (zeros
 * included); presentation decides whether to hide empty tiers and which
 * swatch/label to attach.
 */
export function selectHopBucketCounts(
  nodes: GraphNode[],
  renderNodeIds: Set<string>,
): HopBucketCount[] {
  let origin = 0;
  let oneHop = 0;
  let twoHop = 0;
  let far = 0;
  let unreachable = 0;
  for (const node of nodes) {
    if (!renderNodeIds.has(node.id)) continue;
    const h = node.hopDistance;
    if (!Number.isFinite(h) || h < 0) {
      unreachable += 1;
    } else if (h === 0) {
      origin += 1;
    } else if (h === 1) {
      oneHop += 1;
    } else if (h === 2) {
      twoHop += 1;
    } else if (h >= HOP_FAR_THRESHOLD) {
      far += 1;
    }
  }
  return [
    { key: "origin", count: origin },
    { key: "1hop", count: oneHop },
    { key: "2hop", count: twoHop },
    { key: "far", count: far },
    { key: "unreachable", count: unreachable },
  ];
}
