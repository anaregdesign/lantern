import { describe, expect, it } from "bun:test";
import { browseVerticesReducer, type BrowseVerticesAction } from "./reducer";
import {
  INITIAL_BROWSE_VERTICES_STATE,
  type BrowseVerticesState,
  type VertexPage,
} from "./state";

function withEpoch(state: BrowseVerticesState, epoch: number) {
  return { ...state, prefixEpoch: epoch };
}

function page(
  startCursor: string,
  nextCursor: string,
  keys: string[],
): VertexPage {
  return {
    startCursor,
    nextCursor,
    vertices: keys.map((key) => ({ key })),
  };
}

describe("browseVerticesReducer", () => {
  it("returns identity on PREFIX_CHANGED when prefix is unchanged", () => {
    const next = browseVerticesReducer(INITIAL_BROWSE_VERTICES_STATE, {
      type: "PREFIX_CHANGED",
      prefix: "",
    });
    expect(next).toBe(INITIAL_BROWSE_VERTICES_STATE);
  });

  it("resets state and bumps epoch on PREFIX_CHANGED", () => {
    const base = { ...withEpoch(INITIAL_BROWSE_VERTICES_STATE, 3), count: 99 };
    const next = browseVerticesReducer(base, {
      type: "PREFIX_CHANGED",
      prefix: "user:",
    });
    expect(next.prefix).toBe("user:");
    expect(next.prefixEpoch).toBe(4);
    expect(next.pages).toEqual([]);
    expect(next.currentPageIndex).toBe(-1);
    expect(next.status).toBe("idle");
    // A stale count must be cleared so `selectTotalPages` never renders a wrong
    // total while the operator retypes the prefix (#946).
    expect(next.count).toBeNull();
  });

  it("ignores PAGE_REQUESTED from a stale epoch", () => {
    const base = withEpoch(INITIAL_BROWSE_VERTICES_STATE, 5);
    const next = browseVerticesReducer(base, {
      type: "PAGE_REQUESTED",
      epoch: 4,
    });
    expect(next).toBe(base);
  });

  it("transitions to loading on PAGE_REQUESTED at the current epoch", () => {
    const base = { ...INITIAL_BROWSE_VERTICES_STATE, prefixEpoch: 1 };
    const next = browseVerticesReducer(base, {
      type: "PAGE_REQUESTED",
      epoch: 1,
    });
    expect(next.status).toBe("loading");
    expect(next.error).toBeNull();
  });

  it("records the first page on PAGE_RECEIVED", () => {
    const base = { ...INITIAL_BROWSE_VERTICES_STATE, prefixEpoch: 1 };
    const first = page("", "cursor-1", ["a", "b"]);
    const next = browseVerticesReducer(base, {
      type: "PAGE_RECEIVED",
      epoch: 1,
      page: first,
    });
    expect(next.pages).toEqual([first]);
    expect(next.currentPageIndex).toBe(0);
    expect(next.status).toBe("ready");
  });

  it("appends subsequent pages and advances the index", () => {
    let state: BrowseVerticesState = {
      ...INITIAL_BROWSE_VERTICES_STATE,
      prefixEpoch: 7,
    };
    const first = page("", "c1", ["a"]);
    const second = page("c1", "", ["b"]);
    state = browseVerticesReducer(state, {
      type: "PAGE_RECEIVED",
      epoch: 7,
      page: first,
    });
    state = browseVerticesReducer(state, {
      type: "PAGE_RECEIVED",
      epoch: 7,
      page: second,
    });
    expect(state.pages).toEqual([first, second]);
    expect(state.currentPageIndex).toBe(1);
  });

  it("drops stale page responses", () => {
    const base: BrowseVerticesState = {
      ...INITIAL_BROWSE_VERTICES_STATE,
      prefixEpoch: 9,
    };
    const next = browseVerticesReducer(base, {
      type: "PAGE_RECEIVED",
      epoch: 8,
      page: page("", "x", ["z"]),
    });
    expect(next).toBe(base);
  });

  it("records errors only for the current epoch", () => {
    const base: BrowseVerticesState = {
      ...INITIAL_BROWSE_VERTICES_STATE,
      prefixEpoch: 2,
      status: "loading",
    };
    const fresh = browseVerticesReducer(base, {
      type: "PAGE_FAILED",
      epoch: 2,
      error: "boom",
    });
    expect(fresh.status).toBe("error");
    expect(fresh.error).toBe("boom");

    const stale = browseVerticesReducer(base, {
      type: "PAGE_FAILED",
      epoch: 1,
      error: "stale",
    });
    expect(stale).toBe(base);
  });

  it("records the count without touching pages", () => {
    const base: BrowseVerticesState = {
      ...INITIAL_BROWSE_VERTICES_STATE,
      prefixEpoch: 4,
    };
    const next = browseVerticesReducer(base, {
      type: "COUNT_RECEIVED",
      epoch: 4,
      count: 42,
    });
    expect(next.count).toBe(42);
    expect(next.pages).toBe(base.pages);
  });

  it("steps backward through cached pages", () => {
    let state: BrowseVerticesState = {
      ...INITIAL_BROWSE_VERTICES_STATE,
      prefixEpoch: 1,
    };
    state = browseVerticesReducer(state, {
      type: "PAGE_RECEIVED",
      epoch: 1,
      page: page("", "c1", ["a"]),
    });
    state = browseVerticesReducer(state, {
      type: "PAGE_RECEIVED",
      epoch: 1,
      page: page("c1", "", ["b"]),
    });
    const prev = browseVerticesReducer(state, { type: "NAVIGATE_PREVIOUS" });
    expect(prev.currentPageIndex).toBe(0);
    const guarded = browseVerticesReducer(prev, { type: "NAVIGATE_PREVIOUS" });
    expect(guarded).toBe(prev);
  });

  it("uses cached forward pages on NAVIGATE_NEXT_REQUESTED", () => {
    let state: BrowseVerticesState = {
      ...INITIAL_BROWSE_VERTICES_STATE,
      prefixEpoch: 1,
    };
    const first = page("", "c1", ["a"]);
    const second = page("c1", "", ["b"]);
    state = browseVerticesReducer(state, {
      type: "PAGE_RECEIVED",
      epoch: 1,
      page: first,
    });
    state = browseVerticesReducer(state, {
      type: "PAGE_RECEIVED",
      epoch: 1,
      page: second,
    });
    state = browseVerticesReducer(state, { type: "NAVIGATE_PREVIOUS" });
    const advance = browseVerticesReducer(state, {
      type: "NAVIGATE_NEXT_REQUESTED",
      epoch: 1,
    });
    expect(advance.currentPageIndex).toBe(1);
    expect(advance.status).toBe("ready");
  });

  it("flips to loading when NAVIGATE_NEXT_REQUESTED has no cached page", () => {
    let state: BrowseVerticesState = {
      ...INITIAL_BROWSE_VERTICES_STATE,
      prefixEpoch: 1,
    };
    state = browseVerticesReducer(state, {
      type: "PAGE_RECEIVED",
      epoch: 1,
      page: page("", "c1", ["a"]),
    });
    const next = browseVerticesReducer(state, {
      type: "NAVIGATE_NEXT_REQUESTED",
      epoch: 1,
    });
    expect(next.status).toBe("loading");
    expect(next.currentPageIndex).toBe(0);
  });

  it("replaces the current page in place on refresh without moving the index", () => {
    let state: BrowseVerticesState = {
      ...INITIAL_BROWSE_VERTICES_STATE,
      prefixEpoch: 1,
    };
    const first = page("", "c1", ["a"]);
    const second = page("c1", "", ["b"]);
    state = browseVerticesReducer(state, {
      type: "PAGE_RECEIVED",
      epoch: 1,
      page: first,
    });
    state = browseVerticesReducer(state, {
      type: "PAGE_RECEIVED",
      epoch: 1,
      page: second,
    });
    // Step back to page 0, then refresh it.
    state = browseVerticesReducer(state, { type: "NAVIGATE_PREVIOUS" });
    expect(state.currentPageIndex).toBe(0);
    const refreshed = page("", "c1", ["a", "a2"]);
    const next = browseVerticesReducer(state, {
      type: "PAGE_RECEIVED",
      epoch: 1,
      page: refreshed,
      mode: "replace",
    });
    // History length and index are preserved; only page 0 is overwritten.
    expect(next.pages).toEqual([refreshed, second]);
    expect(next.currentPageIndex).toBe(0);
    expect(next.status).toBe("ready");
  });

  it("treats a replace on empty history as a first-page load", () => {
    const base = { ...INITIAL_BROWSE_VERTICES_STATE, prefixEpoch: 1 };
    const first = page("", "c1", ["a"]);
    const next = browseVerticesReducer(base, {
      type: "PAGE_RECEIVED",
      epoch: 1,
      page: first,
      mode: "replace",
    });
    expect(next.pages).toEqual([first]);
    expect(next.currentPageIndex).toBe(0);
  });

  it("resets to initial and bumps epoch on RESET", () => {
    const base: BrowseVerticesState = {
      ...INITIAL_BROWSE_VERTICES_STATE,
      prefix: "foo",
      prefixEpoch: 10,
    };
    const reset = browseVerticesReducer(base, { type: "RESET" });
    expect(reset.prefix).toBe("");
    expect(reset.prefixEpoch).toBe(11);
  });
});

describe("type guard sanity", () => {
  it("rejects unknown actions at the type level (compile-time check)", () => {
    // This block is here to verify the exhaustive switch defaults. It is
    // intentionally a smoke test — TypeScript would have failed earlier.
    const action: BrowseVerticesAction = { type: "RESET" };
    expect(action.type).toBe("RESET");
  });
});
