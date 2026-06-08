import type { Edge, Vertex } from "~/lib/client/infrastructure/api/illuminate";
import { ACCUMULATOR_SOFT_CAP, type IlluminateState } from "./state";

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
 * Builds the view model the canvas renders from the accumulator. Pure;
 * no React, no DOM, no graphology — those live in the component layer
 * so this stays trivial to unit-test.
 */
export function selectGraphView(state: IlluminateState): GraphView {
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

  // Edge weights drive node importance. Aggregate over the merged edges.
  const weightByKey = new Map<string, number>();
  for (const acc of state.accumulator.edges.values()) {
    const e = acc.edge;
    if (!e.tail || !e.head) continue;
    const w = e.weight ?? 0;
    weightByKey.set(e.tail, (weightByKey.get(e.tail) ?? 0) + w);
    weightByKey.set(e.head, (weightByKey.get(e.head) ?? 0) + w);
  }
  const maxWeight = Math.max(0, ...weightByKey.values());

  const nodes: GraphNode[] = [];
  for (const [key, acc] of state.accumulator.vertices) {
    if (key === "") continue;
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
    });
  }

  const knownKeys = new Set(nodes.map((n) => n.id));
  const edges: GraphEdge[] = [];
  for (const [id, acc] of state.accumulator.edges) {
    const e = acc.edge;
    if (!e.tail || !e.head) continue;
    // Drop edges that reference vertices we don't have; the canvas would
    // otherwise throw when sigma tries to look them up. (Shouldn't happen
    // with current server behaviour, but defensive.)
    if (!knownKeys.has(e.tail) || !knownKeys.has(e.head)) continue;
    edges.push({
      id,
      source: e.tail,
      target: e.head,
      weight: e.weight ?? 0,
      edge: e,
    });
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
