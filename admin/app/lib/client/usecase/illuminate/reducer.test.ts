import { describe, expect, it } from "bun:test";
import { illuminateReducer, type IlluminateAction } from "./reducer";
import {
  DEFAULT_ILLUMINATE_CONTROLS,
  INITIAL_ILLUMINATE_STATE,
  type IlluminateControls,
  type IlluminateFrame,
  type IlluminateState,
} from "./state";

function withSeed(seed: string): IlluminateState {
  // Mimic an arrival from a URL-level navigation.
  return illuminateReducer(INITIAL_ILLUMINATE_STATE, {
    type: "SEED_CHANGED",
    seed,
  });
}

function frame(seed: string): IlluminateFrame {
  return {
    seed,
    controls: DEFAULT_ILLUMINATE_CONTROLS,
    vertices: [{ key: seed }],
    edges: [],
  };
}

describe("illuminateReducer", () => {
  describe("SEED_CHANGED", () => {
    it("is identity when the seed is unchanged", () => {
      const next = illuminateReducer(INITIAL_ILLUMINATE_STATE, {
        type: "SEED_CHANGED",
        seed: "",
      });
      expect(next).toBe(INITIAL_ILLUMINATE_STATE);
    });

    it("replaces history and bumps the epoch", () => {
      const next = illuminateReducer(INITIAL_ILLUMINATE_STATE, {
        type: "SEED_CHANGED",
        seed: "user:1",
      });
      expect(next.seed).toBe("user:1");
      expect(next.history).toEqual(["user:1"]);
      expect(next.fetchEpoch).toBe(1);
      expect(next.frame).toBeNull();
    });

    it("empties history when the seed is cleared", () => {
      const seeded = withSeed("user:1");
      const next = illuminateReducer(seeded, {
        type: "SEED_CHANGED",
        seed: "",
      });
      expect(next.seed).toBe("");
      expect(next.history).toEqual([]);
      expect(next.status).toBe("idle");
    });
  });

  describe("SEED_PUSHED / SEED_POPPED", () => {
    it("pushes a new seed onto history", () => {
      const a = withSeed("a");
      const ab = illuminateReducer(a, { type: "SEED_PUSHED", seed: "b" });
      expect(ab.seed).toBe("b");
      expect(ab.history).toEqual(["a", "b"]);
      expect(ab.fetchEpoch).toBe(a.fetchEpoch + 1);
    });

    it("ignores SEED_PUSHED for an empty or duplicate seed", () => {
      const a = withSeed("a");
      expect(illuminateReducer(a, { type: "SEED_PUSHED", seed: "" })).toBe(a);
      expect(illuminateReducer(a, { type: "SEED_PUSHED", seed: "a" })).toBe(a);
    });

    it("pops back to the previous seed", () => {
      let state = withSeed("a");
      state = illuminateReducer(state, { type: "SEED_PUSHED", seed: "b" });
      state = illuminateReducer(state, { type: "SEED_PUSHED", seed: "c" });
      const popped = illuminateReducer(state, { type: "SEED_POPPED" });
      expect(popped.seed).toBe("b");
      expect(popped.history).toEqual(["a", "b"]);
    });

    it("ignores SEED_POPPED at the root of history", () => {
      const a = withSeed("a");
      expect(illuminateReducer(a, { type: "SEED_POPPED" })).toBe(a);
    });
  });

  describe("CONTROLS_CHANGED", () => {
    it("is identity when the controls are unchanged by value", () => {
      const a = withSeed("a");
      const next = illuminateReducer(a, {
        type: "CONTROLS_CHANGED",
        controls: { ...DEFAULT_ILLUMINATE_CONTROLS },
      });
      expect(next).toBe(a);
    });

    it("records new controls and bumps the epoch", () => {
      const a = withSeed("a");
      const controls: IlluminateControls = {
        ...DEFAULT_ILLUMINATE_CONTROLS,
        step: 4,
        k: 16,
      };
      const next = illuminateReducer(a, {
        type: "CONTROLS_CHANGED",
        controls,
      });
      expect(next.controls).toEqual(controls);
      expect(next.fetchEpoch).toBe(a.fetchEpoch + 1);
    });

    it("keeps the existing frame on screen while reloading", () => {
      let state = withSeed("a");
      state = illuminateReducer(state, {
        type: "FETCH_RECEIVED",
        epoch: state.fetchEpoch,
        frame: frame("a"),
      });
      const next = illuminateReducer(state, {
        type: "CONTROLS_CHANGED",
        controls: { ...DEFAULT_ILLUMINATE_CONTROLS, step: 3 },
      });
      expect(next.frame).toBe(state.frame);
    });
  });

  describe("REFETCH_REQUESTED", () => {
    it("bumps the epoch when there is an active seed", () => {
      const a = withSeed("a");
      const next = illuminateReducer(a, { type: "REFETCH_REQUESTED" });
      expect(next.fetchEpoch).toBe(a.fetchEpoch + 1);
    });

    it("is a no-op when there is no seed", () => {
      const next = illuminateReducer(INITIAL_ILLUMINATE_STATE, {
        type: "REFETCH_REQUESTED",
      });
      expect(next).toBe(INITIAL_ILLUMINATE_STATE);
    });
  });

  describe("FETCH_REQUESTED / FETCH_RECEIVED / FETCH_FAILED", () => {
    it("transitions to loading on a fresh epoch", () => {
      const a = withSeed("a");
      const next = illuminateReducer(a, {
        type: "FETCH_REQUESTED",
        epoch: a.fetchEpoch,
      });
      expect(next.status).toBe("loading");
    });

    it("drops stale request markers", () => {
      const a = withSeed("a");
      const action: IlluminateAction = {
        type: "FETCH_REQUESTED",
        epoch: a.fetchEpoch - 1,
      };
      expect(illuminateReducer(a, action)).toBe(a);
    });

    it("records the received frame and flips to ready", () => {
      const a = withSeed("a");
      const next = illuminateReducer(a, {
        type: "FETCH_RECEIVED",
        epoch: a.fetchEpoch,
        frame: frame("a"),
      });
      expect(next.status).toBe("ready");
      expect(next.frame?.seed).toBe("a");
    });

    it("drops stale frames", () => {
      const a = withSeed("a");
      const next = illuminateReducer(a, {
        type: "FETCH_RECEIVED",
        epoch: a.fetchEpoch - 1,
        frame: frame("stale"),
      });
      expect(next).toBe(a);
    });

    it("records errors at the current epoch", () => {
      const a = withSeed("a");
      const next = illuminateReducer(a, {
        type: "FETCH_FAILED",
        epoch: a.fetchEpoch,
        error: "boom",
      });
      expect(next.status).toBe("error");
      expect(next.error).toBe("boom");
    });

    it("drops stale errors", () => {
      const a = withSeed("a");
      const next = illuminateReducer(a, {
        type: "FETCH_FAILED",
        epoch: a.fetchEpoch - 1,
        error: "old",
      });
      expect(next).toBe(a);
    });
  });

  describe("RESET", () => {
    it("returns to initial state and bumps the epoch", () => {
      const a = withSeed("a");
      const next = illuminateReducer(a, { type: "RESET" });
      expect(next.seed).toBe("");
      expect(next.history).toEqual([]);
      expect(next.fetchEpoch).toBe(a.fetchEpoch + 1);
    });
  });
});
