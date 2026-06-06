import { useEffect, useMemo, useRef, useState } from "react";
import Graph from "graphology";
import forceAtlas2 from "graphology-layout-forceatlas2";
import Sigma from "sigma";
import type {
  GraphEdge,
  GraphNode,
} from "~/lib/client/usecase/illuminate/selectors";
import styles from "./IlluminateCanvas.module.css";

export interface IlluminateCanvasProps {
  nodes: GraphNode[];
  edges: GraphEdge[];
  onNodeClick: (key: string) => void;
  /** When true, the canvas dims to communicate a stale frame. */
  isBusy: boolean;
}

const SEED_COLOR = "#0078d4"; // FluentUI brandColor (web theme accent)
const NODE_COLOR = "#5b5b5b";
const EDGE_COLOR = "#bdbdbd";

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
  onNodeClick,
  isBusy,
}: IlluminateCanvasProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const sigmaRef = useRef<Sigma | null>(null);
  const graphRef = useRef<Graph | null>(null);
  const [hover, setHover] = useState<HoverState | null>(null);

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
      defaultEdgeColor: EDGE_COLOR,
      defaultNodeColor: NODE_COLOR,
    });
    graphRef.current = graph;
    sigmaRef.current = renderer;

    renderer.on("clickNode", (event) => {
      onNodeClickRef.current(event.node);
    });
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
    };
    const clearHover = () => setHover(null);
    renderer.on("enterNode", showNodeHover);
    renderer.on("leaveNode", clearHover);
    renderer.on("enterEdge", showEdgeHover);
    renderer.on("leaveEdge", clearHover);

    return () => {
      renderer.kill();
      graph.clear();
      sigmaRef.current = null;
      graphRef.current = null;
    };
  }, []);

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

    for (const node of nodes) {
      const size = 4 + node.importance * 10;
      const color = node.isSeed ? SEED_COLOR : NODE_COLOR;
      const detail = describeVertex(node);
      if (graph.hasNode(node.id)) {
        graph.mergeNodeAttributes(node.id, {
          label: node.label,
          size,
          color,
          detail,
          isSeed: node.isSeed,
        });
      } else {
        // Seed the layout near the origin, neighbours in a wide ring so
        // ForceAtlas2 has somewhere to push from. Random within a circle
        // keeps the first frame stable.
        const angle = Math.random() * Math.PI * 2;
        const radius = node.isSeed ? 0 : 1 + Math.random() * 4;
        graph.addNode(node.id, {
          label: node.label,
          x: Math.cos(angle) * radius,
          y: Math.sin(angle) * radius,
          size,
          color,
          detail,
          isSeed: node.isSeed,
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
          color: EDGE_COLOR,
          label: edge.id,
          detail: `weight = ${edge.weight}`,
        });
      }
    }

    if (graph.order > 0) {
      forceAtlas2.assign(graph, {
        iterations: 80,
        settings: {
          gravity: 1,
          scalingRatio: 8,
          slowDown: 4,
          barnesHutOptimize: graph.order > 100,
        },
      });
    }

    sigma?.refresh();
  }, [nodes, edges]);

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
