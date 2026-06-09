import { describe, expect, test } from "bun:test";
import {
  COLLIDE_SIZE_SCALE,
  FORCE_ALPHA,
  FORCE_ALPHA_MIN,
  FORCE_COLLIDE_PADDING,
  collideRadius,
  createForceSimulation,
  type ForceLink,
  type ForceNode,
} from "./force-layout";

function distance(a: ForceNode, b: ForceNode): number {
  const dx = (a.x ?? 0) - (b.x ?? 0);
  const dy = (a.y ?? 0) - (b.y ?? 0);
  return Math.hypot(dx, dy);
}

/** Tick until settled (alpha decayed below the stop threshold) or capped. */
function settle(
  simulation: ReturnType<typeof createForceSimulation>,
  maxTicks = 600,
): number {
  let ticks = 0;
  while (simulation.alpha() > FORCE_ALPHA_MIN && ticks < maxTicks) {
    simulation.tick();
    ticks += 1;
  }
  return ticks;
}

describe("collideRadius", () => {
  test("scales node size and adds the padding gap", () => {
    expect(collideRadius(0)).toBe(FORCE_COLLIDE_PADDING);
    expect(collideRadius(4)).toBeCloseTo(
      4 * COLLIDE_SIZE_SCALE + FORCE_COLLIDE_PADDING,
    );
    expect(collideRadius(10)).toBeCloseTo(
      10 * COLLIDE_SIZE_SCALE + FORCE_COLLIDE_PADDING,
    );
  });

  test("is monotonic in size", () => {
    expect(collideRadius(8)).toBeGreaterThan(collideRadius(4));
  });
});

describe("createForceSimulation", () => {
  test("returns a stopped simulation seeded at the requested alpha", () => {
    const nodes: ForceNode[] = [{ id: "a", size: 4, x: 0, y: 0 }];
    const simulation = createForceSimulation(nodes, [], { alpha: FORCE_ALPHA });
    // Stopped: alpha has not been advanced by an internal timer.
    expect(simulation.alpha()).toBe(FORCE_ALPHA);
  });

  test("defaults to the incremental alpha when none is given", () => {
    const nodes: ForceNode[] = [{ id: "a", size: 4, x: 0, y: 0 }];
    const simulation = createForceSimulation(nodes, []);
    expect(simulation.alpha()).toBe(FORCE_ALPHA);
  });

  test("ticking cools the simulation toward the stop threshold", () => {
    const nodes: ForceNode[] = [
      { id: "a", size: 4, x: 0, y: 0 },
      { id: "b", size: 4, x: 1, y: 0 },
    ];
    const simulation = createForceSimulation(nodes, []);
    const before = simulation.alpha();
    simulation.tick();
    expect(simulation.alpha()).toBeLessThan(before);

    const ticks = settle(simulation);
    expect(simulation.alpha()).toBeLessThanOrEqual(FORCE_ALPHA_MIN);
    expect(ticks).toBeLessThan(600);
  });

  test("separates overlapping nodes to at least their summed collide radii", () => {
    const nodes: ForceNode[] = [
      { id: "a", size: 4, x: 0, y: 0 },
      { id: "b", size: 4, x: 1, y: 0 },
    ];
    const simulation = createForceSimulation(nodes, []);
    settle(simulation);

    const minSeparation = collideRadius(4) + collideRadius(4);
    // collide is iterative (strength < 1); allow a small tolerance but the
    // nodes must be clearly non-overlapping, not stacked.
    expect(distance(nodes[0], nodes[1])).toBeGreaterThan(minSeparation * 0.9);
  });

  test("edge spring pulls far-apart connected nodes together without overlap", () => {
    const nodes: ForceNode[] = [
      { id: "a", size: 4, x: -250, y: 0 },
      { id: "b", size: 4, x: 250, y: 0 },
    ];
    const links: ForceLink[] = [{ source: "a", target: "b", weight: 1 }];
    const simulation = createForceSimulation(nodes, links);
    const initial = distance(nodes[0], nodes[1]);
    settle(simulation);
    const final = distance(nodes[0], nodes[1]);

    // The spring reels them in dramatically from 500 units apart...
    expect(final).toBeLessThan(initial * 0.6);
    // ...but collide still keeps them from overlapping.
    expect(final).toBeGreaterThan(collideRadius(4) + collideRadius(4));
  });

  test("keeps pinned nodes exactly at their fx/fy", () => {
    const nodes: ForceNode[] = [
      { id: "pinned", size: 4, x: 100, y: 50, fx: 100, fy: 50 },
      { id: "free", size: 4, x: 105, y: 50 },
    ];
    const simulation = createForceSimulation(nodes, []);
    settle(simulation);

    const pinned = nodes.find((n) => n.id === "pinned")!;
    expect(pinned.x).toBe(100);
    expect(pinned.y).toBe(50);
    // The free node is repelled away from the pinned one.
    const free = nodes.find((n) => n.id === "free")!;
    expect(distance(pinned, free)).toBeGreaterThan(collideRadius(4));
  });

  test("drops links whose endpoints are not in the visible node set", () => {
    const nodes: ForceNode[] = [
      { id: "a", size: 4, x: 0, y: 0 },
      { id: "b", size: 4, x: 10, y: 0 },
    ];
    const links: ForceLink[] = [
      { source: "a", target: "b", weight: 1 },
      { source: "a", target: "hidden", weight: 1 },
      { source: "ghost", target: "b", weight: 1 },
    ];
    // Must not throw on the dangling links...
    const simulation = createForceSimulation(nodes, links);
    const linkForce = simulation.force("link") as unknown as {
      links(): unknown[];
    };
    // ...and only the fully-resolvable link survives.
    expect(linkForce.links()).toHaveLength(1);
  });
});
