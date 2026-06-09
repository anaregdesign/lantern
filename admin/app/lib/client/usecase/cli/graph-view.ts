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
import { coerceValue, ttlSecondsToExpiration } from "./dispatcher";

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
    // #483: the CLI surface is stateless — every command overwrites the
    // previous frame, so the whole view IS the latest result and there
    // are no accumulator survivors to hide. Empty sets tell the canvas
    // to apply no result filter (show everything).
    latestResultVertexKeys: new Set<string>(),
    latestResultEdgeIds: new Set<string>(),
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

/**
 * The graph elements a mutating verb (`put`/`add`) contributes to the
 * canvas (#518). Unlike {@link commandResultToGraphView} — which REPLACES
 * the frame for read verbs — a merge is folded additively onto the live
 * frame (see {@link mergeGraphView}) so a `put`/`add` shows up immediately
 * without discarding the operator's current exploration context.
 */
export interface GraphMerge {
  /** Nodes to upsert. A node carrying a value replaces an existing one;
   *  a key-only edge-endpoint placeholder never clobbers a richer node. */
  nodes: GraphNode[];
  /** Edges to upsert. */
  edges: GraphEdge[];
  /** True for `add edge` (the server sums weights additively); false for
   *  `put`, which overwrites. Decides merge-vs-replace for an existing
   *  edge of the same id. */
  additive: boolean;
  /** The key the mutation is "about" (put-vertex key / edge tail). Used as
   *  the hop origin when the canvas is otherwise empty, so a put into a
   *  cold canvas opens focused on the new element. */
  focus: string | null;
}

/**
 * Project a mutating `put`/`add` command into the {@link GraphMerge} the
 * reducer folds onto the live canvas frame (#518). Returns `null` for
 * every verb that already produces a full {@link GraphView} (via
 * {@link commandResultToGraphView}) or carries nothing to show
 * (`delete`, `exit`) — those keep their existing behaviour.
 *
 * Pure: derives entirely from the parsed command (the dispatcher's write
 * responses carry no echo we need). Reuses the dispatcher's `coerceValue`
 * / `ttlSecondsToExpiration` so the rendered vertex matches exactly what
 * the dispatcher sent over the wire.
 */
export function commandResultToGraphMerge(command: Command): GraphMerge | null {
  if (command.verb === "put" && command.objective === "vertex") {
    const vertex: Vertex = {
      key: command.key,
      ...coerceValue(command.value),
      expiration: ttlSecondsToExpiration(command.ttlSeconds),
    };
    return {
      nodes: [mergeNode(command.key, vertex)],
      edges: [],
      additive: false,
      focus: command.key,
    };
  }
  if (
    (command.verb === "put" || command.verb === "add") &&
    command.objective === "edge"
  ) {
    const edge: Edge = {
      tail: command.tail,
      head: command.head,
      weight: command.weight,
      expiration: ttlSecondsToExpiration(command.ttlSeconds),
    };
    return {
      // Key-only endpoint placeholders: if either vertex already lives on
      // the canvas (with a value), the merge keeps the richer copy.
      nodes: [
        mergeNode(command.tail, { key: command.tail }),
        mergeNode(command.head, { key: command.head }),
      ],
      edges: [
        {
          id: `${command.tail}→${command.head}`,
          source: command.tail,
          target: command.head,
          weight: command.weight,
          edge,
        },
      ],
      additive: command.verb === "add",
      focus: command.tail,
    };
  }
  return null;
}

/** Build a neutral merge node. Hop distance / origin flags are finalised
 *  by {@link mergeGraphView} once the full post-merge node + edge set is
 *  known. */
function mergeNode(key: string, vertex: Vertex): GraphNode {
  return {
    id: key,
    label: key,
    vertex,
    isInitialSeed: false,
    isExpansionOrigin: false,
    importance: 0.5,
    firstSeenExpansion: 0,
    hopDistance: Number.POSITIVE_INFINITY,
  };
}

/**
 * Fold a {@link GraphMerge} onto the current canvas frame (#518),
 * returning a fresh {@link GraphView}. Additive by construction so the
 * operator's exploration context survives a write:
 *
 *  - **nodes** upsert — an incoming node refreshes an existing one's
 *    payload only when it carries a value ({@link vertexHasValue}), while
 *    preserving that node's role (seed / importance) in the current frame;
 *    a bare edge-endpoint placeholder never overwrites a vertex already on
 *    the canvas.
 *  - **edges** upsert — for `add edge` an existing edge's weight is summed
 *    (mirroring the server's additive semantics); `put edge` replaces.
 *  - **hop distances** are recomputed from the surviving origins (or the
 *    merge `focus` when the canvas was empty) so ramp colours stay
 *    consistent.
 *
 * When `current` is `null` (cold canvas) the merge becomes the whole
 * frame, anchored on the new element.
 */
export function mergeGraphView(
  current: GraphView | null,
  merge: GraphMerge,
): GraphView {
  const base = current ?? emptyView();
  const wasEmpty = base.nodes.length === 0;

  const nodeById = new Map(base.nodes.map((n) => [n.id, n]));
  for (const incoming of merge.nodes) {
    const existing = nodeById.get(incoming.id);
    if (!existing) {
      nodeById.set(incoming.id, { ...incoming });
    } else if (vertexHasValue(incoming.vertex)) {
      // Refresh the payload (value + expiration) but keep the node's
      // existing role/importance in the current frame — a put must not
      // demote the seed the operator is exploring around.
      nodeById.set(incoming.id, { ...existing, vertex: incoming.vertex });
    }
    // else: key-only placeholder for a node already present → keep it.
  }

  const edgeById = new Map(base.edges.map((e) => [e.id, e]));
  for (const incoming of merge.edges) {
    const existing = edgeById.get(incoming.id);
    if (existing && merge.additive) {
      const weight = existing.weight + incoming.weight;
      edgeById.set(incoming.id, {
        ...incoming,
        weight,
        edge: { ...incoming.edge, weight },
      });
    } else {
      edgeById.set(incoming.id, incoming);
    }
  }

  const nodes = Array.from(nodeById.values());
  const edges = Array.from(edgeById.values());

  // Keep the established origins when the canvas already had a frame;
  // otherwise anchor hop colouring on the merge focus.
  const origins =
    base.expansionOrigins.length > 0
      ? base.expansionOrigins
      : merge.focus !== null && merge.focus !== ""
        ? [merge.focus]
        : [];
  const knownKeys = new Set(nodes.map((n) => n.id));
  const hopByKey = computeHopDistances(knownKeys, edges, origins);
  const originSet = new Set(origins);
  const rehopped = nodes.map((n) => ({
    ...n,
    hopDistance: hopByKey.get(n.id) ?? Number.POSITIVE_INFINITY,
    // Only promote a node to origin/seed when the canvas started empty —
    // a put into an existing frame must not steal the seed halo from the
    // node the operator is already exploring around.
    isExpansionOrigin: n.isExpansionOrigin || (wasEmpty && originSet.has(n.id)),
    isInitialSeed: n.isInitialSeed || (wasEmpty && originSet.has(n.id)),
  }));

  return {
    nodes: rehopped,
    edges,
    latestExpansionOrigin: wasEmpty
      ? (merge.focus ?? base.latestExpansionOrigin)
      : base.latestExpansionOrigin,
    expansionOrigins: origins,
    overSoftCap: base.overSoftCap,
    latestResultVertexKeys: new Set<string>(),
    latestResultEdgeIds: new Set<string>(),
  };
}

/** Empty additive-model frame — the base a merge folds onto when the
 *  canvas has no prior view. */
function emptyView(): GraphView {
  return {
    nodes: [],
    edges: [],
    latestExpansionOrigin: null,
    expansionOrigins: [],
    overSoftCap: false,
    latestResultVertexKeys: new Set<string>(),
    latestResultEdgeIds: new Set<string>(),
  };
}

/** True when a vertex carries any value field beyond its identity
 *  (`key`) and lifetime (`expiration`). Field-name-agnostic so it
 *  survives additions to the value oneof. */
function vertexHasValue(vertex: Vertex): boolean {
  for (const k of Object.keys(vertex)) {
    if (k !== "key" && k !== "expiration") return true;
  }
  return false;
}
