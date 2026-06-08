import { describe, expect, it } from "bun:test";
import type { Edge, Vertex } from "~/lib/client/infrastructure/api/illuminate";
import { illuminateReducer } from "./reducer";
import {
  ACCUMULATOR_HARD_CAP,
  DEFAULT_ILLUMINATE_CONTROLS,
  edgeIdOf,
  INITIAL_ILLUMINATE_STATE,
  type IlluminateControls,
  type IlluminateState,
} from "./state";

/**
 * Helpers — keep test bodies focused on the assertion. We never reach
 * for `as any`: the model is small enough to type honestly.
 */
function v(key: string): Vertex {
  return { key };
}

function e(tail: string, head: string, weight = 1): Edge {
  return { tail, head, weight };
}

function afterUrlSeed(seed: string | null): IlluminateState {
  return illuminateReducer(INITIAL_ILLUMINATE_STATE, {
    type: "INITIAL_SEED_CHANGED",
    seed,
  });
}

function afterExpansion(
  state: IlluminateState,
  opts: {
    expansionId: number;
    origin: string;
    vertices: Vertex[];
    edges: Edge[];
    receivedAtMs?: number;
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
    receivedAtMs: opts.receivedAtMs ?? 100,
  });
}

describe("illuminateReducer", () => {
  describe("INITIAL_SEED_CHANGED", () => {
    it("is identity when the seed is unchanged (both null)", () => {
      const next = illuminateReducer(INITIAL_ILLUMINATE_STATE, {
        type: "INITIAL_SEED_CHANGED",
        seed: null,
      });
      expect(next).toBe(INITIAL_ILLUMINATE_STATE);
    });

    it("adopts a new URL seed and resets the accumulator", () => {
      const seeded = afterUrlSeed("user:1");
      expect(seeded.initialSeed).toBe("user:1");
      expect(seeded.accumulator.vertices.size).toBe(0);
      expect(seeded.expansions).toEqual([]);
      expect(seeded.status).toBe("idle");
      expect(seeded.error).toBeNull();
    });

    it("preserves controls across an INITIAL_SEED_CHANGED", () => {
      const custom: IlluminateControls = {
        ...DEFAULT_ILLUMINATE_CONTROLS,
        step: 4,
        k: 16,
      };
      const withControls = illuminateReducer(afterUrlSeed("a"), {
        type: "CONTROLS_CHANGED",
        controls: custom,
      });
      const reseeded = illuminateReducer(withControls, {
        type: "INITIAL_SEED_CHANGED",
        seed: "b",
      });
      expect(reseeded.controls).toEqual(custom);
      expect(reseeded.initialSeed).toBe("b");
    });

    it("clears the seed back to null when the URL drops ?seed=", () => {
      const seeded = afterUrlSeed("a");
      const cleared = illuminateReducer(seeded, {
        type: "INITIAL_SEED_CHANGED",
        seed: null,
      });
      expect(cleared.initialSeed).toBeNull();
      expect(cleared.accumulator.vertices.size).toBe(0);
    });
  });

  describe("EXPANSION lifecycle", () => {
    it("flips to loading on EXPANSION_REQUESTED", () => {
      const seeded = afterUrlSeed("a");
      const next = illuminateReducer(seeded, {
        type: "EXPANSION_REQUESTED",
        expansionId: 1,
        origin: "a",
        controls: seeded.controls,
        startedAtMs: 0,
      });
      expect(next.status).toBe("loading");
      expect(next.pendingCount).toBe(1);
    });

    it("merges vertices and edges and appends to expansions", () => {
      const seeded = afterUrlSeed("a");
      const next = afterExpansion(seeded, {
        expansionId: 1,
        origin: "a",
        vertices: [v("a"), v("b"), v("c")],
        edges: [e("a", "b", 2), e("a", "c", 3)],
      });
      expect(next.accumulator.vertices.size).toBe(3);
      expect(next.accumulator.edges.size).toBe(2);
      expect(next.expansions).toHaveLength(1);
      expect(next.expansions[0]?.origin).toBe("a");
      expect(next.expansions[0]?.vertexKeys).toEqual(["a", "b", "c"]);
      expect(next.expansions[0]?.edgeIds).toEqual([
        edgeIdOf("a", "b"),
        edgeIdOf("a", "c"),
      ]);
      expect(next.status).toBe("ready");
      expect(next.pendingCount).toBe(0);
    });

    it("is additive across two expansions — no node is dropped", () => {
      const seeded = afterUrlSeed("a");
      const after1 = afterExpansion(seeded, {
        expansionId: 1,
        origin: "a",
        vertices: [v("a"), v("b")],
        edges: [e("a", "b")],
      });
      const after2 = afterExpansion(after1, {
        expansionId: 2,
        origin: "b",
        vertices: [v("b"), v("c")],
        edges: [e("b", "c")],
      });
      expect(after2.accumulator.vertices.size).toBe(3);
      expect([...after2.accumulator.vertices.keys()].sort()).toEqual([
        "a",
        "b",
        "c",
      ]);
      // b appears in both expansion-index lists; latest-wins on the
      // `vertex` payload.
      expect(after2.accumulator.vertices.get("b")?.expansionIndexes).toEqual([
        0, 1,
      ]);
      expect(after2.expansions).toHaveLength(2);
      expect(after2.expansions[1]?.origin).toBe("b");
      // `vertexKeys` mirrors the response payload — both `b` and `c`
      // came back even though `b` was already in the accumulator.
      expect(after2.expansions[1]?.vertexKeys).toEqual(["b", "c"]);
      expect(after2.expansions[1]?.edgeIds).toEqual([edgeIdOf("b", "c")]);
    });

    it("is latest-wins on the vertex payload (#466 D3/D4)", () => {
      const seeded = afterUrlSeed("a");
      const first = afterExpansion(seeded, {
        expansionId: 1,
        origin: "a",
        vertices: [{ key: "a", string: "old" }],
        edges: [],
        receivedAtMs: 50,
      });
      const second = afterExpansion(first, {
        expansionId: 2,
        origin: "a",
        vertices: [{ key: "a", string: "new" }],
        edges: [],
        receivedAtMs: 100,
      });
      expect(second.accumulator.vertices.get("a")?.vertex.string).toBe("new");
      expect(second.accumulator.vertices.get("a")?.receivedAtMs).toBe(100);
    });

    it("dedups vertex keys within a single response", () => {
      const seeded = afterUrlSeed("a");
      const next = afterExpansion(seeded, {
        expansionId: 1,
        origin: "a",
        vertices: [v("a"), v("a"), v("b")],
        edges: [],
      });
      expect(next.accumulator.vertices.size).toBe(2);
      expect(next.expansions[0]?.vertexKeys).toEqual(["a", "b"]);
    });

    it("drops vertices without a key", () => {
      const seeded = afterUrlSeed("a");
      const next = afterExpansion(seeded, {
        expansionId: 1,
        origin: "a",
        vertices: [v("a"), { key: "" }],
        edges: [],
      });
      expect([...next.accumulator.vertices.keys()]).toEqual(["a"]);
    });

    it("drops edges missing tail or head", () => {
      const seeded = afterUrlSeed("a");
      const next = afterExpansion(seeded, {
        expansionId: 1,
        origin: "a",
        vertices: [v("a"), v("b")],
        edges: [e("a", "b"), { tail: "", head: "b", weight: 1 }],
      });
      expect(next.accumulator.edges.size).toBe(1);
    });

    it("records an EXPANSION_FAILED error and decrements pendingCount", () => {
      const seeded = afterUrlSeed("a");
      const requested = illuminateReducer(seeded, {
        type: "EXPANSION_REQUESTED",
        expansionId: 1,
        origin: "a",
        controls: seeded.controls,
        startedAtMs: 0,
      });
      const failed = illuminateReducer(requested, {
        type: "EXPANSION_FAILED",
        expansionId: 1,
        error: "boom",
      });
      expect(failed.status).toBe("error");
      expect(failed.error).toBe("boom");
      expect(failed.pendingCount).toBe(0);
    });

    it("treats EXPANSION_FAILED with empty error as a silent abort", () => {
      const seeded = afterUrlSeed("a");
      const requested = illuminateReducer(seeded, {
        type: "EXPANSION_REQUESTED",
        expansionId: 1,
        origin: "a",
        controls: seeded.controls,
        startedAtMs: 0,
      });
      const aborted = illuminateReducer(requested, {
        type: "EXPANSION_FAILED",
        expansionId: 1,
        error: "",
      });
      // Error is still recorded but is the empty string — the page won't
      // render a MessageBar for an empty error.
      expect(aborted.pendingCount).toBe(0);
      expect(aborted.error).toBe("");
    });
  });

  describe("hard cap (#466 D13)", () => {
    it("rejects an EXPANSION_RECEIVED that would push past the hard cap", () => {
      let state = afterUrlSeed("a");
      // Pre-fill the accumulator to one below the cap.
      const bigVertices: Vertex[] = [];
      for (let i = 0; i < ACCUMULATOR_HARD_CAP - 1; i += 1) {
        bigVertices.push(v(`pre:${i}`));
      }
      state = afterExpansion(state, {
        expansionId: 1,
        origin: "a",
        vertices: bigVertices,
        edges: [],
      });
      expect(state.accumulator.vertices.size).toBe(ACCUMULATOR_HARD_CAP - 1);

      // Now request an expansion that adds two more vertices — over cap.
      const requested = illuminateReducer(state, {
        type: "EXPANSION_REQUESTED",
        expansionId: 2,
        origin: "a",
        controls: state.controls,
        startedAtMs: 0,
      });
      const rejected = illuminateReducer(requested, {
        type: "EXPANSION_RECEIVED",
        expansionId: 2,
        origin: "a",
        controls: state.controls,
        startedAtMs: 0,
        vertices: [v("over:0"), v("over:1")],
        edges: [],
        receivedAtMs: 1,
      });
      // Accumulator untouched.
      expect(rejected.accumulator.vertices.size).toBe(ACCUMULATOR_HARD_CAP - 1);
      // Expansions list NOT appended.
      expect(rejected.expansions).toHaveLength(state.expansions.length);
      expect(rejected.status).toBe("error");
      expect(rejected.error).toMatch(/hard cap/);
    });
  });

  describe("CONTROLS_CHANGED", () => {
    it("is identity when the controls are unchanged by value", () => {
      const seeded = afterUrlSeed("a");
      const next = illuminateReducer(seeded, {
        type: "CONTROLS_CHANGED",
        controls: { ...DEFAULT_ILLUMINATE_CONTROLS },
      });
      expect(next).toBe(seeded);
    });

    it("updates controls without touching the accumulator or expansions (#466 D8)", () => {
      let state = afterUrlSeed("a");
      state = afterExpansion(state, {
        expansionId: 1,
        origin: "a",
        vertices: [v("a"), v("b")],
        edges: [e("a", "b")],
      });
      const before = state;
      const next = illuminateReducer(state, {
        type: "CONTROLS_CHANGED",
        controls: { ...DEFAULT_ILLUMINATE_CONTROLS, step: 4 },
      });
      expect(next.controls.step).toBe(4);
      expect(next.accumulator).toBe(before.accumulator);
      expect(next.expansions).toBe(before.expansions);
    });
  });

  describe("CLEARED", () => {
    it("empties accumulator and expansions but preserves initialSeed + controls", () => {
      let state = afterUrlSeed("a");
      state = illuminateReducer(state, {
        type: "CONTROLS_CHANGED",
        controls: { ...DEFAULT_ILLUMINATE_CONTROLS, k: 16 },
      });
      state = afterExpansion(state, {
        expansionId: 1,
        origin: "a",
        vertices: [v("a"), v("b")],
        edges: [e("a", "b")],
      });
      const cleared = illuminateReducer(state, { type: "CLEARED" });
      expect(cleared.initialSeed).toBe("a");
      expect(cleared.controls.k).toBe(16);
      expect(cleared.accumulator.vertices.size).toBe(0);
      expect(cleared.expansions).toEqual([]);
      expect(cleared.status).toBe("idle");
      expect(cleared.pendingCount).toBe(0);
    });
  });

  describe("RESET", () => {
    it("wipes everything except controls", () => {
      let state = afterUrlSeed("a");
      state = illuminateReducer(state, {
        type: "CONTROLS_CHANGED",
        controls: { ...DEFAULT_ILLUMINATE_CONTROLS, step: 3 },
      });
      state = afterExpansion(state, {
        expansionId: 1,
        origin: "a",
        vertices: [v("a"), v("b")],
        edges: [e("a", "b")],
      });
      const next = illuminateReducer(state, { type: "RESET" });
      expect(next.initialSeed).toBeNull();
      expect(next.accumulator.vertices.size).toBe(0);
      expect(next.expansions).toEqual([]);
      expect(next.controls.step).toBe(3); // preserved
    });
  });
});
