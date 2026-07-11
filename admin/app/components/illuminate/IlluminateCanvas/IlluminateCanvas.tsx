import {
  useCallback,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
  type Ref,
} from "react";
import Graph from "graphology";
import Sigma from "sigma";
import { EdgeArrowProgram } from "sigma/rendering";
import { Info16Regular } from "@fluentui/react-icons";
import {
  selectHopBucketCounts,
  selectRenderTargets,
  type GraphEdge,
  type GraphNode,
  type HopBucketKey,
} from "~/lib/client/usecase/illuminate/selectors";
import {
  decideLayoutRegime,
  diffRenderSets,
} from "~/lib/client/usecase/illuminate/reconcile";
import {
  FORCE_ALPHA,
  FORCE_ALPHA_COLD,
  type ForceLink,
  type ForceNode,
} from "~/lib/client/usecase/illuminate/force-layout";
import { useIlluminateCanvas } from "~/lib/client/usecase/illuminate/use-illuminate-canvas";
import { usePreferredTheme } from "~/lib/client/usecase/theme/use-preferred-theme";
import { formatEdgeWeight, makeDrawEdgeLabel } from "./edge-label";
import { HOP_FAR_THRESHOLD, colorForHop, describeHop } from "./hop-palette";
import { makeDrawNodeHover } from "./hover-label";
import { makeDrawNodeLabel } from "./node-label";
import {
  EDGE_LABEL_SIZE,
  EDGE_LABEL_WEIGHT,
  FALLBACK_PALETTE,
  LABEL_SIZE,
  LABEL_WEIGHT,
  resolvePalette,
  type SigmaPalette,
} from "./palette";
import {
  applyTtlFade,
  applyWarningTint,
  computeTtlFraction,
  isInWarningWindow,
  warningUrgency,
} from "./ttl-decay";
import styles from "./IlluminateCanvas.module.css";

export interface IlluminateCanvasProps {
  nodes: GraphNode[];
  edges: GraphEdge[];
  /**
   * Key of the vertex that originated the most recent expansion. Used as
   * the position hint for new nodes (#466 D7): when a node is being
   * added for the first time, we place it near the centroid of its
   * (already-placed) neighbours so the continuous d3-force layout (#483)
   * settles without yanking the existing layout.
   */
  latestExpansionOrigin: string | null;
  /**
   * Vertex keys belonging to the most recent expansion's result (#483,
   * delete-not-hide per #491). The canvas renders ONLY this set: every
   * node whose id is absent from it is dropped from graphology, so each
   * graph switch deletes the previous frame's nodes and lets d3-force
   * re-converge the new frame from scratch (no retained/frozen
   * positions). An empty set means "no result yet" (cold start / Clear)
   * — the canvas falls back to the full accumulator (itself empty then).
   */
  latestResultVertexKeys: Set<string>;
  /**
   * Edge ids belonging to the most recent expansion's result (#483).
   * Same delete-only semantics as {@link latestResultVertexKeys}.
   */
  latestResultEdgeIds: Set<string>;
  onNodeClick: (key: string) => void;
  /**
   * Fired when the user activates a node's info icon (#461). Distinct
   * from `onNodeClick` (which drives the additive expansion on a node
   * body click): inspecting opens the read-only detail Drawer and must
   * NOT expand or refetch. Optional so the canvas stays reusable from
   * contexts that don't surface a detail panel.
   */
  onNodeInspect?: (key: string) => void;
  /** When true, the canvas dims to communicate a stale frame. */
  isBusy: boolean;
  /**
   * #516: fill the parent container (absolute `inset: 0`) instead of
   * imposing the standalone `70vh`/`min-height` sizing. The `/cli` split
   * view embeds the canvas in a height-constrained, `overflow: hidden`
   * panel; without this the fixed-height wrapper overflows the panel and
   * clips the bottom-left hop-distance legend on short viewports.
   * Leaving it unset keeps the standalone `70vh` sizing.
   */
  fill?: boolean;
  /**
   * Imperative handle (#456). The expansion chip strip calls
   * `panToNode(originKey)` to scroll the camera to a previous click
   * point without mutating state or refetching. Optional — keeps the
   * canvas reusable from contexts that don't need camera control.
   */
  ref?: Ref<IlluminateCanvasHandle>;
}

/**
 * Imperative API exposed to parent components via `ref` (#456). Only
 * pan-to-node is published today; the rest of the canvas's surface
 * remains declarative (props) so test-bridge methods stay isolated to
 * `window.__illuminateCanvas`.
 */
export interface IlluminateCanvasHandle {
  /**
   * Animate the sigma camera so the supplied vertex is centred, then
   * briefly highlight it (~600 ms). Returns `false` when the key is
   * not currently in the graph (e.g., the user cleared the
   * accumulator). Repeated calls cancel the previous highlight so
   * rapidly clicked chips don't stack pulse timers on each other.
   */
  panToNode: (
    key: string,
    options?: { duration?: number; highlightMs?: number; ratio?: number },
  ) => boolean;
}

interface HoverState {
  kind: "node" | "edge";
  label: string;
  detail: string;
  x: number;
  y: number;
}

/**
 * Per-node info-icon position (#461). Viewport-pixel coordinates of the
 * icon button that, when activated, opens the read-only detail Drawer.
 */
interface InfoIconState {
  key: string;
  x: number;
  y: number;
}

/**
 * #461 info-icon tuning.
 *
 * - `INFO_ICON_HIDE_DELAY_MS`: grace period after the cursor leaves a
 *   node before the icon hides, so the user can travel from the node to
 *   the icon without it disappearing (the standard hover-bridge
 *   pattern).
 * - `INFO_ICON_DRAG_TOLERANCE_PX`: pointer travel (viewport px) at or
 *   below which a press → release on the icon counts as a click; beyond
 *   it the gesture is treated as a drag and the inspect is suppressed so
 *   #455 drag-to-pin keeps precedence.
 * - `INFO_ICON_OFFSET_*`: where the icon sits relative to the node's
 *   viewport centre.
 */
const INFO_ICON_HIDE_DELAY_MS = 160;
const INFO_ICON_DRAG_TOLERANCE_PX = 4;
const INFO_ICON_OFFSET_X = 10;
const INFO_ICON_OFFSET_Y = 18;

/**
 * #500 node sizing. The per-expansion seed (the canvas's pinned anchor)
 * renders at {@link SEED_NODE_SIZE}; every other node renders at the
 * single uniform {@link DEFAULT_NODE_SIZE}. Sizes feed both the sigma
 * disc radius and the d3-force collision radius, so the larger seed also
 * claims more space — reinforcing its role as the layout's fixed point.
 */
const DEFAULT_NODE_SIZE = 7;
const SEED_NODE_SIZE = 16;

/**
 * #514 camera re-frame duration (ms). After a click-to-expand or a fresh
 * seed reshapes the rendered subgraph, the reconcile effect animates the
 * camera back to its default framing over this window so the new result
 * is centred and zoomed to fit. Long enough to read as a smooth glide,
 * short enough not to lag the layout ease that runs alongside it.
 */
const CAMERA_FIT_MS = 500;

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
  latestResultVertexKeys,
  latestResultEdgeIds,
  onNodeClick,
  onNodeInspect,
  isBusy,
  fill = false,
  ref,
}: IlluminateCanvasProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const sigmaRef = useRef<Sigma | null>(null);
  const graphRef = useRef<Graph | null>(null);
  /**
   * #456 highlight-cancellation handle. `panToNode` schedules a
   * setTimeout to revert the `highlighted` attribute it sets on the
   * pulsing node; if the user clicks another chip before the pulse
   * expires, the prior timeout must be cancelled (and its node
   * un-highlighted) so the new pulse doesn't get cleared early.
   */
  const highlightTimeoutRef = useRef<{
    handle: ReturnType<typeof setTimeout>;
    nodeKey: string;
    previousHighlighted: boolean;
  } | null>(null);
  /**
   * Snapshot of the node IDs the graph held at the end of the previous
   * reconcile. Diffing against the next reconcile's IDs tells us whether
   * this is a cold mount (empty → populated, full synchronous layout),
   * an incremental expansion (animated easing, #483), or a no-op
   * structural delta (just re-apply the hide filter).
   */
  const previousNodeIdsRef = useRef<Set<string>>(new Set());
  /**
   * Snapshot of the edge IDs the graph held at the end of the previous
   * reconcile (#500). Diffing against the next reconcile's edge IDs lets
   * an expansion that only adds/removes EDGES among already-present nodes
   * (no node delta) still trigger an animated reheat of the whole render
   * set, so existing nodes move on every structural expansion.
   */
  const previousEdgeIdsRef = useRef<Set<string>>(new Set());
  /**
   * Wall clock the TTL reducer reads on every frame (#459). Bumped by
   * the 1 Hz tick effect below; the mount effect captures `.current`
   * inside the reducer so a single value flows from the tick into both
   * the node and edge reducers without re-installing them.
   *
   * Kept as a ref (not React state) so a tick doesn't rerender the
   * canvas \u2014 the only observable effect of a tick is the reducer
   * output, which sigma picks up via the explicit `refresh()` call.
   */
  const nowRef = useRef<number>(Date.now());
  /**
   * Test-only override for {@link nowRef}. When set, the tick effect
   * reads from this instead of `Date.now()` so e2e tests can simulate
   * the passage of time without waiting real seconds. Production code
   * never writes here.
   */
  const nowOverrideRef = useRef<number | null>(null);
  /**
   * Diagnostic counter exposed via the test bridge: how many TTL
   * refresh ticks have actually executed. The e2e suite uses this to
   * verify that the tick pauses while `document.visibilityState ===
   * "hidden"` (acceptance criterion 5) without having to wait real
   * wall-clock seconds.
   */
  const tickCountRef = useRef<number>(0);
  const [hover, setHover] = useState<HoverState | null>(null);
  /**
   * #461 per-node info icon. `null` when no node is hovered. Held as
   * React state (not a ref) because the icon is rendered declaratively
   * in JSX; a ref mirror ({@link infoIconRef}) lets the mount-effect
   * closure (test bridge) read it without re-installing handlers.
   */
  const [infoIcon, setInfoIcon] = useState<InfoIconState | null>(null);
  const infoIconRef = useRef<InfoIconState | null>(null);
  useEffect(() => {
    infoIconRef.current = infoIcon;
  }, [infoIcon]);
  /**
   * Viewport coords of the pointer-down that began the current info-icon
   * press (#461). Compared against the click position so a press that
   * travelled more than {@link INFO_ICON_DRAG_TOLERANCE_PX} is treated
   * as a drag and suppresses the inspect — keeping #455 drag-to-pin the
   * dominant gesture. `null` for keyboard activation, which always
   * inspects.
   */
  const infoIconPointerDownRef = useRef<{ x: number; y: number } | null>(null);
  /**
   * #517: label-visibility toggles. Both default on. These drive sigma's
   * `renderLabels` / `renderEdgeLabels` settings, which gate ALL node /
   * edge labels regardless of any per-element `forceLabel`, so flipping
   * them is the clean way to hide one label class without touching the
   * graphology label attributes (which the test bridge still reads).
   */
  const [showNodeLabels, setShowNodeLabels] = useState(true);
  const [showEdgeLabels, setShowEdgeLabels] = useState(true);
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

  // #517: keep sigma's label-visibility settings in lockstep with the
  // toggles. The canvas mounts once and survives data updates, so these
  // small effects are the single writers of these two settings (the
  // palette effect never touches them). `refresh()` repaints immediately.
  useEffect(() => {
    const sigma = sigmaRef.current;
    if (!sigma) return;
    sigma.setSetting("renderLabels", showNodeLabels);
    sigma.refresh();
  }, [showNodeLabels]);
  useEffect(() => {
    const sigma = sigmaRef.current;
    if (!sigma) return;
    sigma.setSetting("renderEdgeLabels", showEdgeLabels);
    sigma.refresh();
  }, [showEdgeLabels]);

  const pickFill = useCallback(
    // #460/#500: every node (including the pinned seed) draws from the
    // red→blue hop ramp. The seed is the hop-0 origin, so it naturally
    // lands on the warm red end — it's distinguished by colour via the
    // ramp itself, plus its larger size and pinned position (#500).
    (node: { hopDistance: number }) => colorForHop(node.hopDistance, palette),
    [palette],
  );

  // #483 continuous d3-force layout lifecycle — the live simulation
  // handle, the rAF tick loop, and the synchronous settle/step/pause
  // controls — lives in a feature-local controller hook (#495 batch 4).
  // This component is the Sigma/graphology rendering shell that drives it
  // from the reconcile effect, the drag handlers, and the test bridge.
  // `layoutRef` / `layoutPausedRef` / `reheatLayoutRef` are surfaced as
  // refs because the once-mounted sigma effect reads and writes them from
  // event-time closures; the destructured callbacks keep stable
  // identities so that effect's dependency array stays minimal.
  const {
    layoutRef,
    layoutPausedRef,
    reheatLayoutRef,
    beginLayout,
    startLayoutLoop,
    stopLayout,
    settleLayout,
    stepLayout,
    setLayoutPaused,
  } = useIlluminateCanvas({ graphRef, sigmaRef });

  // Stable callback ref so the click listener doesn't have to be rebound
  // every render (would otherwise drop hover state).
  const onNodeClickRef = useRef(onNodeClick);
  useEffect(() => {
    onNodeClickRef.current = onNodeClick;
  }, [onNodeClick]);

  // #461: stable ref to the inspect callback, read at event time inside
  // the once-mounted sigma effect (and by the info-icon click handler)
  // so we never rebind sigma listeners when the parent re-renders.
  const onNodeInspectRef = useRef(onNodeInspect);
  useEffect(() => {
    onNodeInspectRef.current = onNodeInspect;
  }, [onNodeInspect]);

  // #461 hover-bridge plumbing. The pending-hide timer lives in the
  // mount-effect closure (it owns the sigma handlers), but the
  // JSX-rendered icon needs to cancel/re-arm that timer from its own
  // mouse events. These refs publish the closure's cancel/schedule
  // helpers to the render body.
  const cancelInfoIconHideRef = useRef<(() => void) | null>(null);
  const scheduleInfoIconHideRef = useRef<(() => void) | null>(null);

  // #456 imperative handle. Reads from `graphRef`/`sigmaRef` at call
  // time so the closure stays valid across re-renders without any
  // dependency wiring; mount/unmount of sigma is handled by the
  // dedicated effect below.
  useImperativeHandle(
    ref,
    () => ({
      panToNode: (key, options) => {
        const sigma = sigmaRef.current;
        const graph = graphRef.current;
        if (!sigma || !graph || !graph.hasNode(key)) return false;

        // `getNodeDisplayData` returns coordinates in the sigma camera's
        // coordinate frame, which is exactly what `camera.animate`
        // accepts as a target state. The graph-space coordinates from
        // `graph.getNodeAttributes` are NOT directly compatible (they
        // pre-date sigma's normalization pass), so always go through
        // the renderer's display data.
        const display = sigma.getNodeDisplayData(key);
        if (!display) return false;

        const duration = options?.duration ?? 600;
        const ratio = options?.ratio ?? 0.5;
        const highlightMs = options?.highlightMs ?? duration;

        const camera = sigma.getCamera();
        camera.animate({ x: display.x, y: display.y, ratio }, { duration });

        // Cancel any prior highlight pulse — restore its node first so
        // a rapid sequence of chip clicks doesn't leave stranded
        // `highlighted=true` attrs around.
        const prior = highlightTimeoutRef.current;
        if (prior) {
          clearTimeout(prior.handle);
          if (graph.hasNode(prior.nodeKey)) {
            graph.setNodeAttribute(
              prior.nodeKey,
              "highlighted",
              prior.previousHighlighted,
            );
          }
          highlightTimeoutRef.current = null;
        }

        const previousHighlighted =
          graph.getNodeAttribute(key, "highlighted") === true;
        graph.setNodeAttribute(key, "highlighted", true);
        sigma.refresh();

        const handle = setTimeout(() => {
          highlightTimeoutRef.current = null;
          if (!graphRef.current?.hasNode(key)) return;
          graphRef.current.setNodeAttribute(
            key,
            "highlighted",
            previousHighlighted,
          );
          sigmaRef.current?.refresh();
        }, highlightMs);

        highlightTimeoutRef.current = {
          handle,
          nodeKey: key,
          previousHighlighted,
        };
        return true;
      },
    }),
    // The handle reads `sigmaRef`/`graphRef`/`highlightTimeoutRef` at
    // call time, so it has no reactive dependencies; an empty array
    // keeps the imperative handle identity stable for the lifetime of
    // the component.
    [],
  );

  // Cancel any in-flight #456 highlight pulse on unmount so a stale
  // setTimeout doesn't fire against a discarded sigma instance.
  useEffect(
    () => () => {
      const pending = highlightTimeoutRef.current;
      if (pending) {
        clearTimeout(pending.handle);
        highlightTimeoutRef.current = null;
      }
    },
    [],
  );

  // Mount sigma once. The graph and renderer survive across data updates;
  // see the reconcile effect below.
  useEffect(() => {
    if (!containerRef.current) return;
    const graph = new Graph({ multi: false, type: "directed" });
    const renderer = new Sigma(graph, containerRef.current, {
      renderLabels: true,
      labelDensity: 0.5,
      labelGridCellSize: 80,
      // #514: every node carries `forceLabel: true` (set in the reconcile
      // loop) so the key is always drawn; drop the size threshold to 0 so
      // the default-sized discs (size 7 < the old 8px gate) are never
      // culled even on the rare frame before `forceLabel` is applied.
      labelRenderedSizeThreshold: 0,
      defaultEdgeColor: FALLBACK_PALETTE.edge,
      defaultNodeColor: FALLBACK_PALETTE.baseNode,
      // === #485 directed-edge arrowheads ================================
      // The graphology graph is `type: "directed"`, but Sigma's default
      // edge renderer (`EdgeRectangleProgram`) draws every edge as a
      // plain undirected bar, so the canvas hid the direction Lantern's
      // edges actually carry (tail → head). Render the built-in arrow
      // program instead: `EdgeClampedProgram` body + `EdgeArrowHeadProgram`
      // head, with the head clamped to the target node radius so it never
      // disappears under the disc. We register the program explicitly and
      // make it the default type for every edge (no edge sets a per-edge
      // `type`, so this governs the whole graph) rather than relying on
      // the default registry, keeping the wiring self-contained.
      defaultEdgeType: "arrow",
      edgeProgramClasses: { arrow: EdgeArrowProgram },
      // === end #485 =====================================================
      labelColor: { color: FALLBACK_PALETTE.labelText },
      labelSize: LABEL_SIZE,
      labelWeight: LABEL_WEIGHT,
      labelFont: FALLBACK_PALETTE.labelFont,
      // === #514 always-on, readable node-key labels =====================
      // Sigma's built-in `drawDiscNodeLabel` paints the key as bare text
      // with no background, so a key over a same-luminance disc or a busy
      // patch of canvas was hard to read. Swap in a palette-skinned
      // renderer that draws an opaque chip behind every key (same contrast
      // story as the hover chip). Combined with per-node `forceLabel` in
      // the reconcile loop, every vertex key is now always visible and
      // legible. The palette effect re-applies this on a theme flip.
      defaultDrawNodeLabel: makeDrawNodeLabel(FALLBACK_PALETTE),
      // === end #514 =====================================================
      // === #500 on-edge weight labels ===================================
      // Render each edge's accumulated weight as a label along the edge
      // body. The per-edge text is stamped in the reconcile loop
      // (`formatEdgeWeight(edge.weight)`); these settings govern its
      // appearance, and the palette effect re-applies the colour/font on
      // a theme flip, mirroring the node-label settings above.
      renderEdgeLabels: true,
      edgeLabelColor: { color: FALLBACK_PALETTE.labelText },
      edgeLabelSize: EDGE_LABEL_SIZE,
      edgeLabelWeight: EDGE_LABEL_WEIGHT,
      edgeLabelFont: FALLBACK_PALETTE.labelFont,
      // #514: skin the weight label the same way as the node key — an
      // opaque chip behind the text, drawn for every edge (sigma's default
      // skips short edges), so the weight never collides with the edge
      // line or the discs underneath. Re-applied on a theme flip below.
      defaultDrawEdgeLabel: makeDrawEdgeLabel(FALLBACK_PALETTE),
      // === end #500 =====================================================
      // === #484 theme-aware hover label =================================
      // Sigma's default `drawDiscNodeHover` paints a hard-coded near-white
      // box behind the hovered label, then draws the label in
      // `labelColor` (our `--colorNeutralForeground1`) — which is also
      // near-white in dark theme, so the hovered label rendered
      // white-on-white. Swap in a palette-skinned renderer: the chip is
      // filled with `--colorNeutralBackground1` and outlined with a 1px
      // `--colorNeutralStroke1`, guaranteeing it contrasts with the text.
      // The palette effect re-applies this on every theme flip.
      defaultDrawNodeHover: makeDrawNodeHover(FALLBACK_PALETTE),
      // === end #484 =====================================================
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
      // === #459 TTL alpha (composition note) ============================
      // Composition order is hop hue (#460) \u2192 TTL alpha (#459) \u2192 hover
      // dim (#458). TTL fade applies first so the hover dim's solid
      // grey swatch can override it for out-of-focus nodes \u2014 we don't
      // want a dim node to "double-fade" and disappear entirely.
      const attrs = data as {
        color: string;
        expiration?: string | null;
      };
      const nowMs = nowRef.current;
      const expiration = attrs.expiration ?? undefined;
      const fraction = computeTtlFraction(expiration, nowMs);
      // Compute the TTL-tinted, faded color once. Falls through to
      // `attrs.color` when there's no expiration.
      let ttlColor = attrs.color;
      if (fraction !== null) {
        // Warning window: red-tint the base before fading so the
        // surviving alpha communicates urgency, not just decay. The
        // spec asks for a "pulsing red halo" \u2014 we deliver the simpler
        // red tint here and leave the halo to a follow-up (it requires
        // a custom WebGL node program; out of scope for #459).
        const baseColor = isInWarningWindow(expiration, nowMs)
          ? applyWarningTint(attrs.color, warningUrgency(expiration, nowMs))
          : attrs.color;
        ttlColor = applyTtlFade(baseColor, fraction);
      }
      // === end #459 ===================================================
      if (focusSet === null) {
        return fraction !== null ? { ...data, color: ttlColor } : data;
      }
      if (focusSet.has(key)) {
        return fraction !== null ? { ...data, color: ttlColor } : data;
      }
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
      // === #459 TTL alpha (composition note same as nodeReducer) ========
      const attrs = data as { color: string; expiration?: string | null };
      const nowMs = nowRef.current;
      const expiration = attrs.expiration ?? undefined;
      const fraction = computeTtlFraction(expiration, nowMs);
      let ttlColor = attrs.color;
      if (fraction !== null) {
        ttlColor = applyTtlFade(attrs.color, fraction);
      }
      // === end #459 ===================================================
      if (focusSet === null || hoveredNodeId === null) {
        return fraction !== null ? { ...data, color: ttlColor } : data;
      }
      const [src, dst] = graph.extremities(key);
      // Incident edges stay at their TTL-faded saturation so the local
      // subgraph structure is obvious; the fade still communicates
      // remaining lifetime.
      if (src === hoveredNodeId || dst === hoveredNodeId) {
        return fraction !== null
          ? { ...data, color: ttlColor, zIndex: 1 }
          : { ...data, zIndex: 1 };
      }
      return { ...data, color: paletteRef.current.dimEdge, zIndex: 0 };
    });
    // Sigma needs zIndex sorting opted-in to honour the per-element
    // zIndex hints emitted by the reducers above.
    renderer.setSetting("zIndex", true);
    // === end #458 ===========================================================

    // === #461 per-node info icon ===========================================
    // On hover, surface a small info button anchored near the node's
    // viewport position. Activating it opens the read-only detail Drawer
    // (the parent's `onNodeInspect`) WITHOUT expanding — node-body
    // clicks keep their #466 additive-expansion meaning. A short hide
    // delay lets the cursor travel from the node onto the icon without
    // it vanishing; the icon's own `onMouseEnter` cancels the pending
    // hide (the standard hover-bridge pattern).
    let infoIconHideTimer: ReturnType<typeof setTimeout> | null = null;
    const computeIconViewport = (
      key: string,
    ): { x: number; y: number } | null => {
      const display = renderer.getNodeDisplayData(key);
      if (!display) return null;
      // `getNodeDisplayData` is in sigma's framed-graph space; convert
      // to viewport pixels so the DOM overlay lands on the node.
      const vp = renderer.framedGraphToViewport({
        x: display.x,
        y: display.y,
      });
      return { x: vp.x + INFO_ICON_OFFSET_X, y: vp.y - INFO_ICON_OFFSET_Y };
    };
    const showInfoIconFor = (key: string): boolean => {
      if (infoIconHideTimer !== null) {
        clearTimeout(infoIconHideTimer);
        infoIconHideTimer = null;
      }
      const pos = computeIconViewport(key);
      if (!pos) return false;
      setInfoIcon({ key, x: pos.x, y: pos.y });
      return true;
    };
    const scheduleInfoIconHide = () => {
      if (infoIconHideTimer !== null) clearTimeout(infoIconHideTimer);
      infoIconHideTimer = setTimeout(() => {
        infoIconHideTimer = null;
        setInfoIcon(null);
      }, INFO_ICON_HIDE_DELAY_MS);
    };
    cancelInfoIconHideRef.current = () => {
      if (infoIconHideTimer !== null) {
        clearTimeout(infoIconHideTimer);
        infoIconHideTimer = null;
      }
    };
    scheduleInfoIconHideRef.current = scheduleInfoIconHide;
    // === end #461 ===========================================================

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
      // #461: surface the per-node info icon near the hovered node.
      showInfoIconFor(event.node);
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
      // #461: arm the delayed hide so the cursor can bridge onto the
      // icon before it disappears.
      scheduleInfoIconHide();
    };
    renderer.on("enterNode", showNodeHover);
    renderer.on("leaveNode", clearHoverNode);
    renderer.on("enterEdge", showEdgeHover);
    renderer.on("leaveEdge", clearHoverNode);

    // === #455 drag (no pin, per #491) =======================================
    // Sigma's standard drag-and-drop recipe. The dragged node id lives in
    // a closure-local ref (not React state) so a mid-drag rerender can't
    // wipe it. On mouse-down we suspend sigma's default camera-pan via
    // `preventSigmaDefault()` and mark the node highlighted so the user
    // gets a visual lock-on. On `mousemovebody` we project the cursor
    // into graph space and rewrite the node's x/y; sigma re-renders on
    // the next frame. On mouse-up we release the node back to the
    // simulation (NO pin) and reheat the layout so physics re-converges
    // the graph around its new position — the drag never freezes a node.
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
      // #483: if a layout animation is in flight, hold this node at the
      // cursor (temporary fx/fy) so the simulation respects the drag
      // instead of fighting it; finishDrag clears the hold and reheats.
      const draggingForceNode = layoutRef.current?.nodeById.get(node);
      if (draggingForceNode) {
        draggingForceNode.fx = pos.x;
        draggingForceNode.fy = pos.y;
      }
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
      draggedNodeRef.current = null;
      // No pin (#491): release the node back to physics and reheat the
      // layout so it re-converges around the dropped position instead of
      // freezing there. reheatLayout rebuilds the sim from the live
      // graphology positions (including this drag), so no fx/fy survives.
      reheatLayoutRef.current();
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
        /**
         * #988 test bridge: invoke the same callback a Sigma clickNode event
         * would fire, but only for a node currently rendered in the graph.
         */
        clickNode: (key: string) => boolean;
        isNodeFixed: (key: string) => boolean;
        /**
         * #491 test bridge: whether an edge currently exists in
         * graphology. Lets the e2e assert delete-not-hide — a non-result
         * edge is dropped (false), a result edge is present (true).
         */
        hasEdge: (key: string) => boolean;
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
         * #460 test bridge: read the hop distance stamped on a node
         * by the selector. Returns `null` if the node is unknown,
         * `Number.POSITIVE_INFINITY` for unreachable. The e2e suite
         * uses this to assert the multi-source BFS result without
         * having to reverse-engineer it from rendered colours.
         */
        getNodeHopDistance: (key: string) => number | null;
        /**
         * #500 test bridge: read the disc size stamped on a node.
         * Returns `null` if the node is unknown. The e2e suite uses
         * this to assert the seed renders larger than the uniform
         * non-seed size.
         */
        getNodeSize: (key: string) => number | null;
        /**
         * #500 test bridge: read the weight label stamped on an edge.
         * Returns `null` if the edge is unknown. Lets the e2e assert
         * the on-edge weight label without reading WebGL pixels.
         */
        getEdgeLabel: (key: string) => string | null;
        /**
         * #458 test bridge: read whether the hover-focus reducer is
         * currently active (i.e., a node is focused).
         */
        hoveredNode: () => string | null;
        /**
         * #459 test bridge: how many TTL refresh ticks have actually
         * executed. The e2e suite uses this to verify the tick pauses
         * while the tab is hidden \u2014 polling for the count to stay
         * flat is faster than waiting on real wall-clock seconds.
         */
        tickCount: () => number;
        /**
         * #459 test bridge: override the wall clock the TTL reducer
         * reads, then force a refresh so the new value is applied.
         * Lets the e2e test simulate elapsed time without waiting.
         * Pass `null` to release the override and resume reading
         * `Date.now()` on the next tick.
         */
        setNow: (ms: number | null) => void;
        /**
         * #459 test bridge: synchronously execute a TTL refresh tick
         * (bumps `nowRef` if no override is active, increments the
         * tick counter, then refreshes sigma). Lets the e2e
         * deterministically observe the post-tick color without
         * waiting on `setInterval`.
         */
        forceTick: () => void;
        /**
         * #456 test bridge: read the current sigma camera state
         * (`x`/`y`/`ratio`/`angle`). The e2e suite snapshots this
         * before and after a chip click to assert the camera animated
         * toward the clicked origin without relying on pixel diffs.
         */
        cameraState: () => {
          x: number;
          y: number;
          ratio: number;
          angle: number;
        };
        /**
         * #456 test bridge: read the post-reducer display coordinates
         * of a node (the same frame `panToNode` animates the camera
         * to). Returns `null` when the node is unknown. Lets the e2e
         * assert the camera converged on the chip's origin vertex.
         */
        getNodeDisplayPosition: (
          key: string,
        ) => { x: number; y: number } | null;
        /**
         * #456 test bridge: whether a node currently carries the
         * transient highlight `panToNode` applies for ~600 ms. Lets the
         * e2e confirm the pulse fires (and later clears).
         */
        isNodeHighlighted: (key: string) => boolean;
        /**
         * #461 test bridge: open the detail Drawer for a node exactly as
         * the info-icon click would, bypassing the WebGL hover + DOM
         * click headless chromium populates only intermittently. Returns
         * `false` when the key isn't in the live graph.
         */
        inspectNode: (key: string) => boolean;
        /**
         * #461 test bridge: surface the per-node info icon for a node as
         * a real `enterNode` hover would. Returns `false` when the key
         * isn't in the live graph or has no display data yet.
         */
        showInfoIcon: (key: string) => boolean;
        /**
         * #461 test bridge: the key of the node whose info icon is
         * currently visible, or `null` when none is shown.
         */
        infoIconNode: () => string | null;
        /**
         * #485 test bridge: the resolved `defaultEdgeType` sigma
         * setting. Every edge renders with this program (none set a
         * per-edge `type`), so asserting it is `"arrow"` proves the
         * directed arrowheads are wired and have not regressed.
         */
        getDefaultEdgeType: () => string;
        /**
         * #484 test bridge: the resolved hover-label chip colours
         * (`background` ← `--colorNeutralBackground1`, `stroke` ←
         * `--colorNeutralStroke1`, `text` ← `--colorNeutralForeground1`).
         * The custom hover renderer paints the chip with `background` +
         * a 1px `stroke` and the label with `text`, so asserting
         * `background !== text` in the live theme proves the hovered
         * label can never collide with its own box. Reads `paletteRef`
         * so it reflects the current theme after a flip.
         */
        getHoverLabelColors: () => {
          background: string;
          stroke: string;
          text: string;
        };
        /**
         * #483 test bridge: pause or resume the continuous d3-force
         * layout. While paused the animation loop suspends (no ticks)
         * so the e2e can assert exact survivor positions immediately
         * after a click, then step the layout deterministically.
         */
        setLayoutPaused: (paused: boolean) => void;
        /**
         * #483 test bridge: advance the live simulation `ticks` steps
         * synchronously (no rAF), writing positions back and refreshing
         * sigma. Returns the number of ticks executed (short-circuits
         * once the layout has cooled). Lets the e2e observe gradual,
         * bounded motion one controlled frame at a time.
         */
        stepLayout: (ticks: number) => number;
        /**
         * #483 test bridge: run the live simulation to rest
         * synchronously (bounded by `maxTicks`), write back, refresh,
         * and drop it. Returns the tick count. Lets the e2e freeze the
         * final layout before asserting spacing / camera convergence.
         */
        settleLayout: (maxTicks?: number) => number;
        /**
         * #483 test bridge: whether the animated layout loop currently
         * has a frame scheduled. The e2e polls this to wait for the
         * auto-run animation to converge.
         */
        layoutRunning: () => boolean;
        /**
         * #517 test bridge: the live label-visibility settings the two
         * on-canvas toggles drive. Reads `renderer.getSetting(...)` so it
         * reflects the current sigma state, not the mount-time closure.
         */
        getLabelVisibility: () => { vertex: boolean; edge: boolean };
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
      clickNode: (key: string) => {
        if (!graph.hasNode(key)) return false;
        onNodeClickRef.current(key);
        return true;
      },
      isNodeFixed: (key: string) => {
        if (!graph.hasNode(key)) return false;
        return graph.getNodeAttribute(key, "fixed") === true;
      },
      hasEdge: (key: string) => graph.hasEdge(key),
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
        // Keep an in-flight simulation in sync with the drag (#483).
        const forceNode = layoutRef.current?.nodeById.get(key);
        if (forceNode) {
          forceNode.fx = attrs.x + deltaGraphX;
          forceNode.fy = attrs.y + deltaGraphY;
        }
        dragStats.moveBody += 1;
        // 3) mouseup \u2014 release (no pin, #491); finishDrag reheats the layout
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
      getNodeHopDistance: (key: string) => {
        if (!graph.hasNode(key)) return null;
        const attrs = graph.getNodeAttributes(key) as {
          hopDistance?: number;
        };
        return typeof attrs.hopDistance === "number"
          ? attrs.hopDistance
          : Number.POSITIVE_INFINITY;
      },
      getNodeSize: (key: string) => {
        if (!graph.hasNode(key)) return null;
        const size = graph.getNodeAttribute(key, "size");
        return typeof size === "number" ? size : null;
      },
      getEdgeLabel: (key: string) => {
        if (!graph.hasEdge(key)) return null;
        const label = graph.getEdgeAttribute(key, "label");
        return typeof label === "string" ? label : null;
      },
      hoveredNode: () => hoveredNodeId,
      tickCount: () => tickCountRef.current,
      setNow: (ms: number | null) => {
        nowOverrideRef.current = ms;
        // Push the override into `nowRef` immediately so the next
        // forceTick (or the next observable refresh) sees it without
        // waiting for the scheduled interval to fire.
        if (ms !== null) {
          nowRef.current = ms;
        }
      },
      forceTick: () => {
        nowRef.current = nowOverrideRef.current ?? Date.now();
        tickCountRef.current += 1;
        renderer.refresh();
      },
      cameraState: () => {
        const state = renderer.getCamera().getState();
        return {
          x: state.x,
          y: state.y,
          ratio: state.ratio,
          angle: state.angle,
        };
      },
      getNodeDisplayPosition: (key: string) => {
        if (!graph.hasNode(key)) return null;
        const data = renderer.getNodeDisplayData(key);
        return data ? { x: data.x, y: data.y } : null;
      },
      isNodeHighlighted: (key: string) => {
        if (!graph.hasNode(key)) return false;
        return graph.getNodeAttribute(key, "highlighted") === true;
      },
      inspectNode: (key: string) => {
        if (!graph.hasNode(key)) return false;
        onNodeInspectRef.current?.(key);
        return true;
      },
      showInfoIcon: (key: string) => {
        if (!graph.hasNode(key)) return false;
        return showInfoIconFor(key);
      },
      infoIconNode: () => infoIconRef.current?.key ?? null,
      getDefaultEdgeType: () => renderer.getSetting("defaultEdgeType"),
      getHoverLabelColors: () => ({
        background: paletteRef.current.labelBackground,
        stroke: paletteRef.current.labelStroke,
        text: paletteRef.current.labelText,
      }),
      setLayoutPaused: (paused: boolean) => setLayoutPaused(paused),
      stepLayout: (ticks: number) => stepLayout(ticks),
      settleLayout: (maxTicks?: number) => settleLayout(maxTicks),
      layoutRunning: () => layoutRef.current?.raf != null,
      getLabelVisibility: () => ({
        vertex: renderer.getSetting("renderLabels"),
        edge: renderer.getSetting("renderEdgeLabels"),
      }),
    };

    return () => {
      // #483: tear down any in-flight layout animation before sigma dies.
      if (layoutRef.current?.raf != null) {
        cancelAnimationFrame(layoutRef.current.raf);
      }
      layoutRef.current = null;
      layoutPausedRef.current = false;
      renderer.kill();
      graph.clear();
      sigmaRef.current = null;
      graphRef.current = null;
      previousNodeIdsRef.current = new Set();
      previousEdgeIdsRef.current = new Set();
      if (infoIconHideTimer !== null) {
        clearTimeout(infoIconHideTimer);
        infoIconHideTimer = null;
      }
      cancelInfoIconHideRef.current = null;
      scheduleInfoIconHideRef.current = null;
      delete win.__illuminateCanvas;
    };
    // The layout refs/callbacks now come from `useIlluminateCanvas`; they
    // all have stable identities (the refs never change, the callbacks have
    // stable deps), so listing them keeps this a once-on-mount effect while
    // satisfying exhaustive-deps now that they are no longer local `useRef`s.
  }, [
    setLayoutPaused,
    stepLayout,
    settleLayout,
    layoutRef,
    layoutPausedRef,
    reheatLayoutRef,
  ]);

  // Apply palette changes to sigma's global settings + repaint existing
  // node fills. Splitting this out from the reconcile effect lets a
  // theme toggle re-skin the canvas without re-running the layout.
  useEffect(() => {
    const sigma = sigmaRef.current;
    const graph = graphRef.current;
    if (!sigma || !graph) return;
    sigma.setSetting("defaultNodeColor", palette.baseNode);
    sigma.setSetting("defaultEdgeColor", palette.edge);
    sigma.setSetting("labelColor", { color: palette.labelText });
    sigma.setSetting("labelFont", palette.labelFont);
    // #500: keep the on-edge weight labels in sync with the theme.
    sigma.setSetting("edgeLabelColor", { color: palette.labelText });
    sigma.setSetting("edgeLabelFont", palette.labelFont);
    // #484: rebuild the hover renderer against the new palette so the
    // hovered-label chip follows the theme alongside the label text.
    sigma.setSetting("defaultDrawNodeHover", makeDrawNodeHover(palette));
    // #514: rebuild the always-on node-key and edge-weight renderers
    // against the new palette so their chips follow the theme too,
    // mirroring the hover renderer above.
    sigma.setSetting("defaultDrawNodeLabel", makeDrawNodeLabel(palette));
    sigma.setSetting("defaultDrawEdgeLabel", makeDrawEdgeLabel(palette));
    for (const id of graph.nodes()) {
      const attrs = graph.getNodeAttributes(id) as {
        hopDistance?: number;
      };
      // #460: re-derive the hop hue against the new palette so a theme
      // flip recolours every node without re-running the reconcile
      // effect. Missing/invalid `hopDistance` falls through to the
      // unreachable tone via `colorForHop`'s defensive branch.
      graph.setNodeAttribute(
        id,
        "color",
        pickFill({
          hopDistance:
            typeof attrs.hopDistance === "number"
              ? attrs.hopDistance
              : Number.POSITIVE_INFINITY,
        }),
      );
    }
    for (const id of graph.edges()) {
      graph.setEdgeAttribute(id, "color", palette.edge);
    }
    sigma.refresh();
  }, [palette, pickFill]);

  // === #459 TTL refresh tick ============================================
  // Bump `nowRef` once a second and refresh sigma so the TTL fade
  // reducer re-evaluates against the new wall clock. Pauses when the
  // tab is hidden \u2014 there's no point burning a render/sec when the
  // user isn't watching, and per spec acceptance criterion 5 it MUST
  // pause so we don't keep React's event loop spinning on
  // background tabs.
  //
  // The cadence (1 Hz) is deliberately coarse. The TTL alpha fades
  // over 10 minutes (see LIFETIME_BUDGET_MS in ttl-decay.ts), so a
  // per-second refresh changes the alpha byte by at most
  // 255 / 600 \u2248 0.42 \u2014 imperceptible per tick, smooth in
  // aggregate. The warning-window red tint sweeps over 60 s so it
  // remains visually obvious without a higher refresh rate.
  useEffect(() => {
    let intervalId: ReturnType<typeof setInterval> | null = null;
    const sigma = sigmaRef.current;
    const tick = () => {
      nowRef.current = nowOverrideRef.current ?? Date.now();
      tickCountRef.current += 1;
      sigmaRef.current?.refresh();
    };
    const start = () => {
      if (intervalId !== null) return;
      // Capture a baseline tick on (re)start so the test bridge and
      // the user see an immediate fade after a tab switch instead of
      // waiting a full second.
      tick();
      intervalId = setInterval(tick, 1000);
    };
    const stop = () => {
      if (intervalId === null) return;
      clearInterval(intervalId);
      intervalId = null;
    };
    const onVisibilityChange = () => {
      if (document.visibilityState === "hidden") {
        stop();
      } else {
        start();
      }
    };
    // Only start if we have a sigma instance (post-mount) AND the tab
    // is currently visible. The mount effect ordering guarantees
    // `sigmaRef.current` is set by the time this effect runs because
    // both effects depend on the same render pass.
    if (sigma !== null && document.visibilityState !== "hidden") {
      start();
    }
    document.addEventListener("visibilitychange", onVisibilityChange);
    return () => {
      stop();
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, []);
  // === end #459 ==========================================================

  // Reconcile the graph with the latest view model. The canvas renders
  // ONLY the latest expansion result (#491): nodes/edges outside it are
  // DROPPED (not hidden), and a survivor that carries over keeps its exact
  // position so the d3-force layout (#483) eases the new frame in smoothly
  // instead of reshuffling — surviving nodes never snap on click, while
  // everything else is deleted and re-converges from scratch on return.
  useEffect(() => {
    const graph = graphRef.current;
    if (!graph) return;
    const sigma = sigmaRef.current;

    // #491: the rendered set IS the latest expansion result. The
    // membership rule (latest-result-or-full-accumulator fallback) is
    // owned by the pure `selectRenderTargets` selector so the reconcile
    // diff and the hop legend can never disagree about what's on screen.
    const { nodeIds: nextNodeIds, edgeIds: nextEdgeIds } = selectRenderTargets({
      nodes,
      edges,
      latestResultVertexKeys,
      latestResultEdgeIds,
    });

    // Per-reconcile diff used to choose the layout regime below (#483).
    // We compute it BEFORE mutating the graph so the counts reflect the
    // structural delta, not the post-merge state. The add/drop + edge-set
    // arithmetic is pure, so it lives in the use-case layer (#495 batch 3)
    // where it is unit-tested in isolation; #500's edge-only-change rule
    // (an expansion that adds/removes EDGES among already-present nodes
    // still reheats the whole render set) is folded into `edgeSetChanged`.
    const { addedCount, droppedCount, edgeSetChanged } = diffRenderSets(
      previousNodeIdsRef.current,
      nextNodeIds,
      previousEdgeIdsRef.current,
      nextEdgeIds,
    );

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
      // #491: render only the latest result; skip everything else so it is
      // neither merged nor (re)created — the drop loop above already
      // deleted it from graphology.
      if (!nextNodeIds.has(node.id)) continue;
      // #500: the per-expansion seed (origin of the most recent
      // expansion) is the canvas's pinned anchor — render it larger and
      // stamp `fixed` so the layout driver pins it (fx/fy) and the drag
      // bridge reports it fixed. Its colour comes from the hop ramp like
      // every other node: as the hop-0 origin it sits on the warm red end,
      // which is what visually distinguishes it. Every other node renders
      // at a single uniform size.
      const isSeed =
        latestExpansionOrigin != null && node.id === latestExpansionOrigin;
      const size = isSeed ? SEED_NODE_SIZE : DEFAULT_NODE_SIZE;
      const color = pickFill({ hopDistance: node.hopDistance });
      const detail = describeVertex(node);
      // #459: stash the protobuf-JSON expiration on the graphology
      // node so the per-tick reducer can read it without crossing back
      // through the selector. `null` means "no expiration", same
      // semantics as `Vertex.expiration === undefined`.
      const expiration = node.vertex.expiration ?? null;
      if (graph.hasNode(node.id)) {
        graph.mergeNodeAttributes(node.id, {
          label: node.label,
          // #514: force the key label so it is never culled by label
          // density/grid selection — every vertex key is always visible.
          forceLabel: true,
          size,
          color,
          detail,
          isInitialSeed: node.isInitialSeed,
          isExpansionOrigin: node.isExpansionOrigin,
          // #500: mark the per-expansion seed so the layout driver pins
          // it (fx/fy). Size (above) is what visually enlarges it; colour
          // comes from the hop ramp (hop-0 origin = warm red end).
          isSeed,
          fixed: isSeed,
          expiration,
          // #460: stamp hop distance for the legend computation and
          // (post-theme-flip) for the palette repaint effect to look
          // up against the new palette.
          hopDistance: node.hopDistance,
          firstSeenExpansion: node.firstSeenExpansion,
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
          // #514: force the key label (see the merge branch above).
          forceLabel: true,
          x,
          y,
          size,
          color,
          detail,
          isInitialSeed: node.isInitialSeed,
          isExpansionOrigin: node.isExpansionOrigin,
          // #500: see the merge branch above.
          isSeed,
          fixed: isSeed,
          expiration,
          hopDistance: node.hopDistance,
          firstSeenExpansion: node.firstSeenExpansion,
        });
      }
    }
    for (const edge of edges) {
      // #491: render only the latest result's edges, and never create an
      // edge whose endpoints were dropped above (graphology would throw).
      if (!nextEdgeIds.has(edge.id)) continue;
      if (!graph.hasNode(edge.source) || !graph.hasNode(edge.target)) continue;
      // #459: edges have their own expiration; mirror the node treatment.
      const expiration = edge.edge.expiration ?? null;
      if (graph.hasEdge(edge.id)) {
        graph.mergeEdgeAttributes(edge.id, {
          size: 1 + Math.min(4, edge.weight),
          // #500: render the accumulated weight on the edge itself.
          label: formatEdgeWeight(edge.weight),
          // #514: force the weight label so it is always drawn.
          forceLabel: true,
          detail: `weight = ${edge.weight}`,
          expiration,
        });
      } else {
        graph.addEdgeWithKey(edge.id, edge.source, edge.target, {
          size: 1 + Math.min(4, edge.weight),
          color: palette.edge,
          // #500: render the accumulated weight on the edge itself.
          label: formatEdgeWeight(edge.weight),
          // #514: force the weight label (see the merge branch above).
          forceLabel: true,
          detail: `weight = ${edge.weight}`,
          expiration,
        });
      }
    }

    // === #483 continuous layout regime =================================
    // Build the simulation over every rendered node (#491: delete-not-
    // hide means graphology already holds exactly the latest result, so
    // there is nothing to exclude). The per-expansion seed is pinned
    // (#500): its `fixed` attribute fixes fx/fy so the sim relaxes every
    // other node around the anchor instead of letting it drift.
    const forceNodes: ForceNode[] = [];
    for (const id of graph.nodes()) {
      const x = graph.getNodeAttribute(id, "x") as number;
      const y = graph.getNodeAttribute(id, "y") as number;
      const size = graph.getNodeAttribute(id, "size") as number;
      const fn: ForceNode = { id, size, x, y };
      if (graph.getNodeAttribute(id, "fixed") === true) {
        fn.fx = x;
        fn.fy = y;
      }
      forceNodes.push(fn);
    }
    const forceLinks: ForceLink[] = [];
    for (const edge of edges) {
      if (!nextEdgeIds.has(edge.id)) continue;
      if (!graph.hasNode(edge.source) || !graph.hasNode(edge.target)) continue;
      forceLinks.push({
        source: edge.source,
        target: edge.target,
        weight: edge.weight,
      });
    }

    // Cold (or full reseed: no survivors) settles synchronously so the
    // first paint is already a sensible layout. An incremental expansion
    // refreshes ONCE at t=0 (survivors render at their EXACT prior
    // position — no snap) and then eases on rAF. A pure attribute or
    // weight change just refreshes and leaves any running animation
    // alone. The decision is pure (#495 batch 3): `decideLayoutRegime`
    // owns the cold/incremental/static branch; the component maps the
    // chosen regime to the heat constant + the imperative sim calls.
    const regime = decideLayoutRegime({
      nextNodeCount: nextNodeIds.size,
      forceNodeCount: forceNodes.length,
      addedCount,
      droppedCount,
      edgeSetChanged,
    });
    if (regime === "cold") {
      beginLayout(forceNodes, forceLinks, FORCE_ALPHA_COLD);
      settleLayout();
    } else if (regime === "incremental") {
      beginLayout(forceNodes, forceLinks, FORCE_ALPHA);
      // Paint survivors at their exact prior position before the first
      // tick so the click never snaps; the animation then eases on rAF.
      sigma?.refresh();
      startLayoutLoop();
    } else {
      // Nothing structural changed. Drop a now-empty sim (Clear), but
      // otherwise leave a converging animation undisturbed.
      if (forceNodes.length === 0) stopLayout();
      sigma?.refresh();
    }
    // === end #483 ======================================================

    // === #514 re-frame the camera on the latest subgraph ===============
    // After a structural change (click-to-expand or a fresh seed) glide
    // the camera back to its default framing so the new result is centred
    // and zoomed to fit. `animatedReset` returns the camera to its default
    // (x:0.5, y:0.5, ratio:1); with sigma's autoRescale + autoCenter that
    // frames the whole rendered graph. Gated on the structural regimes +
    // a non-empty result so a TTL tick, theme flip, or hover ("static",
    // or an empty Clear) never yanks the viewport. For `cold` the layout
    // has already settled synchronously; for `incremental` autoRescale
    // re-normalises every eased frame, so resetting to the default still
    // frames the whole graph as it relaxes.
    if (
      (regime === "cold" || regime === "incremental") &&
      forceNodes.length > 0
    ) {
      void sigma?.getCamera().animatedReset({ duration: CAMERA_FIT_MS });
    }
    // === end #514 ======================================================

    previousNodeIdsRef.current = nextNodeIds;
    previousEdgeIdsRef.current = nextEdgeIds;
  }, [
    nodes,
    edges,
    latestExpansionOrigin,
    latestResultVertexKeys,
    latestResultEdgeIds,
    palette,
    pickFill,
    beginLayout,
    settleLayout,
    startLayoutLoop,
    stopLayout,
  ]);

  const empty = nodes.length === 0;
  const wrapperClass = useMemo(
    () =>
      [styles.wrapper, fill ? styles.fill : "", isBusy ? styles.busy : ""]
        .filter(Boolean)
        .join(" "),
    [fill, isBusy],
  );

  // #460: hop-distance legend buckets. Recomputed whenever the
  // accumulator changes (i.e. on the same trigger as the reconcile
  // effect), so the counts stay in lockstep with what's rendered.
  // Bucket order matches the palette ramp: origin (0) → 1 → 2 → far
  // (≥3) → unreachable (∞). Buckets with zero count are hidden so the
  // legend stays compact in the common case (most graphs have no
  // unreachables, and many won't have anything past 2 hops).
  const legendBuckets = useMemo(() => {
    // Membership (#491 "render only the latest expansion result") comes
    // from the shared `selectRenderTargets` selector and counting from
    // `selectHopBucketCounts` — the same membership the reconcile uses, so
    // the legend can't desync from the canvas the way it did inline here.
    // This memo only attaches presentation: a theme-derived swatch colour
    // and the human hop label per tier, then hides empty tiers so the
    // legend stays compact.
    const presentation: Record<HopBucketKey, { label: string; color: string }> =
      {
        origin: { label: describeHop(0), color: palette.hop0 },
        "1hop": { label: describeHop(1), color: palette.hop1 },
        "2hop": { label: describeHop(2), color: palette.hop2 },
        far: { label: describeHop(HOP_FAR_THRESHOLD), color: palette.hopFar },
        unreachable: {
          label: describeHop(Number.POSITIVE_INFINITY),
          color: palette.hopUnreachable,
        },
      };
    const { nodeIds } = selectRenderTargets({
      nodes,
      edges,
      latestResultVertexKeys,
      latestResultEdgeIds,
    });
    return selectHopBucketCounts(nodes, nodeIds)
      .filter((b) => b.count > 0)
      .map((b) => ({ ...b, ...presentation[b.key] }));
  }, [nodes, edges, palette, latestResultVertexKeys, latestResultEdgeIds]);

  return (
    <div className={wrapperClass} data-testid="illuminate-canvas">
      <div ref={containerRef} className={styles.canvas} aria-hidden="true" />
      {empty ? (
        <div className={styles.emptyOverlay}>
          <span>No vertices to display.</span>
        </div>
      ) : null}
      {!empty && legendBuckets.length > 0 ? (
        <div
          className={styles.legend}
          role="img"
          aria-label="Hop distance legend"
          data-testid="illuminate-legend"
          ref={(el) => {
            if (!el) return;
            // Pipe each bucket's resolved colour to the swatch via a
            // CSS custom property. Avoids per-element inline styles
            // (lint forbids them) while keeping the colours fully
            // theme-derived.
            for (const b of legendBuckets) {
              el.style.setProperty(`--hop-swatch-${b.key}`, b.color);
            }
          }}
        >
          <div className={styles.legendTitle}>hop distance</div>
          {legendBuckets.map((b) => (
            <div
              key={b.key}
              className={styles.legendRow}
              data-testid={`illuminate-legend-${b.key}`}
            >
              <span
                className={`${styles.legendSwatch} ${styles[`swatch_${b.key}`]}`}
                aria-hidden="true"
              />
              <span>{b.label}</span>
              <span className={styles.legendCount}>{b.count}</span>
            </div>
          ))}
        </div>
      ) : null}
      {!empty ? (
        <div
          className={styles.labelControls}
          data-testid="illuminate-label-controls"
        >
          <button
            type="button"
            className={styles.labelToggle}
            data-testid="illuminate-toggle-node-labels"
            aria-pressed={showNodeLabels}
            title={showNodeLabels ? "Hide vertex labels" : "Show vertex labels"}
            onClick={() => setShowNodeLabels((v) => !v)}
          >
            vertex labels
          </button>
          <button
            type="button"
            className={styles.labelToggle}
            data-testid="illuminate-toggle-edge-labels"
            aria-pressed={showEdgeLabels}
            title={showEdgeLabels ? "Hide edge labels" : "Show edge labels"}
            onClick={() => setShowEdgeLabels((v) => !v)}
          >
            edge labels
          </button>
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
      {infoIcon ? (
        <button
          type="button"
          className={styles.infoIcon}
          data-testid="illuminate-info-icon"
          aria-label={`Inspect ${infoIcon.key}`}
          ref={(el) => {
            if (!el) return;
            el.style.setProperty("--icon-x", `${infoIcon.x}px`);
            el.style.setProperty("--icon-y", `${infoIcon.y}px`);
          }}
          onPointerDown={(e) => {
            e.stopPropagation();
            infoIconPointerDownRef.current = { x: e.clientX, y: e.clientY };
          }}
          onClick={(e) => {
            e.stopPropagation();
            const down = infoIconPointerDownRef.current;
            infoIconPointerDownRef.current = null;
            // A pointer press that travelled too far is a drag, not a
            // click — suppress so #455 drag-to-pin wins. `down === null`
            // means keyboard activation (Enter/Space), which inspects.
            if (down) {
              const moved = Math.hypot(e.clientX - down.x, e.clientY - down.y);
              if (moved > INFO_ICON_DRAG_TOLERANCE_PX) return;
            }
            onNodeInspectRef.current?.(infoIcon.key);
            setInfoIcon(null);
          }}
          onMouseEnter={() => cancelInfoIconHideRef.current?.()}
          onMouseLeave={() => scheduleInfoIconHideRef.current?.()}
        >
          <Info16Regular />
        </button>
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
 *     drop it at their centroid with small jitter — the d3-force
 *     simulation then only has to nudge it into place instead of yanking
 *     the whole layout.
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
    // other (would make the simulation's first tick meaningless).
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
  const valueText = formatVertexValue(node.vertex);
  // #460: append the per-vertex audit trail the hop-distance encoding
  // implies — "hop = N · first seen in expansion #M" — so the user can
  // disambiguate "1 hop from origin A" vs "1 hop from origin B" via
  // the same tooltip the hover focus reducer (#458) surfaces.
  const auditText = `hop = ${describeHop(node.hopDistance)} · first seen in expansion #${node.firstSeenExpansion + 1}`;
  return valueText ? `${valueText}\n${auditText}` : auditText;
}

function formatVertexValue(v: GraphNode["vertex"]): string {
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
