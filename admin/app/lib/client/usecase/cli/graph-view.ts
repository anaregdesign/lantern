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
 *
 * The CLI surface is stateless from the canvas's perspective — every
 * command produces a single "expansion" that overwrites the previous
 * frame. So every node gets `firstSeenExpansion: 0` and `overSoftCap`
 * is always false (no accumulation across commands).
 */

import type { Command } from "~/lib/cli/types";
import type {
  GraphEdge,
  GraphNode,
  GraphView,
} from "~/lib/client/usecase/illuminate/selectors";
import { computeHopDistances } from "~/lib/client/usecase/illuminate/selectors";
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

/**
 * Wrap a freshly built {nodes, edges} pair in the additive-model
 * envelope the canvas expects. `seed` doubles as both the initial
 * seed and the latest expansion origin in the CLI's stateless view.
 */
function wrapView(
  nodes: GraphNode[],
  edges: GraphEdge[],
  seed: string | null,
): GraphView {
  return {
    nodes,
    edges,
    latestExpansionOrigin: seed,
    expansionOrigins: seed !== null && seed !== "" ? [seed] : [],
    overSoftCap: false,
  };
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
        isInitialSeed: isSeed,
        isExpansionOrigin: isSeed,
        importance,
        firstSeenExpansion: 0,
        // Placeholder; filled in by the BFS below now that we know the
        // full node set + edge filter. The CLI uses single-source BFS
        // from THE seed (#460 issue body explicitly carves out the CLI
        // path as not affected by the multi-source semantics).
        hopDistance: Number.POSITIVE_INFINITY,
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
  // Single-source BFS from the seed so the cli's per-call frame
  // gets the same hop encoding as the /illuminate route (just with
  // one origin instead of N).
  const hopByKey = computeHopDistances(knownKeys, edges, [seedKey]);
  for (const node of nodes) {
    node.hopDistance = hopByKey.get(node.id) ?? Number.POSITIVE_INFINITY;
  }
  return wrapView(nodes, edges, seedKey === "" ? null : seedKey);
}

function getVertexView(key: string, vertex: Vertex | null): GraphView {
  if (!vertex) {
    // The server returned NotFound. Render an empty canvas; the
    // scrollback already shows the error.
    return wrapView([], [], null);
  }
  return wrapView(
    [
      {
        id: key,
        label: key,
        vertex: { ...vertex, key },
        isInitialSeed: true,
        isExpansionOrigin: true,
        importance: 1,
        firstSeenExpansion: 0,
        // Sole node IS the seed/origin, so hop 0 by definition.
        hopDistance: 0,
      },
    ],
    [],
    key,
  );
}

function getEdgeView(tail: string, head: string, edge: Edge | null): GraphView {
  if (!edge) {
    return wrapView([], [], null);
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
      isInitialSeed: true,
      isExpansionOrigin: true,
      importance: 1,
      firstSeenExpansion: 0,
      // `get edge` is rooted at the tail; head is exactly one hop away.
      hopDistance: 0,
    },
    {
      id: head,
      label: head,
      vertex: { key: head },
      isInitialSeed: false,
      isExpansionOrigin: false,
      importance: 0.5,
      firstSeenExpansion: 0,
      hopDistance: 1,
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
  return wrapView(nodes, edges, tail);
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
    .map((v) => {
      const isSeed = seedKey !== "" && v.key === seedKey;
      return {
        id: v.key,
        label: v.key,
        vertex: v,
        isInitialSeed: isSeed,
        isExpansionOrigin: isSeed,
        importance: 0.5,
        firstSeenExpansion: 0,
        // Scan results have NO edges so BFS isn't meaningful. The
        // matched seed gets hop 0 (origin); everyone else gets hop 1
        // so the legend reads as "origin + 1 neighbour bucket"
        // instead of degenerating into an unreachable-red sea.
        hopDistance: isSeed ? 0 : 1,
      };
    });
  // For scan we treat the prefix as the latest origin only if a node
  // matches it exactly (otherwise the canvas would render a halo
  // around an absent vertex). Returning null is the right signal.
  const matchedSeed = nodes.some((n) => seedKey !== "" && n.id === seedKey)
    ? seedKey
    : null;
  return wrapView(nodes, [], matchedSeed);
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
      const isSeed = tailPrefix !== "" && e.tail === tailPrefix;
      nodeMap.set(e.tail, {
        id: e.tail,
        label: e.tail,
        vertex: { key: e.tail },
        // Mark every endpoint whose key matches the scan prefix as
        // a seed. Empty prefix → no seeds (every node neutral).
        isInitialSeed: isSeed,
        isExpansionOrigin: isSeed,
        importance: 0.5,
        firstSeenExpansion: 0,
        // The tail endpoint is the "starting" side of the edge from
        // the prefix scan's perspective. Same 0/1 ramp as
        // scanVerticesView: matched prefix → origin, else first
        // neighbour ring.
        hopDistance: isSeed ? 0 : 1,
      });
    }
    if (!nodeMap.has(e.head)) {
      nodeMap.set(e.head, {
        id: e.head,
        label: e.head,
        vertex: { key: e.head },
        isInitialSeed: false,
        isExpansionOrigin: false,
        importance: 0.5,
        firstSeenExpansion: 0,
        // Heads are always one step beyond their tails. Keep them
        // visually distinct from the matched-prefix origins.
        hopDistance: 1,
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
  const matchedSeed =
    nodeMap.has(tailPrefix) && tailPrefix !== "" ? tailPrefix : null;
  return wrapView(Array.from(nodeMap.values()), edges, matchedSeed);
}
