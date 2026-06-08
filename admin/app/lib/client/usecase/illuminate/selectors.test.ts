import { describe, expect, it } from "bun:test";
import type { Edge, Vertex } from "~/lib/client/infrastructure/api/illuminate";
import { illuminateReducer } from "./reducer";
import {
  selectCanClear,
  selectExpansionCount,
  selectGraphView,
  selectIsBusy,
} from "./selectors";
import {
  ACCUMULATOR_SOFT_CAP,
  INITIAL_ILLUMINATE_STATE,
  type IlluminateState,
} from "./state";

function v(key: string): Vertex {
  return { key };
}

function e(tail: string, head: string, weight = 1): Edge {
  return { tail, head, weight };
}

function applyExpansion(
  state: IlluminateState,
  opts: {
    expansionId: number;
    origin: string;
    vertices: Vertex[];
    edges: Edge[];
  },
): IlluminateState {
  const requested = illuminateReducer(state, {
    type: "EXPANSION_REQUESTED",
    expansionId: opts.expansionId,
    origin: opts.origin,
    controls: state.controls,
    startedAtMs: 0,
  });
  return illuminateReducer(requested, {
    type: "EXPANSION_RECEIVED",
    expansionId: opts.expansionId,
    origin: opts.origin,
    controls: state.controls,
    startedAtMs: 0,
    vertices: opts.vertices,
    edges: opts.edges,
    receivedAtMs: 100 * opts.expansionId,
  });
}

function stateWithInitialSeed(seed: string): IlluminateState {
  return illuminateReducer(INITIAL_ILLUMINATE_STATE, {
    type: "INITIAL_SEED_CHANGED",
    seed,
  });
}

describe("selectGraphView", () => {
  it("returns empty arrays when no expansion has been received", () => {
    const view = selectGraphView(INITIAL_ILLUMINATE_STATE);
    expect(view.nodes).toEqual([]);
    expect(view.edges).toEqual([]);
    expect(view.latestExpansionOrigin).toBeNull();
    expect(view.expansionOrigins).toEqual([]);
    expect(view.overSoftCap).toBe(false);
  });

  it("marks the initial seed and the expansion origin", () => {
    let state = stateWithInitialSeed("a");
    state = applyExpansion(state, {
      expansionId: 1,
      origin: "a",
      vertices: [v("a"), v("b"), v("c")],
      edges: [e("a", "b", 1), e("a", "c", 3)],
    });
    const view = selectGraphView(state);
    const a = view.nodes.find((n) => n.id === "a");
    const b = view.nodes.find((n) => n.id === "b");
    const c = view.nodes.find((n) => n.id === "c");
    expect(a?.isInitialSeed).toBe(true);
    expect(a?.isExpansionOrigin).toBe(true);
    expect(a?.importance).toBe(1);
    expect(b?.isInitialSeed).toBe(false);
    expect(b?.isExpansionOrigin).toBe(false);
    // Importance is normalised against the heaviest non-origin node.
    expect(c?.importance).toBeGreaterThan(0);
    expect(c?.importance).toBeLessThanOrEqual(1);
  });

  it("tracks every expansion origin in insertion order, latest last", () => {
    let state = stateWithInitialSeed("a");
    state = applyExpansion(state, {
      expansionId: 1,
      origin: "a",
      vertices: [v("a"), v("b")],
      edges: [e("a", "b")],
    });
    state = applyExpansion(state, {
      expansionId: 2,
      origin: "b",
      vertices: [v("b"), v("c")],
      edges: [e("b", "c")],
    });
    const view = selectGraphView(state);
    expect(view.expansionOrigins).toEqual(["a", "b"]);
    expect(view.latestExpansionOrigin).toBe("b");
    // Both 'a' and 'b' are expansion origins; only 'a' is the initial seed.
    expect(view.nodes.find((n) => n.id === "a")?.isExpansionOrigin).toBe(true);
    expect(view.nodes.find((n) => n.id === "b")?.isExpansionOrigin).toBe(true);
    expect(view.nodes.find((n) => n.id === "a")?.isInitialSeed).toBe(true);
    expect(view.nodes.find((n) => n.id === "b")?.isInitialSeed).toBe(false);
  });

  it("attributes firstSeenExpansion to the earliest expansion that brought the vertex in", () => {
    let state = stateWithInitialSeed("a");
    state = applyExpansion(state, {
      expansionId: 1,
      origin: "a",
      vertices: [v("a"), v("b")],
      edges: [e("a", "b")],
    });
    state = applyExpansion(state, {
      expansionId: 2,
      origin: "b",
      vertices: [v("b"), v("c")],
      edges: [e("b", "c")],
    });
    const view = selectGraphView(state);
    expect(view.nodes.find((n) => n.id === "a")?.firstSeenExpansion).toBe(0);
    expect(view.nodes.find((n) => n.id === "b")?.firstSeenExpansion).toBe(0);
    expect(view.nodes.find((n) => n.id === "c")?.firstSeenExpansion).toBe(1);
  });

  it("aggregates edge weights across expansions for importance ranking", () => {
    let state = stateWithInitialSeed("a");
    state = applyExpansion(state, {
      expansionId: 1,
      origin: "a",
      vertices: [v("a"), v("b")],
      edges: [e("a", "b", 2)],
    });
    state = applyExpansion(state, {
      expansionId: 2,
      origin: "b",
      vertices: [v("b"), v("a")],
      edges: [e("b", "a", 5)],
    });
    const view = selectGraphView(state);
    // Two edges in opposite directions; both are kept in the view.
    expect(view.edges).toHaveLength(2);
    const ids = view.edges.map((edge) => edge.id).sort();
    expect(ids).toEqual(["a→b", "b→a"]);
  });

  it("drops edges that reference unknown vertices (defensive filter)", () => {
    // Direct-construct a state where the accumulator has a stale edge
    // pointing at a vanished vertex — the reducer prevents this in
    // practice, but the selector must still be defensive.
    const state: IlluminateState = {
      ...INITIAL_ILLUMINATE_STATE,
      initialSeed: "a",
      accumulator: {
        vertices: new Map([
          ["a", { vertex: v("a"), receivedAtMs: 1, expansionIndexes: [0] }],
        ]),
        edges: new Map([
          [
            "a→b",
            {
              edge: e("a", "b"),
              receivedAtMs: 1,
              expansionIndexes: [0],
            },
          ],
        ]),
      },
      expansions: [
        {
          id: 1,
          origin: "a",
          controls: INITIAL_ILLUMINATE_STATE.controls,
          startedAtMs: 0,
          vertexKeys: ["a"],
          edgeIds: ["a→b"],
        },
      ],
    };
    const view = selectGraphView(state);
    expect(view.edges).toEqual([]);
  });

  it("flips overSoftCap when the accumulator passes ACCUMULATOR_SOFT_CAP", () => {
    let state = stateWithInitialSeed("a");
    const bigVertices: Vertex[] = [];
    for (let i = 0; i < ACCUMULATOR_SOFT_CAP + 1; i += 1) {
      bigVertices.push(v(`k:${i}`));
    }
    state = applyExpansion(state, {
      expansionId: 1,
      origin: "a",
      vertices: bigVertices,
      edges: [],
    });
    const view = selectGraphView(state);
    expect(view.overSoftCap).toBe(true);
  });

  it("does NOT flip overSoftCap at exactly the soft cap", () => {
    let state = stateWithInitialSeed("a");
    const bigVertices: Vertex[] = [];
    for (let i = 0; i < ACCUMULATOR_SOFT_CAP; i += 1) {
      bigVertices.push(v(`k:${i}`));
    }
    state = applyExpansion(state, {
      expansionId: 1,
      origin: "a",
      vertices: bigVertices,
      edges: [],
    });
    const view = selectGraphView(state);
    expect(view.overSoftCap).toBe(false);
  });
});

describe("selectCanClear", () => {
  it("is false on a brand-new state", () => {
    expect(selectCanClear(INITIAL_ILLUMINATE_STATE)).toBe(false);
  });

  it("is true once an expansion has populated the accumulator", () => {
    let state = stateWithInitialSeed("a");
    state = applyExpansion(state, {
      expansionId: 1,
      origin: "a",
      vertices: [v("a")],
      edges: [],
    });
    expect(selectCanClear(state)).toBe(true);
  });
});

describe("selectExpansionCount", () => {
  it("counts the expansions stored in state", () => {
    let state = stateWithInitialSeed("a");
    expect(selectExpansionCount(state)).toBe(0);
    state = applyExpansion(state, {
      expansionId: 1,
      origin: "a",
      vertices: [v("a")],
      edges: [],
    });
    expect(selectExpansionCount(state)).toBe(1);
  });
});

describe("selectIsBusy", () => {
  it("is false on a brand-new state", () => {
    expect(selectIsBusy(INITIAL_ILLUMINATE_STATE)).toBe(false);
  });

  it("is true while loading or while any pending expansion remains", () => {
    expect(
      selectIsBusy({ ...INITIAL_ILLUMINATE_STATE, status: "loading" }),
    ).toBe(true);
    expect(selectIsBusy({ ...INITIAL_ILLUMINATE_STATE, pendingCount: 1 })).toBe(
      true,
    );
  });
});
