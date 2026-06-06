import { describe, expect, it } from "bun:test";
import { selectCanPop, selectGraphView, selectIsBusy } from "./selectors";
import {
  DEFAULT_ILLUMINATE_CONTROLS,
  INITIAL_ILLUMINATE_STATE,
  type IlluminateFrame,
  type IlluminateState,
} from "./state";

function stateWith(frame: IlluminateFrame | null): IlluminateState {
  return {
    ...INITIAL_ILLUMINATE_STATE,
    seed: frame?.seed ?? "",
    history: frame ? [frame.seed] : [],
    frame,
    status: frame ? "ready" : "idle",
  };
}

describe("selectGraphView", () => {
  it("returns empty arrays when no frame has been received", () => {
    const view = selectGraphView(INITIAL_ILLUMINATE_STATE);
    expect(view.nodes).toEqual([]);
    expect(view.edges).toEqual([]);
  });

  it("marks the seed node and renders all returned vertices", () => {
    const frame: IlluminateFrame = {
      seed: "a",
      controls: DEFAULT_ILLUMINATE_CONTROLS,
      vertices: [{ key: "a" }, { key: "b" }, { key: "c" }],
      edges: [
        { tail: "a", head: "b", weight: 1 },
        { tail: "a", head: "c", weight: 3 },
      ],
    };
    const view = selectGraphView(stateWith(frame));
    expect(view.nodes.map((n) => n.id).sort()).toEqual(["a", "b", "c"]);
    const seed = view.nodes.find((n) => n.id === "a");
    expect(seed?.isSeed).toBe(true);
    expect(seed?.importance).toBe(1);
    const c = view.nodes.find((n) => n.id === "c");
    expect(c?.isSeed).toBe(false);
    // Importance is normalised against the highest weight sum (here `c`
    // shares all 3 weight units with the seed; `b` has 1 unit). The seed
    // is pinned to 1 regardless.
    expect(c?.importance).toBeGreaterThan(0);
    expect(c?.importance).toBeLessThanOrEqual(1);
  });

  it("drops vertices that lack a key", () => {
    const frame: IlluminateFrame = {
      seed: "a",
      controls: DEFAULT_ILLUMINATE_CONTROLS,
      vertices: [{ key: "a" }, { key: "" }, {} as { key?: string }],
      edges: [],
    };
    const view = selectGraphView(stateWith(frame));
    expect(view.nodes.map((n) => n.id)).toEqual(["a"]);
  });

  it("drops edges that reference unknown vertices", () => {
    const frame: IlluminateFrame = {
      seed: "a",
      controls: DEFAULT_ILLUMINATE_CONTROLS,
      vertices: [{ key: "a" }, { key: "b" }],
      edges: [
        { tail: "a", head: "b", weight: 1 },
        // `c` was not returned in the response; this edge must be dropped.
        { tail: "a", head: "c", weight: 1 },
      ],
    };
    const view = selectGraphView(stateWith(frame));
    expect(view.edges.map((e) => e.id)).toEqual(["a→b"]);
  });

  it("emits stable IDs for edges", () => {
    const frame: IlluminateFrame = {
      seed: "a",
      controls: DEFAULT_ILLUMINATE_CONTROLS,
      vertices: [{ key: "a" }, { key: "b" }],
      edges: [{ tail: "a", head: "b", weight: 1 }],
    };
    const view = selectGraphView(stateWith(frame));
    expect(view.edges[0]?.id).toBe("a→b");
    expect(view.edges[0]?.source).toBe("a");
    expect(view.edges[0]?.target).toBe("b");
  });
});

describe("selectCanPop", () => {
  it("is false until the user pushes a second seed", () => {
    expect(selectCanPop(INITIAL_ILLUMINATE_STATE)).toBe(false);
    const single: IlluminateState = {
      ...INITIAL_ILLUMINATE_STATE,
      seed: "a",
      history: ["a"],
    };
    expect(selectCanPop(single)).toBe(false);
    const stacked: IlluminateState = {
      ...INITIAL_ILLUMINATE_STATE,
      seed: "b",
      history: ["a", "b"],
    };
    expect(selectCanPop(stacked)).toBe(true);
  });
});

describe("selectIsBusy", () => {
  it("is true only during a loading state", () => {
    expect(selectIsBusy(INITIAL_ILLUMINATE_STATE)).toBe(false);
    expect(
      selectIsBusy({ ...INITIAL_ILLUMINATE_STATE, status: "loading" }),
    ).toBe(true);
    expect(selectIsBusy({ ...INITIAL_ILLUMINATE_STATE, status: "ready" })).toBe(
      false,
    );
  });
});
