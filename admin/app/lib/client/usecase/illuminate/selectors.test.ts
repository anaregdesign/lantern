import { describe, expect, it } from "bun:test";
import type { Edge, Vertex } from "~/lib/client/infrastructure/api/illuminate";
import { illuminateReducer } from "./reducer";
import {
  filterInboundEdges,
  selectCanClear,
  selectExpansionChips,
  selectExpansionCount,
  selectGraphView,
  selectInspectedDetail,
  selectIsBusy,
} from "./selectors";
import {
  ACCUMULATOR_SOFT_CAP,
  INITIAL_ILLUMINATE_STATE,
  type IlluminateState,
  edgeIdOf,
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
    expect(view.latestResultVertexKeys.size).toBe(0);
    expect(view.latestResultEdgeIds.size).toBe(0);
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

  it("exposes only the latest expansion's membership as the result sets (#483)", () => {
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

    // The accumulator still holds 'a' (a survivor), but the latest result
    // is only the b→c expansion — 'a' must NOT be in the result sets.
    expect([...view.latestResultVertexKeys].sort()).toEqual(["b", "c"]);
    expect(view.latestResultVertexKeys.has("a")).toBe(false);
    expect([...view.latestResultEdgeIds]).toEqual([edgeIdOf("b", "c")]);
    expect(view.latestResultEdgeIds.has(edgeIdOf("a", "b"))).toBe(false);
    // The accumulator (nodes/edges) still carries the survivor 'a'.
    expect(view.nodes.find((n) => n.id === "a")).toBeDefined();
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

  // ── #459 TTL decay: selector-level filtering ──────────────────────
  // The reducer keeps everything the server sends; the selector is the
  // single place that drops past-expiry data so the canvas never sees
  // tombstones. Mid-frame fading lives in the canvas reducer chain.

  const T_NOW = Date.parse("2026-06-09T12:00:00.000Z");
  const isoAt = (deltaMs: number): string =>
    new Date(T_NOW + deltaMs).toISOString();
  const vWithExpiration = (key: string, expiration: string): Vertex => ({
    key,
    expiration,
  });
  const eWithExpiration = (
    tail: string,
    head: string,
    expiration: string,
    weight = 1,
  ): Edge => ({ tail, head, weight, expiration });

  it("treats a vertex with no expiration as ∞ (never filtered)", () => {
    let state = stateWithInitialSeed("a");
    state = applyExpansion(state, {
      expansionId: 1,
      origin: "a",
      vertices: [v("a"), v("b")],
      edges: [e("a", "b")],
    });
    const view = selectGraphView(state, T_NOW);
    expect(view.nodes.map((n) => n.id).sort()).toEqual(["a", "b"]);
    expect(view.edges).toHaveLength(1);
  });

  it("drops a vertex whose expiration is at or past nowMs", () => {
    let state = stateWithInitialSeed("a");
    state = applyExpansion(state, {
      expansionId: 1,
      origin: "a",
      vertices: [v("a"), vWithExpiration("b", isoAt(-1_000))],
      edges: [e("a", "b")],
    });
    const view = selectGraphView(state, T_NOW);
    expect(view.nodes.map((n) => n.id)).toEqual(["a"]);
    // Cascading filter: edge incident on dropped vertex is also gone.
    expect(view.edges).toEqual([]);
  });

  it("keeps a vertex whose expiration is in the near future (cliff/warning state)", () => {
    let state = stateWithInitialSeed("a");
    state = applyExpansion(state, {
      expansionId: 1,
      origin: "a",
      vertices: [v("a"), vWithExpiration("b", isoAt(500))],
      edges: [e("a", "b")],
    });
    const view = selectGraphView(state, T_NOW);
    // Inside the warning window but still present — the canvas
    // reducer is responsible for the visual cliff treatment.
    expect(view.nodes.map((n) => n.id).sort()).toEqual(["a", "b"]);
    expect(view.edges).toHaveLength(1);
  });

  it("drops an edge whose own expiration has passed even when both endpoints survive", () => {
    let state = stateWithInitialSeed("a");
    state = applyExpansion(state, {
      expansionId: 1,
      origin: "a",
      vertices: [v("a"), v("b")],
      edges: [eWithExpiration("a", "b", isoAt(-1_000))],
    });
    const view = selectGraphView(state, T_NOW);
    expect(view.nodes.map((n) => n.id).sort()).toEqual(["a", "b"]);
    expect(view.edges).toEqual([]);
  });

  it("does not let an expired edge inflate node importance", () => {
    let state = stateWithInitialSeed("a");
    // Two edges: live a→b with weight 1, expired a→c with weight 100.
    // Without filtering, 'c' would dominate importance ranking.
    state = applyExpansion(state, {
      expansionId: 1,
      origin: "a",
      vertices: [v("a"), v("b"), v("c")],
      edges: [e("a", "b", 1), eWithExpiration("a", "c", isoAt(-1_000), 100)],
    });
    const view = selectGraphView(state, T_NOW);
    const b = view.nodes.find((n) => n.id === "b");
    const c = view.nodes.find((n) => n.id === "c");
    // 'c' has no surviving edges contributing to its weight, so it
    // falls to the default importance for non-seed/non-origin vertices.
    expect(b?.importance).toBeGreaterThan(c?.importance ?? Infinity);
  });

  // ── #460 multi-source BFS hop distances ───────────────────────────
  // The selector annotates each node with its shortest hop to ANY
  // expansion origin, computed over the undirected projection of the
  // live (post-TTL) edge set. The canvas uses this for hop-distance
  // colouring (#460); the test bridge reads it through the rendered
  // node colour in the e2e suite.

  it("attaches hopDistance 0 to expansion origins and Infinity to an empty graph", () => {
    const view = selectGraphView(INITIAL_ILLUMINATE_STATE);
    expect(view.nodes).toEqual([]);
    expect(view.expansionOrigins).toEqual([]);
  });

  it("assigns hopDistance 0 to the only origin and BFS hops to its reachable neighbours", () => {
    let state = stateWithInitialSeed("a");
    state = applyExpansion(state, {
      expansionId: 1,
      origin: "a",
      vertices: [v("a"), v("b"), v("c"), v("d")],
      edges: [e("a", "b"), e("b", "c"), e("c", "d")],
    });
    const view = selectGraphView(state);
    const byId = new Map(view.nodes.map((n) => [n.id, n.hopDistance]));
    expect(byId.get("a")).toBe(0);
    expect(byId.get("b")).toBe(1);
    expect(byId.get("c")).toBe(2);
    expect(byId.get("d")).toBe(3);
  });

  it("treats edges as undirected for hop distance (matches Illuminate's symmetric step semantics)", () => {
    let state = stateWithInitialSeed("a");
    // All edges point AWAY from 'a' — a strictly directed BFS would
    // never reach 'a' from 'c'. Hop distance must be 0 at 'a' and 1
    // at 'b', 2 at 'c' under the undirected projection.
    state = applyExpansion(state, {
      expansionId: 1,
      origin: "a",
      vertices: [v("a"), v("b"), v("c")],
      edges: [e("a", "b"), e("b", "c")],
    });
    // Now run a second expansion from 'c' so 'c' itself becomes hop 0,
    // and assert the previously-1 vertex 'b' becomes hop 1 to whichever
    // origin is closer (still 1: 'a' is hop 0 from 'a', 'b' is hop 1
    // from BOTH origins; 'c' is hop 0 from 'c').
    state = applyExpansion(state, {
      expansionId: 2,
      origin: "c",
      vertices: [v("c")],
      edges: [],
    });
    const view = selectGraphView(state);
    const byId = new Map(view.nodes.map((n) => [n.id, n.hopDistance]));
    expect(byId.get("a")).toBe(0);
    expect(byId.get("b")).toBe(1);
    expect(byId.get("c")).toBe(0);
  });

  it("picks the minimum hop across multiple origins (multi-source BFS)", () => {
    let state = stateWithInitialSeed("a");
    // Topology:  a — b — c — d — e — f
    // Origins: 'a' and 'f'. Expected hops: a=0, b=1, c=2, d=2, e=1, f=0.
    state = applyExpansion(state, {
      expansionId: 1,
      origin: "a",
      vertices: [v("a"), v("b"), v("c"), v("d"), v("e"), v("f")],
      edges: [e("a", "b"), e("b", "c"), e("c", "d"), e("d", "e"), e("e", "f")],
    });
    state = applyExpansion(state, {
      expansionId: 2,
      origin: "f",
      vertices: [v("f")],
      edges: [],
    });
    const view = selectGraphView(state);
    const byId = new Map(view.nodes.map((n) => [n.id, n.hopDistance]));
    expect(byId.get("a")).toBe(0);
    expect(byId.get("b")).toBe(1);
    expect(byId.get("c")).toBe(2);
    expect(byId.get("d")).toBe(2);
    expect(byId.get("e")).toBe(1);
    expect(byId.get("f")).toBe(0);
  });

  it("assigns Infinity to vertices disconnected from every origin", () => {
    // Two components: {a, b} reachable from 'a'; {x, y} an orphan
    // subgraph that was somehow merged in (defensive — shouldn't
    // happen with current server behaviour but the selector must not
    // throw).
    const state: IlluminateState = {
      ...INITIAL_ILLUMINATE_STATE,
      initialSeed: "a",
      accumulator: {
        vertices: new Map([
          ["a", { vertex: v("a"), receivedAtMs: 1, expansionIndexes: [0] }],
          ["b", { vertex: v("b"), receivedAtMs: 1, expansionIndexes: [0] }],
          ["x", { vertex: v("x"), receivedAtMs: 1, expansionIndexes: [0] }],
          ["y", { vertex: v("y"), receivedAtMs: 1, expansionIndexes: [0] }],
        ]),
        edges: new Map([
          [
            "a→b",
            { edge: e("a", "b"), receivedAtMs: 1, expansionIndexes: [0] },
          ],
          [
            "x→y",
            { edge: e("x", "y"), receivedAtMs: 1, expansionIndexes: [0] },
          ],
        ]),
      },
      expansions: [
        {
          id: 1,
          origin: "a",
          controls: INITIAL_ILLUMINATE_STATE.controls,
          startedAtMs: 0,
          vertexKeys: ["a", "b", "x", "y"],
          edgeIds: ["a→b", "x→y"],
        },
      ],
    };
    const view = selectGraphView(state);
    const byId = new Map(view.nodes.map((n) => [n.id, n.hopDistance]));
    expect(byId.get("a")).toBe(0);
    expect(byId.get("b")).toBe(1);
    expect(byId.get("x")).toBe(Number.POSITIVE_INFINITY);
    expect(byId.get("y")).toBe(Number.POSITIVE_INFINITY);
  });

  it("never grows an existing vertex's hopDistance after a new expansion (#460 monotonic shrink invariant)", () => {
    let state = stateWithInitialSeed("a");
    state = applyExpansion(state, {
      expansionId: 1,
      origin: "a",
      vertices: [v("a"), v("b"), v("c"), v("d")],
      edges: [e("a", "b"), e("b", "c"), e("c", "d")],
    });
    const firstByKey = new Map(
      selectGraphView(state).nodes.map((n) => [n.id, n.hopDistance]),
    );
    // Pull in 'd' as an additional expansion origin — every other
    // vertex's distance can only stay the same or shrink.
    state = applyExpansion(state, {
      expansionId: 2,
      origin: "d",
      vertices: [v("d")],
      edges: [],
    });
    const secondByKey = new Map(
      selectGraphView(state).nodes.map((n) => [n.id, n.hopDistance]),
    );
    for (const [key, before] of firstByKey) {
      const after = secondByKey.get(key);
      expect(after).toBeDefined();
      expect(after!).toBeLessThanOrEqual(before);
    }
    // And the newly-promoted origin really did drop to 0.
    expect(secondByKey.get("d")).toBe(0);
  });

  it("does not let an expired edge bridge a BFS gap (#459/#460 interaction)", () => {
    let state = stateWithInitialSeed("a");
    state = applyExpansion(state, {
      expansionId: 1,
      origin: "a",
      vertices: [v("a"), v("b"), v("c")],
      edges: [
        // a→b is live; b→c is expired. Without filtering, 'c' would
        // be hop 2; with filtering it must be Infinity.
        e("a", "b", 1),
        eWithExpiration("b", "c", isoAt(-1_000)),
      ],
    });
    const view = selectGraphView(state, T_NOW);
    const byId = new Map(view.nodes.map((n) => [n.id, n.hopDistance]));
    expect(byId.get("a")).toBe(0);
    expect(byId.get("b")).toBe(1);
    expect(byId.get("c")).toBe(Number.POSITIVE_INFINITY);
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

describe("selectExpansionChips (#456)", () => {
  it("is empty when no expansion has happened yet", () => {
    expect(selectExpansionChips(INITIAL_ILLUMINATE_STATE)).toEqual([]);
  });

  it("emits one chip per expansion in chronological order, with the seed flag on chip 0", () => {
    let state = stateWithInitialSeed("Distributed_computing");
    state = applyExpansion(state, {
      expansionId: 1,
      origin: "Distributed_computing",
      vertices: [v("Distributed_computing"), v("CAP_theorem")],
      edges: [e("Distributed_computing", "CAP_theorem")],
    });
    state = applyExpansion(state, {
      expansionId: 2,
      origin: "CAP_theorem",
      vertices: [v("CAP_theorem"), v("Eric_Brewer_(scientist)")],
      edges: [e("CAP_theorem", "Eric_Brewer_(scientist)")],
    });
    state = applyExpansion(state, {
      expansionId: 3,
      origin: "Eric_Brewer_(scientist)",
      vertices: [v("Eric_Brewer_(scientist)"), v("Larry_Page")],
      edges: [e("Eric_Brewer_(scientist)", "Larry_Page")],
    });

    const chips = selectExpansionChips(state);
    expect(chips).toHaveLength(3);
    expect(chips.map((c) => c.originKey)).toEqual([
      "Distributed_computing",
      "CAP_theorem",
      "Eric_Brewer_(scientist)",
    ]);
    expect(chips[0]).toMatchObject({ isSeed: true, index: 0 });
    expect(chips[1]).toMatchObject({ isSeed: false, index: 1 });
    expect(chips[2]).toMatchObject({ isSeed: false, index: 2 });
  });

  it("preserves duplicate origin keys when the user re-clicks the same vertex", () => {
    let state = stateWithInitialSeed("a");
    state = applyExpansion(state, {
      expansionId: 1,
      origin: "a",
      vertices: [v("a")],
      edges: [],
    });
    state = applyExpansion(state, {
      expansionId: 2,
      origin: "a",
      vertices: [v("a")],
      edges: [],
    });
    const chips = selectExpansionChips(state);
    expect(chips.map((c) => c.originKey)).toEqual(["a", "a"]);
    // Both chips keep their distinct React keys (Expansion.id).
    expect(new Set(chips.map((c) => c.id)).size).toBe(2);
    expect(chips[0]?.isSeed).toBe(true);
    expect(chips[1]?.isSeed).toBe(false);
  });
});

describe("selectInspectedDetail", () => {
  it("returns null for a null key, empty key, or unknown vertex", () => {
    let state = stateWithInitialSeed("hub");
    state = applyExpansion(state, {
      expansionId: 1,
      origin: "hub",
      vertices: [v("hub"), v("left")],
      edges: [e("hub", "left")],
    });
    expect(selectInspectedDetail(state, null)).toBeNull();
    expect(selectInspectedDetail(state, "")).toBeNull();
    expect(selectInspectedDetail(state, "not-in-accumulator")).toBeNull();
  });

  it("projects the vertex and its live outgoing edges, sorted by target", () => {
    let state = stateWithInitialSeed("hub");
    state = applyExpansion(state, {
      expansionId: 1,
      origin: "hub",
      vertices: [v("hub"), v("alpha"), v("beta"), v("gamma")],
      // Intentionally out of target order so the sort is observable.
      edges: [
        e("hub", "gamma", 3),
        e("hub", "alpha", 1),
        e("hub", "beta", 2),
        // An edge that does NOT originate at hub must be excluded.
        e("alpha", "beta", 9),
      ],
    });

    const detail = selectInspectedDetail(state, "hub");
    expect(detail).not.toBeNull();
    expect(detail?.key).toBe("hub");
    expect(detail?.vertex.key).toBe("hub");
    expect(detail?.outgoing.map((o) => o.target)).toEqual([
      "alpha",
      "beta",
      "gamma",
    ]);
    expect(detail?.outgoing.map((o) => o.weight)).toEqual([1, 2, 3]);
    // Stable edge ids mirror `edgeIdOf(tail, head)`.
    expect(detail?.outgoing[0]?.id).toBe("hub\u2192alpha");
  });

  it("drops the inspected vertex once it has expired", () => {
    const past = new Date(Date.now() - 60_000).toISOString();
    let state = stateWithInitialSeed("hub");
    state = applyExpansion(state, {
      expansionId: 1,
      origin: "hub",
      vertices: [{ key: "hub", expiration: past }, v("left")],
      edges: [e("hub", "left")],
    });
    // With a wall clock after the expiry, the vertex is gone → null so the
    // Drawer self-closes.
    expect(selectInspectedDetail(state, "hub", Date.now())).toBeNull();
    // But reading it as-of a time BEFORE the expiry still resolves it.
    expect(
      selectInspectedDetail(state, "hub", Date.parse(past) - 1_000),
    ).not.toBeNull();
  });

  it("filters expired outgoing edges out of the projection", () => {
    const past = new Date(Date.now() - 60_000).toISOString();
    let state = stateWithInitialSeed("hub");
    state = applyExpansion(state, {
      expansionId: 1,
      origin: "hub",
      vertices: [v("hub"), v("live"), v("dead")],
      edges: [
        { tail: "hub", head: "live", weight: 1 },
        { tail: "hub", head: "dead", weight: 1, expiration: past },
      ],
    });
    const detail = selectInspectedDetail(state, "hub", Date.now());
    expect(detail?.outgoing.map((o) => o.target)).toEqual(["live"]);
  });
});

describe("filterInboundEdges", () => {
  it("keeps only edges whose head exactly equals the key", () => {
    const edges: Edge[] = [
      e("a", "target"),
      e("b", "target"),
      // Prefix over-match the wire `headPrefix` scan can return.
      e("c", "target:child"),
      e("d", "other"),
    ];
    const inbound = filterInboundEdges(edges, "target");
    expect(inbound.map((x) => x.tail)).toEqual(["a", "b"]);
  });

  it("returns an empty array for an empty key", () => {
    expect(filterInboundEdges([e("a", "")], "")).toEqual([]);
  });
});
