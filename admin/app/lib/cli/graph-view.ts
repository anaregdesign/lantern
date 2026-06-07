/**
 * Per-command graph projection (#439).
 *
 * Given a parsed CLI `Command` and the JSON-serialisable result the
 * dispatcher returned, project the response into the same
 * `GraphView` shape `IlluminateCanvas` already consumes on the
 * `/illuminate` route. Verbs whose response carries no graph data
 * (`put`, `add`, `delete`, `exit`) return `null` so the caller can
 * keep the previous canvas frame visible — the user does not lose
 * their context after a mutation.
 *
 * Pure: no React, no DOM, no Sigma. The CliPage component handles
 * mount lifecycle.
 */

import type { Command } from "./types";
import type {
  GraphEdge,
  GraphNode,
  GraphView,
} from "~/lib/client/usecase/illuminate/selectors";
import type {
  Edge,
  IlluminateResponse,
  ScanEdgesResponse,
  ScanVerticesResponse,
  Vertex,
} from "~/lib/client/infrastructure/api/types";

/**
 * Project the dispatcher result for `command` into a `GraphView`.
 * Returns `null` when the verb carries no graph payload — the
 * caller should leave the previous canvas frame alone.
 */
export function commandResultToGraphView(
  command: Command,
  result: unknown,
): GraphView | null {
  switch (command.verb) {
    case "illuminate":
      return illuminateView(command.seed, result as IlluminateResponse);
    case "get":
      if (command.objective === "vertex") {
        return getVertexView(command.key, result as Vertex | null);
      }
      return getEdgeView(command.tail, command.head, result as Edge | null);
    case "scan":
      if (command.objective === "vertices") {
        return scanVerticesView(command.prefix, result as ScanVerticesResponse);
      }
      return scanEdgesView(command.tailPrefix, result as ScanEdgesResponse);
    default:
      // put, add, delete, exit — no graph payload.
      return null;
  }
}

function illuminateView(seed: string, response: IlluminateResponse): GraphView {
  const graph = response.graph ?? {};
  const seedKey = seed;

  const rawVertices: Vertex[] = graph.vertices ?? [];
  const rawEdges: Edge[] = graph.edges ?? [];

  // Importance == sum of incident edge weights, normalised against the
  // largest such sum. The seed is pinned to 1 regardless so it always
  // dominates the canvas like the /illuminate route does.
  const weightByKey = new Map<string, number>();
  for (const edge of rawEdges) {
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

  const nodes: GraphNode[] = rawVertices
    .filter(
      (v): v is Vertex & { key: string } =>
        typeof v.key === "string" && v.key !== "",
    )
    .map((v) => {
      const isSeed = v.key === seedKey;
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
  for (const e of rawEdges) {
    if (!e.tail || !e.head) continue;
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

function getVertexView(key: string, vertex: Vertex | null): GraphView {
  if (!vertex) {
    // The server returned NotFound. Render an empty canvas; the
    // scrollback already shows the error.
    return { nodes: [], edges: [] };
  }
  return {
    nodes: [
      {
        id: key,
        label: key,
        vertex: { ...vertex, key },
        isSeed: true,
        importance: 1,
      },
    ],
    edges: [],
  };
}

function getEdgeView(tail: string, head: string, edge: Edge | null): GraphView {
  if (!edge) {
    return { nodes: [], edges: [] };
  }
  const nodes: GraphNode[] = [
    {
      id: tail,
      label: tail,
      // We do NOT have the underlying vertex value (the server only
      // returned the edge); synthesise a key-only Vertex so the
      // canvas tooltip / a11y table render a recognisable label
      // rather than crashing on missing fields.
      vertex: { key: tail },
      isSeed: true,
      importance: 1,
    },
    {
      id: head,
      label: head,
      vertex: { key: head },
      isSeed: false,
      importance: 0.5,
    },
  ];
  const edges: GraphEdge[] = [
    {
      id: `${tail}→${head}`,
      source: tail,
      target: head,
      weight: edge.weight ?? 0,
      edge,
    },
  ];
  return { nodes, edges };
}

function scanVerticesView(
  prefix: string,
  response: ScanVerticesResponse,
): GraphView {
  const rawVertices = response.vertices ?? [];
  const seedKey = prefix; // Empty prefix → no seed, every node neutral.
  const nodes: GraphNode[] = rawVertices
    .filter(
      (v): v is Vertex & { key: string } =>
        typeof v.key === "string" && v.key !== "",
    )
    .map((v) => ({
      id: v.key,
      label: v.key,
      vertex: v,
      isSeed: seedKey !== "" && v.key === seedKey,
      importance: 0.5,
    }));
  return { nodes, edges: [] };
}

function scanEdgesView(
  tailPrefix: string,
  response: ScanEdgesResponse,
): GraphView {
  const rawEdges = response.edges ?? [];
  const nodeMap = new Map<string, GraphNode>();
  const edges: GraphEdge[] = [];
  for (const e of rawEdges) {
    if (!e.tail || !e.head) continue;
    if (!nodeMap.has(e.tail)) {
      nodeMap.set(e.tail, {
        id: e.tail,
        label: e.tail,
        vertex: { key: e.tail },
        // Mark every endpoint whose key matches the scan prefix as
        // a seed. Empty prefix → no seeds (every node neutral).
        isSeed: tailPrefix !== "" && e.tail === tailPrefix,
        importance: 0.5,
      });
    }
    if (!nodeMap.has(e.head)) {
      nodeMap.set(e.head, {
        id: e.head,
        label: e.head,
        vertex: { key: e.head },
        isSeed: false,
        importance: 0.5,
      });
    }
    edges.push({
      id: `${e.tail}→${e.head}`,
      source: e.tail,
      target: e.head,
      weight: e.weight ?? 0,
      edge: e,
    });
  }
  return { nodes: Array.from(nodeMap.values()), edges };
}
