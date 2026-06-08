/**
 * Pure d3-force layout configuration for the Illuminate canvas (#483).
 *
 * Background: the canvas previously ran a synchronous ForceAtlas2 pass on
 * every reconcile (see the now-removed `layout-iterations.ts`). That
 * model had two problems the additive-expansion UX (#466) made painful:
 *
 *   1. A fresh FA2 pass from a perturbed equilibrium redistributes every
 *      node, so surviving vertices *snapped* to new positions on each
 *      click instead of easing.
 *   2. There was no notion of "settle gradually" — the layout was either
 *      fully recomputed or skipped.
 *
 * #483 replaces FA2 with a continuous, velocity-Verlet d3-force
 * simulation that the component drives one `tick()` at a time inside a
 * `requestAnimationFrame` loop. The same engine is used for the cold
 * start (run synchronously to completion) and for incremental
 * expansions (animated), so the whole canvas lives in ONE coordinate
 * system and never rescales between the two paths.
 *
 * This module is deliberately free of React, sigma, and graphology so it
 * stays trivially unit-testable: it owns the force *recipe* and the
 * tuning constants; the component owns the rAF loop, the graphology
 * read/write-back, and the sigma refresh.
 */
import {
  forceCollide,
  forceLink,
  forceManyBody,
  forceSimulation,
  forceX,
  forceY,
  type Simulation,
  type SimulationLinkDatum,
  type SimulationNodeDatum,
} from "d3-force";

/**
 * Target rest length (graph units) for an edge spring. Edges act as soft
 * springs pulling their endpoints toward this separation. Sigma
 * auto-rescales the whole layout to the viewport, so the absolute value
 * only matters *relative* to {@link FORCE_CHARGE_STRENGTH} and
 * {@link collideRadius}. Deliberately short: paired with a strong charge,
 * short stiff edges reel each connected cluster into a tight, cohesive
 * blob while repulsion drives the blobs apart — the classic readable
 * force-layout look of tight edges + wide inter-cluster gaps.
 */
export const FORCE_LINK_DISTANCE = 40;
/**
 * Spring stiffness in [0, 1]. Stiff enough that edges genuinely stay
 * short — so a connected cluster reads as one tight group rather than a
 * loose web — yet still below 1 so survivors ease into place after an
 * expansion instead of snapping (#483 acceptance A; the gradual-motion
 * invariant only requires that a node not reach its settled spot in a
 * single tick, which velocity damping still guarantees at this
 * stiffness).
 */
export const FORCE_LINK_STRENGTH = 0.5;
/**
 * Many-body charge. Negative = mutual repulsion, so nodes push apart and
 * the graph fills open space instead of collapsing onto its edges. Kept
 * strong relative to {@link FORCE_LINK_DISTANCE}: the short, stiff edges
 * hold each cluster together locally while this repulsion spreads the
 * nodes — and drives whole clusters apart — opening the wide
 * inter-cluster gaps that keep cross-cluster edges from overlapping.
 * Because sigma rescales the layout to the viewport, it is this
 * charge:link-distance *ratio* (not the absolute edge length) that sets
 * how far things fan.
 */
export const FORCE_CHARGE_STRENGTH = -360;
/**
 * Extra gap (graph units) added beyond a node's size-derived radius in
 * {@link collideRadius}. Guarantees a visible channel between two nodes
 * even when both are at minimum size.
 */
export const FORCE_COLLIDE_PADDING = 6;
/**
 * How hard the collide force resolves overlaps in [0, 1]. High (but not
 * 1) so nodes separate quickly without the position jitter a hard
 * constraint introduces.
 */
export const FORCE_COLLIDE_STRENGTH = 0.85;
/**
 * Collide relaxation iterations per tick. Two passes tighten the
 * non-overlap guarantee for dense clusters at negligible cost for the
 * graph sizes Illuminate renders.
 */
export const FORCE_COLLIDE_ITERATIONS = 2;
/**
 * Strength of the weak gravity toward the origin (via {@link forceX} /
 * {@link forceY}). Just enough to keep a disconnected component from
 * drifting off-canvas without hard-recentering the graph every tick
 * (which would fight pinned nodes and add jitter).
 */
export const FORCE_CENTER_STRENGTH = 0.03;
/**
 * Initial alpha ("heat") for an *incremental* re-layout after an
 * expansion. Deliberately below 1 so the first few ticks move survivors
 * only a little — the canvas should ease, not snap (#483 acceptance A).
 */
export const FORCE_ALPHA = 0.5;
/**
 * Initial alpha for a *cold* start (empty → populated). Full heat so a
 * from-scratch layout reaches a clean equilibrium; the cold pass runs
 * synchronously so the user never sees the warm-up.
 */
export const FORCE_ALPHA_COLD = 1;
/**
 * Per-tick cooling factor. `alpha *= (1 - decay)` each tick, so the
 * simulation reaches {@link FORCE_ALPHA_MIN} in a bounded number of
 * ticks and then stops — no perpetual motion.
 */
export const FORCE_ALPHA_DECAY = 0.045;
/**
 * Alpha at or below which the simulation is considered settled and the
 * rAF loop halts. Higher than d3's 0.001 default so the animation ends
 * promptly (~70 ticks from {@link FORCE_ALPHA}) instead of crawling.
 */
export const FORCE_ALPHA_MIN = 0.02;
/**
 * Velocity damping in [0, 1]; `1 - velocityDecay` of each node's
 * velocity carries into the next tick. d3's default 0.4 gives a stable,
 * slightly viscous settle that reads as "gradual".
 */
export const FORCE_VELOCITY_DECAY = 0.4;

/**
 * Multiplier mapping a node's sigma `size` to its collide radius. A node
 * rendered at `size` px claims `size * SCALE + padding` graph units of
 * personal space, so larger (more important) nodes get proportionally
 * more breathing room.
 */
export const COLLIDE_SIZE_SCALE = 1.6;

/**
 * Node datum the simulation mutates in place. `x`/`y`/`vx`/`vy`/`fx`/`fy`
 * are inherited from {@link SimulationNodeDatum}; `fx`/`fy` pin a node
 * (used for #455 drag-to-pin). `size` is the sigma node size, consumed
 * by {@link collideRadius}.
 */
export interface ForceNode extends SimulationNodeDatum {
  id: string;
  size: number;
}

/**
 * Link datum. `source`/`target` start as node-id strings and are
 * resolved to {@link ForceNode} references by the link force's `.id()`
 * accessor on initialization.
 */
export interface ForceLink extends SimulationLinkDatum<ForceNode> {
  source: string | ForceNode;
  target: string | ForceNode;
  weight: number;
}

/**
 * Collide radius (graph units) for a node of the given sigma `size`.
 * `forceCollide` keeps node centres at least `radius(a) + radius(b)`
 * apart, which is exactly the "pairwise centre distance ≥ sum of radii"
 * spacing guarantee #483 asks for.
 */
export function collideRadius(size: number): number {
  return size * COLLIDE_SIZE_SCALE + FORCE_COLLIDE_PADDING;
}

export interface CreateForceSimulationOptions {
  /** Initial alpha; defaults to {@link FORCE_ALPHA} (incremental heat). */
  alpha?: number;
}

/**
 * Build a configured-but-stopped d3-force simulation over `nodes` /
 * `links`. The caller drives it with `sim.tick()` (manual loop) — the
 * internal timer is stopped immediately so nothing animates until the
 * component schedules it.
 *
 * Links whose endpoints are not present in `nodes` are dropped rather
 * than throwing: the canvas hides everything outside the latest result
 * (#483 requirement C) and only feeds the *visible* node set here, so a
 * link to a hidden node must be silently excluded.
 *
 * The simulation mutates the passed `nodes` array in place (d3 stamps
 * `x`/`y`/`vx`/`vy`/`index`), so seed each node's `x`/`y` from the live
 * graphology position before calling this to preserve survivor
 * placement.
 */
export function createForceSimulation(
  nodes: ForceNode[],
  links: ForceLink[],
  options: CreateForceSimulationOptions = {},
): Simulation<ForceNode, ForceLink> {
  const ids = new Set(nodes.map((n) => n.id));
  const safeLinks = links.filter((link) => {
    const source =
      typeof link.source === "string" ? link.source : link.source.id;
    const target =
      typeof link.target === "string" ? link.target : link.target.id;
    return ids.has(source) && ids.has(target);
  });

  const simulation = forceSimulation<ForceNode, ForceLink>(nodes)
    .force("charge", forceManyBody<ForceNode>().strength(FORCE_CHARGE_STRENGTH))
    .force(
      "link",
      forceLink<ForceNode, ForceLink>(safeLinks)
        .id((node) => node.id)
        .distance(FORCE_LINK_DISTANCE)
        .strength(FORCE_LINK_STRENGTH),
    )
    .force(
      "collide",
      forceCollide<ForceNode>()
        .radius((node) => collideRadius(node.size))
        .strength(FORCE_COLLIDE_STRENGTH)
        .iterations(FORCE_COLLIDE_ITERATIONS),
    )
    .force("x", forceX<ForceNode>(0).strength(FORCE_CENTER_STRENGTH))
    .force("y", forceY<ForceNode>(0).strength(FORCE_CENTER_STRENGTH))
    .velocityDecay(FORCE_VELOCITY_DECAY)
    .alphaDecay(FORCE_ALPHA_DECAY)
    .alphaMin(FORCE_ALPHA_MIN)
    .alpha(options.alpha ?? FORCE_ALPHA);

  // We tick manually from a rAF loop (or synchronously for cold start);
  // stop d3's built-in timer so nothing runs until we ask.
  simulation.stop();
  return simulation;
}
