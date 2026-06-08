import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Graph from "graphology";
import forceAtlas2 from "graphology-layout-forceatlas2";
import Sigma from "sigma";
import type {
  GraphEdge,
  GraphNode,
} from "~/lib/client/usecase/illuminate/selectors";
import { usePreferredTheme } from "~/lib/client/usecase/theme/use-preferred-theme";
import { decideFa2Iterations } from "./layout-iterations";
import {
  FALLBACK_PALETTE,
  LABEL_SIZE,
  LABEL_WEIGHT,
  resolvePalette,
  type SigmaPalette,
} from "./palette";
import styles from "./IlluminateCanvas.module.css";

export interface IlluminateCanvasProps {
  nodes: GraphNode[];
  edges: GraphEdge[];
  /**
   * Key of the vertex that originated the most recent expansion. Used as
   * the position hint for new nodes (#466 D7): when a node is being
   * added for the first time, we place it near the centroid of its
   * (already-placed) neighbours so ForceAtlas2 settles without yanking
   * the existing layout.
   */
  latestExpansionOrigin: string | null;
  onNodeClick: (key: string) => void;
  /** When true, the canvas dims to communicate a stale frame. */
  isBusy: boolean;
}

interface HoverState {
  kind: "node" | "edge";
  label: string;
  detail: string;
  x: number;
  y: number;
}

/**
 * Hosts a `graphology` graph and a `sigma` renderer. The component owns
 * the lifecycle of the renderer (mount on first paint, destroy on unmount)
 * and reconciles the graph in-place when the view model changes — full
 * teardown on every prop change would cost the user any in-flight layout
 * iterations.
 */
export function IlluminateCanvas({
  nodes,
  edges,
  latestExpansionOrigin,
  onNodeClick,
  isBusy,
}: IlluminateCanvasProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const sigmaRef = useRef<Sigma | null>(null);
  const graphRef = useRef<Graph | null>(null);
  /**
   * Snapshot of the node IDs the graph held at the end of the previous
   * reconcile. Diffing against the next reconcile's IDs feeds
   * `decideFa2Iterations` (#454) so we only burn an 80-iteration FA2
   * pass on cold mounts; additive expansions (the common case) cost a
   * cheap 5-iter relax and surviving vertices stay put.
   */
  const previousNodeIdsRef = useRef<Set<string>>(new Set());
  const [hover, setHover] = useState<HoverState | null>(null);
  const [palette, setPalette] = useState<SigmaPalette>(FALLBACK_PALETTE);
  // The mount effect runs ONCE, so any palette swatch read inside the
  // hover-focus reducer (#458) needs a ref so it tracks the latest
  // value after a theme flip. Keeping the ref in sync via a separate
  // useEffect avoids reinstalling the reducer on every palette change.
  const paletteRef = useRef(palette);
  useEffect(() => {
    paletteRef.current = palette;
  }, [palette]);
  const theme = usePreferredTheme();

  // Resolve theme-aware palette from FluentProvider CSS variables (#453).
  // Re-runs whenever the OS color scheme flips so Sigma stays in sync
  // with the Fluent theme without requiring a full remount.
  useEffect(() => {
    if (!containerRef.current) return;
    setPalette(resolvePalette(containerRef.current));
  }, [theme]);

  const pickFill = useCallback(
    (node: { isInitialSeed: boolean; isExpansionOrigin: boolean }) =>
      node.isInitialSeed
        ? palette.seed
        : node.isExpansionOrigin
          ? palette.origin
          : palette.baseNode,
    [palette],
  );

  // Stable callback ref so the click listener doesn't have to be rebound
  // every render (would otherwise drop hover state).
  const onNodeClickRef = useRef(onNodeClick);
  useEffect(() => {
    onNodeClickRef.current = onNodeClick;
  }, [onNodeClick]);

  // Mount sigma once. The graph and renderer survive across data updates;
  // see the reconcile effect below.
  useEffect(() => {
    if (!containerRef.current) return;
    const graph = new Graph({ multi: false, type: "directed" });
    const renderer = new Sigma(graph, containerRef.current, {
      renderLabels: true,
      labelDensity: 0.5,
      labelGridCellSize: 80,
      labelRenderedSizeThreshold: 8,
      defaultEdgeColor: FALLBACK_PALETTE.edge,
      defaultNodeColor: FALLBACK_PALETTE.baseNode,
      labelColor: { color: FALLBACK_PALETTE.labelText },
      labelSize: LABEL_SIZE,
      labelWeight: LABEL_WEIGHT,
      labelFont: FALLBACK_PALETTE.labelFont,
    });
    graphRef.current = graph;
    sigmaRef.current = renderer;

    renderer.on("clickNode", (event) => {
      onNodeClickRef.current(event.node);
    });

    // === #458 hover focus mode ==============================================
    // On hover, dim every node and edge that isn't incident to the
    // hovered vertex so the local structure pops. Idiomatic for sigma:
    // install a `nodeReducer` / `edgeReducer` that swaps the rendered
    // colour to a low-alpha swatch and hides the label. The hovered
    // node id lives in closure-local state (NOT React state) so
    // changing it doesn't rerender the parent; we just bump sigma's
    // refresh tick to re-evaluate the reducers.
    //
    // Composition note: future siblings #459 (TTL halo) and #460 (min-
    // hop coloring) will wrap these reducers. The locked composition
    // order is hop hue (#460) \u2192 TTL alpha (#459) \u2192 hover dim (#458)
    // so that hover always wins as the topmost visual filter. Keep the
    // body of these reducers small and pure so wrapping stays trivial.
    let hoveredNodeId: string | null = null;
    let focusSet: Set<string> | null = null;
    /**
     * Compute the focus set for a hovered node: `{N} \u222a neighbors(N)`.
     * Cached so the reducer doesn't re-scan adjacency every frame; the
     * cache is rebuilt only when the hovered node id changes.
     */
    const rebuildFocusSet = (key: string | null): Set<string> | null => {
      if (key === null) return null;
      if (!graph.hasNode(key)) return null;
      const s = new Set<string>([key]);
      for (const neighbour of graph.neighbors(key)) {
        s.add(neighbour);
      }
      return s;
    };
    const setHoveredNode = (key: string | null) => {
      if (hoveredNodeId === key) return;
      hoveredNodeId = key;
      focusSet = rebuildFocusSet(key);
      renderer.refresh();
    };
    renderer.setSetting("nodeReducer", (key, data) => {
      if (focusSet === null) return data;
      if (focusSet.has(key)) return data;
      return {
        ...data,
        color: paletteRef.current.dimNode,
        // Drop the label so the focused subset is the only labelled
        // group; protects against label-vs-dim-disc contrast going
        // out of WCAG-AA range (#453).
        label: "",
        // Push behind the focused subset so any overlap reads as the
        // focused node sitting on top.
        zIndex: 0,
      };
    });
    renderer.setSetting("edgeReducer", (key, data) => {
      if (focusSet === null || hoveredNodeId === null) return data;
      const [src, dst] = graph.extremities(key);
      // Incident edges stay at full saturation so the local subgraph
      // structure is obvious.
      if (src === hoveredNodeId || dst === hoveredNodeId) {
        return { ...data, zIndex: 1 };
      }
      return { ...data, color: paletteRef.current.dimEdge, zIndex: 0 };
    });
    // Sigma needs zIndex sorting opted-in to honour the per-element
    // zIndex hints emitted by the reducers above.
    renderer.setSetting("zIndex", true);
    // === end #458 ===========================================================

    const showNodeHover = (event: {
      node: string;
      event: { x: number; y: number };
    }) => {
      const attrs = graph.getNodeAttributes(event.node) as {
        label?: string;
        detail?: string;
      };
      setHover({
        kind: "node",
        label: attrs.label ?? event.node,
        detail: attrs.detail ?? "",
        x: event.event.x,
        y: event.event.y,
      });
      // #458: trigger hover focus mode for the node under the cursor.
      setHoveredNode(event.node);
    };
    const showEdgeHover = (event: {
      edge: string;
      event: { x: number; y: number };
    }) => {
      const attrs = graph.getEdgeAttributes(event.edge) as {
        label?: string;
        detail?: string;
      };
      setHover({
        kind: "edge",
        label: attrs.label ?? event.edge,
        detail: attrs.detail ?? "",
        x: event.event.x,
        y: event.event.y,
      });
      // #458: hovering an edge focuses both endpoints so the edge and
      // its incidents pop together \u2014 mirrors the issue's acceptance
      // criterion for "enterEdge: highlight both endpoints + the edge
      // itself."
      const [src] = graph.extremities(event.edge);
      setHoveredNode(src);
    };
    const clearHoverNode = () => {
      setHover(null);
      // #458: leaving any node/edge restores the full graph.
      setHoveredNode(null);
    };
    renderer.on("enterNode", showNodeHover);
    renderer.on("leaveNode", clearHoverNode);
    renderer.on("enterEdge", showEdgeHover);
    renderer.on("leaveEdge", clearHoverNode);

    // === #455 drag-to-pin ===================================================
    // Sigma's standard drag-and-drop recipe. The dragged node id lives in
    // a closure-local ref (not React state) so a mid-drag rerender can't
    // wipe it. On mouse-down we suspend sigma's default camera-pan via
    // `preventSigmaDefault()` and mark the node highlighted so the user
    // gets a visual lock-on. On `mousemovebody` we project the cursor
    // into graph space and rewrite the node's x/y; sigma re-renders on
    // the next frame. On mouse-up we set `fixed: true` so the per-#454
    // additive FA2 relax (and any future reseed) leaves the user-placed
    // node alone — graphology FA2 honors `fixed` directly (verified
    // against `graphology-layout-forceatlas2/helpers.js` line 144).
    const draggedNodeRef: { current: string | null } = { current: null };
    // Diagnostic counters exposed via the test bridge so #455's e2e can
    // disambiguate "drag fired but didn't move the node" from "drag
    // never fired at all" without resorting to console logs.
    const dragStats = { downNode: 0, moveBody: 0, mouseUp: 0 };
    renderer.on("downNode", (payload) => {
      dragStats.downNode += 1;
      draggedNodeRef.current = payload.node;
      graph.setNodeAttribute(payload.node, "highlighted", true);
      payload.preventSigmaDefault();
    });
    const mouseCaptor = renderer.getMouseCaptor();
    mouseCaptor.on("mousemovebody", (coords) => {
      const node = draggedNodeRef.current;
      if (!node) return;
      dragStats.moveBody += 1;
      const pos = renderer.viewportToGraph({ x: coords.x, y: coords.y });
      graph.setNodeAttribute(node, "x", pos.x);
      graph.setNodeAttribute(node, "y", pos.y);
      // Stop the underlying mouse event from triggering camera pan.
      coords.preventSigmaDefault();
      if (coords.original instanceof MouseEvent) {
        coords.original.preventDefault();
        coords.original.stopPropagation();
      }
    });
    const finishDrag = () => {
      dragStats.mouseUp += 1;
      const node = draggedNodeRef.current;
      if (!node) return;
      graph.setNodeAttribute(node, "highlighted", false);
      // Pin: subsequent FA2 relaxations (#454) skip this node.
      graph.setNodeAttribute(node, "fixed", true);
      draggedNodeRef.current = null;
    };
    mouseCaptor.on("mouseup", finishDrag);
    // === end #455 ===========================================================

    // Read-only Playwright test bridge (#454, extended in #455/#458).
    // Lets the e2e suite assert surviving-node positions across an
    // additive expansion, the position/pin state after a drag, and the
    // post-reducer colour after a hover \u2014 all without resorting to
    // flaky pixel diffs. Harmless in production: all production-facing
    // accessors are pure read-only views over the live graphology +
    // sigma state; the two write methods (`simulateDrag`, `setHoveredNode`)
    // mirror the behaviour the real user pointer would produce, and
    // the same internal helpers are invoked.
    const win = window as Window & {
      __illuminateCanvas?: {
        getNodePosition: (key: string) => { x: number; y: number } | null;
        isNodeFixed: (key: string) => boolean;
        dragStats: () => {
          downNode: number;
          moveBody: number;
          mouseUp: number;
        };
        // Test-only: fires a synthetic drag (downNode \u2192 mousemovebody
        // \u2192 mouseup \u2192 finishDrag) by invoking the same closure-local
        // handlers the real sigma events trigger. We use this instead
        // of dispatching real `MouseEvent`s through the page because
        // sigma's `downNode` hit-test reads from a WebGL picking
        // framebuffer that headless chromium populates only
        // intermittently across serial test runs.
        simulateDrag: (
          key: string,
          deltaGraphX: number,
          deltaGraphY: number,
        ) => boolean;
        /**
         * #458 test bridge: set or clear the hover-focus subject.
         * Same effect as a real `enterNode` / `leaveNode` event: the
         * reducer chain re-evaluates and the post-reducer colours
         * become observable via {@link getRenderedNodeColor}.
         * Returns `true` when the supplied key exists (or is `null`),
         * `false` if the key isn't in the live graph.
         */
        setHoveredNode: (key: string | null) => boolean;
        /**
         * #458 test bridge: read the post-reducer render colour for a
         * given node id. Returns `null` if the node is unknown.
         */
        getRenderedNodeColor: (key: string) => string | null;
        /**
         * #458 test bridge: read the post-reducer render colour for a
         * given edge id. Returns `null` if the edge is unknown.
         */
        getRenderedEdgeColor: (key: string) => string | null;
        /**
         * #458 test bridge: read whether the hover-focus reducer is
         * currently active (i.e., a node is focused).
         */
        hoveredNode: () => string | null;
      };
    };
    win.__illuminateCanvas = {
      getNodePosition: (key: string) => {
        if (!graph.hasNode(key)) return null;
        const attrs = graph.getNodeAttributes(key) as {
          x?: number;
          y?: number;
        };
        if (typeof attrs.x !== "number" || typeof attrs.y !== "number") {
          return null;
        }
        return { x: attrs.x, y: attrs.y };
      },
      isNodeFixed: (key: string) => {
        if (!graph.hasNode(key)) return false;
        return graph.getNodeAttribute(key, "fixed") === true;
      },
      dragStats: () => ({ ...dragStats }),
      simulateDrag: (key: string, deltaGraphX: number, deltaGraphY: number) => {
        if (!graph.hasNode(key)) return false;
        const attrs = graph.getNodeAttributes(key) as {
          x?: number;
          y?: number;
        };
        if (typeof attrs.x !== "number" || typeof attrs.y !== "number") {
          return false;
        }
        // 1) downNode \u2014 latch the dragged node
        draggedNodeRef.current = key;
        graph.setNodeAttribute(key, "highlighted", true);
        dragStats.downNode += 1;
        // 2) mousemovebody \u2014 write the new position. We don't go
        //    through viewportToGraph because the test supplies graph
        //    deltas directly.
        graph.setNodeAttribute(key, "x", attrs.x + deltaGraphX);
        graph.setNodeAttribute(key, "y", attrs.y + deltaGraphY);
        dragStats.moveBody += 1;
        // 3) mouseup \u2014 release + pin
        finishDrag();
        return true;
      },
      setHoveredNode: (key: string | null) => {
        if (key !== null && !graph.hasNode(key)) return false;
        setHoveredNode(key);
        return true;
      },
      getRenderedNodeColor: (key: string) => {
        if (!graph.hasNode(key)) return null;
        const data = renderer.getNodeDisplayData(key);
        return data?.color ?? null;
      },
      getRenderedEdgeColor: (key: string) => {
        if (!graph.hasEdge(key)) return null;
        const data = renderer.getEdgeDisplayData(key);
        return data?.color ?? null;
      },
      hoveredNode: () => hoveredNodeId,
    };

    return () => {
      renderer.kill();
      graph.clear();
      sigmaRef.current = null;
      graphRef.current = null;
      previousNodeIdsRef.current = new Set();
      delete win.__illuminateCanvas;
    };
  }, []);

  // Apply palette changes to sigma's global settings + repaint existing
  // node fills. Splitting this out from the reconcile effect lets a
  // theme toggle re-skin the canvas without re-running ForceAtlas2.
  useEffect(() => {
    const sigma = sigmaRef.current;
    const graph = graphRef.current;
    if (!sigma || !graph) return;
    sigma.setSetting("defaultNodeColor", palette.baseNode);
    sigma.setSetting("defaultEdgeColor", palette.edge);
    sigma.setSetting("labelColor", { color: palette.labelText });
    sigma.setSetting("labelFont", palette.labelFont);
    for (const id of graph.nodes()) {
      const attrs = graph.getNodeAttributes(id) as {
        isInitialSeed?: boolean;
        isExpansionOrigin?: boolean;
      };
      graph.setNodeAttribute(
        id,
        "color",
        pickFill({
          isInitialSeed: !!attrs.isInitialSeed,
          isExpansionOrigin: !!attrs.isExpansionOrigin,
        }),
      );
    }
    for (const id of graph.edges()) {
      graph.setEdgeAttribute(id, "color", palette.edge);
    }
    sigma.refresh();
  }, [palette, pickFill]);

  // Reconcile the graph with the latest view model. We diff by ID rather
  // than clearing so ForceAtlas2 can keep the positions of nodes that
  // survived from the previous frame — the canvas reads as a smooth
  // expansion instead of a layout reshuffle on every refetch.
  useEffect(() => {
    const graph = graphRef.current;
    if (!graph) return;
    const sigma = sigmaRef.current;

    const nextNodeIds = new Set(nodes.map((n) => n.id));
    const nextEdgeIds = new Set(edges.map((e) => e.id));

    // Per-reconcile diff used by `decideFa2Iterations` (#454). We
    // compute it BEFORE mutating the graph so the counts reflect the
    // structural delta, not the post-merge state.
    const previousNodeIds = previousNodeIdsRef.current;
    const previousNodeCount = previousNodeIds.size;
    let addedCount = 0;
    let droppedCount = 0;
    for (const id of nextNodeIds) {
      if (!previousNodeIds.has(id)) addedCount += 1;
    }
    for (const id of previousNodeIds) {
      if (!nextNodeIds.has(id)) droppedCount += 1;
    }

    // Drop edges first to avoid orphan references when we drop nodes.
    for (const edgeId of graph.edges()) {
      if (!nextEdgeIds.has(edgeId)) {
        graph.dropEdge(edgeId);
      }
    }
    for (const nodeId of graph.nodes()) {
      if (!nextNodeIds.has(nodeId)) {
        graph.dropNode(nodeId);
      }
    }

    // Per-node neighbour lookup for the placement hint. We build it once
    // per reconcile so we don't rescan the edge list per node below.
    const neighboursByKey = buildNeighbourMap(nodes, edges);

    for (const node of nodes) {
      const size = 4 + node.importance * 10;
      const color = pickFill(node);
      const detail = describeVertex(node);
      if (graph.hasNode(node.id)) {
        graph.mergeNodeAttributes(node.id, {
          label: node.label,
          size,
          color,
          detail,
          isInitialSeed: node.isInitialSeed,
          isExpansionOrigin: node.isExpansionOrigin,
        });
      } else {
        const { x, y } = pickInitialPosition({
          graph,
          node,
          neighbours: neighboursByKey.get(node.id) ?? [],
          latestExpansionOrigin,
        });
        graph.addNode(node.id, {
          label: node.label,
          x,
          y,
          size,
          color,
          detail,
          isInitialSeed: node.isInitialSeed,
          isExpansionOrigin: node.isExpansionOrigin,
        });
      }
    }
    for (const edge of edges) {
      if (graph.hasEdge(edge.id)) {
        graph.mergeEdgeAttributes(edge.id, {
          size: 1 + Math.min(4, edge.weight),
          label: edge.id,
          detail: `weight = ${edge.weight}`,
        });
      } else {
        graph.addEdgeWithKey(edge.id, edge.source, edge.target, {
          size: 1 + Math.min(4, edge.weight),
          color: palette.edge,
          label: edge.id,
          detail: `weight = ${edge.weight}`,
        });
      }
    }

    // Per #454: only burn an 80-iteration FA2 pass on a cold mount or
    // post-drop reseed; the common additive case relaxes in 5 so
    // surviving vertices stay within roughly ±10% of their prior pixel
    // position.
    const iterations = decideFa2Iterations({
      previousNodeCount,
      addedCount,
      droppedCount,
      nextNodeCount: nextNodeIds.size,
    });
    if (iterations > 0 && graph.order > 0) {
      forceAtlas2.assign(graph, {
        iterations,
        settings: {
          gravity: 1,
          scalingRatio: 8,
          slowDown: 4,
          barnesHutOptimize: graph.order > 100,
        },
      });
    }

    previousNodeIdsRef.current = nextNodeIds;

    sigma?.refresh();
  }, [nodes, edges, latestExpansionOrigin, palette, pickFill]);

  const empty = nodes.length === 0;
  const wrapperClass = useMemo(
    () => [styles.wrapper, isBusy ? styles.busy : ""].filter(Boolean).join(" "),
    [isBusy],
  );

  return (
    <div className={wrapperClass} data-testid="illuminate-canvas">
      <div ref={containerRef} className={styles.canvas} aria-hidden="true" />
      {empty ? (
        <div className={styles.emptyOverlay}>
          <span>No vertices to display.</span>
        </div>
      ) : null}
      {hover ? (
        <div
          className={styles.tooltip}
          role="tooltip"
          ref={(el) => {
            if (!el) return;
            el.style.setProperty("--tooltip-x", `${hover.x + 12}px`);
            el.style.setProperty("--tooltip-y", `${hover.y + 12}px`);
          }}
        >
          <div className={styles.tooltipKind}>{hover.kind}</div>
          <div className={styles.tooltipLabel}>{hover.label}</div>
          {hover.detail ? (
            <div className={styles.tooltipDetail}>{hover.detail}</div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

/**
 * Build an undirected neighbour map: for each node id, the keys of its
 * neighbours (in either direction). Used by `pickInitialPosition` to
 * seed a new node near its existing neighbours, per #466 D7.
 */
function buildNeighbourMap(
  nodes: GraphNode[],
  edges: GraphEdge[],
): Map<string, string[]> {
  const keys = new Set(nodes.map((n) => n.id));
  const out = new Map<string, string[]>();
  for (const e of edges) {
    if (!keys.has(e.source) || !keys.has(e.target)) continue;
    appendNeighbour(out, e.source, e.target);
    appendNeighbour(out, e.target, e.source);
  }
  return out;
}

function appendNeighbour(
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
 * Pick an (x, y) for a node that's being added to the graph for the
 * first time.
 *
 * Priority order:
 *  1. The initial seed sits at the origin.
 *  2. If the node has neighbours that are ALREADY placed in graphology,
 *     drop it at their centroid with small jitter — ForceAtlas2 then
 *     only has to nudge it into place instead of yanking the whole
 *     layout.
 *  3. If the latest expansion origin is placed, drop the node in a ring
 *     around it (#466 D7 explicitly calls out "near the parent click").
 *  4. Otherwise, the original random-in-a-circle seeding.
 */
function pickInitialPosition({
  graph,
  node,
  neighbours,
  latestExpansionOrigin,
}: {
  graph: Graph;
  node: GraphNode;
  neighbours: string[];
  latestExpansionOrigin: string | null;
}): { x: number; y: number } {
  if (node.isInitialSeed) {
    return { x: 0, y: 0 };
  }
  // 2 — centroid of already-placed neighbours.
  let sumX = 0;
  let sumY = 0;
  let placedCount = 0;
  for (const neighbour of neighbours) {
    if (!graph.hasNode(neighbour)) continue;
    const attrs = graph.getNodeAttributes(neighbour) as {
      x?: number;
      y?: number;
    };
    if (typeof attrs.x !== "number" || typeof attrs.y !== "number") continue;
    sumX += attrs.x;
    sumY += attrs.y;
    placedCount += 1;
  }
  if (placedCount > 0) {
    const cx = sumX / placedCount;
    const cy = sumY / placedCount;
    // Small jitter so colocated nodes don't sit exactly on top of each
    // other (would make ForceAtlas2's first iteration meaningless).
    return {
      x: cx + (Math.random() - 0.5) * 0.5,
      y: cy + (Math.random() - 0.5) * 0.5,
    };
  }
  // 3 — ring around the latest expansion origin if it's placed.
  if (latestExpansionOrigin !== null && graph.hasNode(latestExpansionOrigin)) {
    const attrs = graph.getNodeAttributes(latestExpansionOrigin) as {
      x?: number;
      y?: number;
    };
    const ox = typeof attrs.x === "number" ? attrs.x : 0;
    const oy = typeof attrs.y === "number" ? attrs.y : 0;
    const angle = Math.random() * Math.PI * 2;
    const radius = 1 + Math.random() * 2;
    return {
      x: ox + Math.cos(angle) * radius,
      y: oy + Math.sin(angle) * radius,
    };
  }
  // 4 — fallback random-in-circle seeding.
  const angle = Math.random() * Math.PI * 2;
  const radius = 1 + Math.random() * 4;
  return { x: Math.cos(angle) * radius, y: Math.sin(angle) * radius };
}

function describeVertex(node: GraphNode): string {
  const v = node.vertex;
  if (v.bool !== undefined) return `bool = ${v.bool}`;
  if (v.int32 !== undefined) return `int32 = ${v.int32}`;
  if (v.int64 !== undefined) return `int64 = ${v.int64}`;
  if (v.uint32 !== undefined) return `uint32 = ${v.uint32}`;
  if (v.uint64 !== undefined) return `uint64 = ${v.uint64}`;
  if (v.float32 !== undefined) return `float32 = ${v.float32}`;
  if (v.float64 !== undefined) return `float64 = ${v.float64}`;
  if (v.string !== undefined) return `string = ${truncate(v.string)}`;
  if (v.timestamp !== undefined) return `timestamp = ${v.timestamp}`;
  if (v.duration !== undefined) return `duration = ${v.duration}`;
  if (v.bytes !== undefined) return "bytes = …";
  if (v.nil) return "nil";
  return "";
}

function truncate(s: string): string {
  return s.length > 64 ? `${s.slice(0, 63)}…` : s;
}
