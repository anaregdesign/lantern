import type { Edge, Vertex } from "~/lib/client/infrastructure/api/illuminate";
import type { IlluminateState } from "./state";

/**
 * Graph node shape consumed by `IlluminateCanvas`. We keep the full
 * Vertex around so the tooltip / a11y table can render typed values
 * without a second pass over the response.
 */
export interface GraphNode {
  id: string;
  label: string;
  vertex: Vertex;
  /** True iff this node is the active seed. */
  isSeed: boolean;
  /** Edge-weight-derived score in [0, 1], 1.0 for the seed. */
  importance: number;
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
}

/**
 * Builds the view model the canvas renders from the most recent fetched
 * frame. Pure; no React, no DOM, no graphology — those live in the
 * component layer so this stays trivial to unit-test.
 */
export function selectGraphView(state: IlluminateState): GraphView {
  const frame = state.frame;
  if (!frame) {
    return { nodes: [], edges: [] };
  }
  const seed = frame.seed;
  const weightByKey = new Map<string, number>();
  for (const edge of frame.edges) {
    if (!edge.tail || !edge.head) continue;
    weightByKey.set(
      edge.tail,
      (weightByKey.get(edge.tail) ?? 0) + (edge.weight ?? 0),
    );
    weightByKey.set(
      edge.head,
      (weightByKey.get(edge.head) ?? 0) + (edge.weight ?? 0),
    );
  }
  const maxWeight = Math.max(0, ...weightByKey.values());
  const nodes: GraphNode[] = frame.vertices
    .filter(
      (v): v is Vertex & { key: string } =>
        typeof v.key === "string" && v.key !== "",
    )
    .map((v) => {
      const isSeed = v.key === seed;
      const raw = weightByKey.get(v.key) ?? 0;
      const importance = isSeed
        ? 1
        : maxWeight > 0
          ? Math.max(0.1, raw / maxWeight)
          : 0.5;
      return {
        id: v.key,
        label: v.key,
        vertex: v,
        isSeed,
        importance,
      };
    });
  const knownKeys = new Set(nodes.map((n) => n.id));
  const edges: GraphEdge[] = [];
  for (const e of frame.edges) {
    if (!e.tail || !e.head) continue;
    // Drop edges that reference vertices the response didn't return; the
    // canvas would otherwise throw when sigma tries to look them up.
    if (!knownKeys.has(e.tail) || !knownKeys.has(e.head)) continue;
    edges.push({
      id: `${e.tail}→${e.head}`,
      source: e.tail,
      target: e.head,
      weight: e.weight ?? 0,
      edge: e,
    });
  }
  return { nodes, edges };
}

export function selectCanPop(state: IlluminateState): boolean {
  return state.history.length > 1;
}

export function selectIsBusy(state: IlluminateState): boolean {
  return state.status === "loading";
}
