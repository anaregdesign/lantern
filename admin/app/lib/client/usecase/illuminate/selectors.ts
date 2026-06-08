import type { Edge, Vertex } from "~/lib/client/infrastructure/api/illuminate";
import { ACCUMULATOR_SOFT_CAP, type IlluminateState } from "./state";

/**
 * `true` when the given protobuf-JSON timestamp is at or before the
 * supplied wall clock. Missing / unparseable timestamps are treated as
 * "never expires" (returns `false`) — defensive: we'd rather render a
 * value at full opacity than drop it over a malformed ISO string.
 *
 * Mirrors the semantics of
 * `IlluminateCanvas/ttl-decay.ts:computeTtlFraction` but specialised
 * to the boolean "should this be filtered?" question the selector
 * cares about. Kept here (not imported from the canvas package) so
 * the use-case layer stays free of component-side dependencies.
 */
function isExpired(expiration: string | undefined, nowMs: number): boolean {
  if (expiration === undefined || expiration === "") return false;
  const expiresAtMs = Date.parse(expiration);
  if (!Number.isFinite(expiresAtMs)) return false;
  return expiresAtMs <= nowMs;
}

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
 * Builds the view model the canvas renders from the accumulator. Pure;
 * no React, no DOM, no graphology — those live in the component layer
 * so this stays trivial to unit-test.
 *
 * `nowMs` is an injected "wall clock" used to drop already-expired
 * vertices and (cascading) their incident edges — per #459 the canvas
 * should never render a value that the server has already let go,
 * even if a stale response is still in the accumulator. The parameter
 * is optional and defaults to `Date.now()` because the vast majority
 * of test fixtures construct vertices without expiration; only the
 * decay-aware tests pass an explicit value.
 *
 * Mid-frame fading (per-tick alpha as a vertex approaches the
 * cliff) is NOT a selector concern — the canvas reducer reads the
 * expiration off each node and fades it on every refresh tick so we
 * avoid recomputing the whole view model every second. See
 * {@link IlluminateCanvas} `nowRef` / `tickRef` for the per-frame
 * machinery.
 */
export function selectGraphView(
  state: IlluminateState,
  nowMs: number = Date.now(),
): GraphView {
  const initialSeed = state.initialSeed;
  const expansionOrigins = state.expansions.map((e) => e.origin);
  const latestExpansionOrigin =
    expansionOrigins.length > 0
      ? (expansionOrigins[expansionOrigins.length - 1] ?? null)
      : null;

  if (state.accumulator.vertices.size === 0) {
    return {
      nodes: [],
      edges: [],
      latestExpansionOrigin,
      expansionOrigins,
      overSoftCap: false,
    };
  }

  const originSet = new Set(expansionOrigins);

  // Edge weights drive node importance. Aggregate over the merged edges,
  // skipping ones that are already past expiry — a dying edge shouldn't
  // inflate a node's importance score (#459).
  const weightByKey = new Map<string, number>();
  for (const acc of state.accumulator.edges.values()) {
    const e = acc.edge;
    if (!e.tail || !e.head) continue;
    if (isExpired(e.expiration, nowMs)) continue;
    const w = e.weight ?? 0;
    weightByKey.set(e.tail, (weightByKey.get(e.tail) ?? 0) + w);
    weightByKey.set(e.head, (weightByKey.get(e.head) ?? 0) + w);
  }
  const maxWeight = Math.max(0, ...weightByKey.values());

  const nodes: GraphNode[] = [];
  for (const [key, acc] of state.accumulator.vertices) {
    if (key === "") continue;
    // #459: filter expired vertices at selector time. The next fetch
    // would drop them anyway, but explicit removal here means a stale
    // accumulator never renders a tombstone.
    if (isExpired(acc.vertex.expiration, nowMs)) continue;
    const isInitialSeed = initialSeed !== null && key === initialSeed;
    const isExpansionOrigin = originSet.has(key);
    const raw = weightByKey.get(key) ?? 0;
    const importance =
      isInitialSeed || isExpansionOrigin
        ? 1
        : maxWeight > 0
          ? Math.max(0.1, raw / maxWeight)
          : 0.5;
    const firstSeenExpansion = acc.expansionIndexes[0] ?? 0;
    nodes.push({
      id: key,
      label: key,
      vertex: acc.vertex,
      isInitialSeed,
      isExpansionOrigin,
      importance,
      firstSeenExpansion,
      // Placeholder; filled in below after edges are filtered so the
      // BFS adjacency mirrors the live (non-expired) graph.
      hopDistance: Number.POSITIVE_INFINITY,
    });
  }

  const knownKeys = new Set(nodes.map((n) => n.id));
  const edges: GraphEdge[] = [];
  for (const [id, acc] of state.accumulator.edges) {
    const e = acc.edge;
    if (!e.tail || !e.head) continue;
    // Drop edges that reference vertices we don't have; the canvas would
    // otherwise throw when sigma tries to look them up. (Shouldn't happen
    // with current server behaviour, but defensive.) This also handles
    // the #459 cascade where an expired vertex was filtered above.
    if (!knownKeys.has(e.tail) || !knownKeys.has(e.head)) continue;
    // #459: an edge can have its own TTL that's shorter than either
    // endpoint's. Filter past-expiry edges explicitly.
    if (isExpired(e.expiration, nowMs)) continue;
    edges.push({
      id,
      source: e.tail,
      target: e.head,
      weight: e.weight ?? 0,
      edge: e,
    });
  }

  // #460: multi-source BFS over the undirected projection of the live
  // (post-filter) edge set, seeded by every expansion origin that is
  // still in the accumulator. Vertices unreachable from every origin
  // keep the `Number.POSITIVE_INFINITY` placeholder so the canvas
  // reducer can render them with the "unreachable" tone.
  const hopByKey = computeHopDistances(knownKeys, edges, expansionOrigins);
  for (const node of nodes) {
    node.hopDistance = hopByKey.get(node.id) ?? Number.POSITIVE_INFINITY;
  }

  return {
    nodes,
    edges,
    latestExpansionOrigin,
    expansionOrigins,
    overSoftCap: state.accumulator.vertices.size > ACCUMULATOR_SOFT_CAP,
  };
}

export function selectIsBusy(state: IlluminateState): boolean {
  return state.status === "loading" || state.pendingCount > 0;
}

export function selectExpansionCount(state: IlluminateState): number {
  return state.expansions.length;
}

export function selectCanClear(state: IlluminateState): boolean {
  return state.expansions.length > 0 || state.accumulator.vertices.size > 0;
}
