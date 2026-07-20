/**
 * Feature-local controller hook for the Illuminate canvas's continuous
 * d3-force layout (#495 batch 4, extracted from `IlluminateCanvas.tsx`).
 *
 * `IlluminateCanvas.tsx` is the Sigma/graphology rendering shell; this
 * hook owns the *lifecycle-heavy* half it used to inline — the live
 * simulation handle, the `requestAnimationFrame` tick loop, and the
 * synchronous settle/step controls (#483). It is a controlled
 * "stateful flow" compromise per the architecture skill: the simulation
 * is an infrastructure handle (like a timer or audio context), so it is
 * coordinated here rather than smeared across the component's effects.
 *
 * The hook is intentionally pure of view concerns: it never reads props,
 * renders, or touches the DOM beyond the `graphRef`/`sigmaRef` handles
 * the shell passes in. Every returned callback has a stable identity for
 * the component's lifetime (the refs it closes over never change), so the
 * shell's once-mounted sigma effect can list `setLayoutPaused` /
 * `stepLayout` / `settleLayout` in its dependency array without
 * re-binding sigma listeners.
 *
 * Behaviour is identical to the inline driver it replaces — this is a
 * relocation, not a change (#495).
 */
import { useCallback, useEffect, useRef, type RefObject } from "react";
import type Graph from "graphology";
import type Sigma from "sigma";
import type { Simulation } from "d3-force";
import {
  FORCE_ALPHA,
  FORCE_ALPHA_MIN,
  createForceSimulation,
  tickSimulationFrame,
  type ForceLink,
  type ForceNode,
} from "./force-layout";

/**
 * The live d3-force simulation driving the continuous layout (#483).
 * `forceNodes` is the array d3 mutates in place; `nodeById` indexes it so
 * the shell's drag handlers can pin a node mid-animation; `raf` is the
 * in-flight `requestAnimationFrame` handle (or `null` when settled or
 * paused).
 */
export interface LayoutState {
  simulation: Simulation<ForceNode, ForceLink>;
  forceNodes: ForceNode[];
  nodeById: Map<string, ForceNode>;
  raf: number | null;
  frameBudgetMs: number;
}

/**
 * The control surface the rendering shell drives. `layoutRef` /
 * `layoutPausedRef` / `reheatLayoutRef` are exposed as refs (not values)
 * because the shell's once-mounted sigma effect reads and writes them
 * from event-time closures (drag handlers, test bridge, teardown) without
 * wanting to re-run.
 */
export interface IlluminateCanvasController {
  /** The live simulation handle, or `null` when nothing is animating. */
  layoutRef: RefObject<LayoutState | null>;
  /** When `true`, the rAF tick loop is suspended (#483 test bridge). */
  layoutPausedRef: RefObject<boolean>;
  /**
   * Ref mirror of {@link IlluminateCanvasController.reheatLayout} so the
   * shell's once-mounted drag handlers can reheat after a drop without
   * the sigma setup effect needing to re-bind.
   */
  reheatLayoutRef: RefObject<() => void>;
  /**
   * Replace the current simulation with a fresh one over the supplied
   * visible nodes/links, seeded from their current positions. Does NOT
   * start the loop — the caller chooses one-tick incremental animation or
   * bounded cold-start batches.
   */
  beginLayout: (
    forceNodes: ForceNode[],
    forceLinks: ForceLink[],
    alpha: number,
  ) => void;
  /**
   * Schedule the animation loop unless it is already running or paused.
   * Passing a budget batches cold-start ticks; omitting it preserves the
   * current simulation's mode.
   */
  startLayoutLoop: (frameBudgetMs?: number) => void;
  /** Cancel any in-flight animation frame and drop the simulation. */
  stopLayout: () => void;
  /** Rebuild + reheat the simulation over the live graph (drag release). */
  reheatLayout: () => void;
  /**
   * Run the simulation to rest synchronously (bounded by `maxTicks`),
   * write back, refresh, and drop it. Returns the tick count.
   */
  settleLayout: (maxTicks?: number) => number;
  /**
   * Advance the simulation `ticks` steps synchronously (no rAF) and write
   * the result back. Returns the number of ticks executed.
   */
  stepLayout: (ticks: number) => number;
  /** Pause/resume the animated layout (#483 test bridge). */
  setLayoutPaused: (paused: boolean) => void;
}

/**
 * Owns the #483 continuous d3-force layout for the Illuminate canvas: the
 * simulation handle, the rAF tick loop, and the settle/step/pause
 * controls. The rendering shell supplies the graphology + sigma handles
 * and drives the returned controls from its reconcile effect, drag
 * handlers, and test bridge.
 */
export function useIlluminateCanvas({
  graphRef,
  sigmaRef,
}: {
  graphRef: RefObject<Graph | null>;
  sigmaRef: RefObject<Sigma | null>;
}): IlluminateCanvasController {
  const layoutRef = useRef<LayoutState | null>(null);
  const layoutPausedRef = useRef<boolean>(false);

  // The simulation lives in `layoutRef`; these helpers own ticking it,
  // writing positions back into graphology, and the rAF scheduling. They
  // touch sigma/graph/layout exclusively through refs so their identities
  // stay stable for the component's lifetime (stable deps), letting both
  // the shell's mount-effect test bridge and its reconcile effect call
  // them without re-wiring sigma listeners.

  /**
   * Copy the simulation's positions into graphology. The only pinned
   * node is the per-expansion seed (#500), whose `fx`/`fy` hold its
   * coordinates constant — writing those back is a harmless no-op since
   * d3-force keeps `x === fx`. Every other node is unpinned (drag-to-pin
   * was removed in #491), so this stays a one-way sim → graphology flow
   * with the simulation authoritative for position.
   */
  const writeBackLayoutPositions = useCallback(() => {
    const layout = layoutRef.current;
    const graph = graphRef.current;
    if (!layout || !graph) return;
    for (const fn of layout.forceNodes) {
      if (!graph.hasNode(fn.id)) continue;
      if (typeof fn.x === "number") graph.setNodeAttribute(fn.id, "x", fn.x);
      if (typeof fn.y === "number") graph.setNodeAttribute(fn.id, "y", fn.y);
    }
  }, [graphRef]);

  /** Cancel any in-flight animation frame and drop the simulation. */
  const stopLayout = useCallback(() => {
    const layout = layoutRef.current;
    if (layout?.raf != null) cancelAnimationFrame(layout.raf);
    layoutRef.current = null;
  }, []);

  /**
   * One animation frame: tick the sim, write back, refresh sigma, then
   * either reschedule, suspend (paused), or stop (settled).
   */
  const runLayoutFrame = useCallback(() => {
    const layout = layoutRef.current;
    if (!layout) return;
    if (layoutPausedRef.current) {
      // Suspend: keep the sim so a later resume/step can continue it.
      layout.raf = null;
      return;
    }
    tickSimulationFrame(layout.simulation, layout.frameBudgetMs);
    writeBackLayoutPositions();
    sigmaRef.current?.refresh();
    if (layout.simulation.alpha() <= FORCE_ALPHA_MIN) {
      stopLayout();
      return;
    }
    layout.raf = requestAnimationFrame(runLayoutFrame);
  }, [writeBackLayoutPositions, stopLayout, sigmaRef]);

  /** Schedule the animation loop unless it is already running or paused. */
  const startLayoutLoop = useCallback(
    (frameBudgetMs?: number) => {
      const layout = layoutRef.current;
      if (!layout) return;
      if (frameBudgetMs !== undefined) {
        layout.frameBudgetMs = Math.max(0, frameBudgetMs);
      }
      if (layout.raf != null || layoutPausedRef.current) return;
      layout.raf = requestAnimationFrame(runLayoutFrame);
    },
    [runLayoutFrame],
  );

  /**
   * Replace the current simulation with a fresh one over the supplied
   * visible nodes/links, seeded from their current positions. Does NOT
   * start the loop — the caller chooses one-tick incremental animation or
   * bounded cold-start batches.
   */
  const beginLayout = useCallback(
    (forceNodes: ForceNode[], forceLinks: ForceLink[], alpha: number) => {
      const previous = layoutRef.current;
      if (previous?.raf != null) cancelAnimationFrame(previous.raf);
      const simulation = createForceSimulation(forceNodes, forceLinks, {
        alpha,
      });
      const nodeById = new Map(forceNodes.map((n) => [n.id, n] as const));
      layoutRef.current = {
        simulation,
        forceNodes,
        nodeById,
        raf: null,
        frameBudgetMs: 0,
      };
    },
    [],
  );

  /**
   * Rebuild the simulation over the currently-rendered nodes/edges and
   * start animating so the layout re-converges. Used after a drag
   * release (#491): the dropped node is NOT pinned, so reheating lets
   * physics settle the whole graph around its new position instead of
   * freezing it where the cursor left it. The one exception is the
   * per-expansion seed (#500): its `fixed` attribute re-pins it (fx/fy)
   * so the reheat relaxes everything else around the anchor.
   */
  const reheatLayout = useCallback(() => {
    const graph = graphRef.current;
    if (!graph) return;
    const forceNodes: ForceNode[] = [];
    for (const id of graph.nodes()) {
      const x = graph.getNodeAttribute(id, "x") as number;
      const y = graph.getNodeAttribute(id, "y") as number;
      const fn: ForceNode = {
        id,
        size: graph.getNodeAttribute(id, "size") as number,
        x,
        y,
      };
      // #500: keep the seed pinned across a reheat.
      if (graph.getNodeAttribute(id, "fixed") === true) {
        fn.fx = x;
        fn.fy = y;
      }
      forceNodes.push(fn);
    }
    if (forceNodes.length === 0) {
      stopLayout();
      return;
    }
    const forceLinks: ForceLink[] = graph.edges().map((id) => {
      const [source, target] = graph.extremities(id);
      return { source, target, weight: 1 };
    });
    beginLayout(forceNodes, forceLinks, FORCE_ALPHA);
    startLayoutLoop();
  }, [graphRef, beginLayout, startLayoutLoop, stopLayout]);
  // Ref mirror so the shell's mount-effect drag handlers can reheat
  // without the sigma setup effect (which runs once) needing to re-bind.
  const reheatLayoutRef = useRef(reheatLayout);
  useEffect(() => {
    reheatLayoutRef.current = reheatLayout;
  }, [reheatLayout]);

  /**
   * Advance the simulation `ticks` steps synchronously (no rAF) and write
   * the result back. Used by the #483 e2e to observe gradual motion one
   * controlled frame at a time. Returns the number of ticks executed.
   */
  const stepLayout = useCallback(
    (ticks: number): number => {
      const layout = layoutRef.current;
      if (!layout) return 0;
      let done = 0;
      for (let i = 0; i < ticks; i += 1) {
        layout.simulation.tick();
        done += 1;
        if (layout.simulation.alpha() <= FORCE_ALPHA_MIN) break;
      }
      writeBackLayoutPositions();
      sigmaRef.current?.refresh();
      return done;
    },
    [writeBackLayoutPositions, sigmaRef],
  );

  /**
   * Run the simulation to rest synchronously (bounded by `maxTicks`),
   * write back, refresh, and drop it. Kept for the deterministic test bridge
   * that freezes positions before spacing and camera assertions.
   */
  const settleLayout = useCallback(
    (maxTicks = 500): number => {
      const layout = layoutRef.current;
      if (!layout) return 0;
      let done = 0;
      while (layout.simulation.alpha() > FORCE_ALPHA_MIN && done < maxTicks) {
        layout.simulation.tick();
        done += 1;
      }
      writeBackLayoutPositions();
      sigmaRef.current?.refresh();
      stopLayout();
      return done;
    },
    [writeBackLayoutPositions, stopLayout, sigmaRef],
  );

  /** Pause/resume the animated layout (#483 test bridge). */
  const setLayoutPaused = useCallback(
    (paused: boolean) => {
      layoutPausedRef.current = paused;
      if (!paused) startLayoutLoop();
    },
    [startLayoutLoop],
  );

  return {
    layoutRef,
    layoutPausedRef,
    reheatLayoutRef,
    beginLayout,
    startLayoutLoop,
    stopLayout,
    reheatLayout,
    settleLayout,
    stepLayout,
    setLayoutPaused,
  };
}
