import { describe, expect, it } from "bun:test";
import { browseEdgesReducer } from "./reducer";
import {
  INITIAL_BROWSE_EDGES_STATE,
  type BrowseEdgesState,
  type EdgePage,
} from "./state";

function page(
  startCursor: string,
  nextCursor: string,
  pairs: Array<[string, string]>,
): EdgePage {
  return {
    startCursor,
    nextCursor,
    edges: pairs.map(([tail, head]) => ({ tail, head, weight: 1 })),
  };
}

describe("browseEdgesReducer", () => {
  it("treats unchanged prefixes as a no-op", () => {
    const next = browseEdgesReducer(INITIAL_BROWSE_EDGES_STATE, {
      type: "PREFIXES_CHANGED",
      tailPrefix: "",
      headPrefix: "",
    });
    expect(next).toBe(INITIAL_BROWSE_EDGES_STATE);
  });

  it("resets and bumps epoch when either prefix changes", () => {
    const base = { ...INITIAL_BROWSE_EDGES_STATE, prefixEpoch: 2 };
    const next = browseEdgesReducer(base, {
      type: "PREFIXES_CHANGED",
      tailPrefix: "u:",
      headPrefix: "",
    });
    expect(next.tailPrefix).toBe("u:");
    expect(next.headPrefix).toBe("");
    expect(next.prefixEpoch).toBe(3);
    expect(next.pages).toEqual([]);
    expect(next.currentPageIndex).toBe(-1);
  });

  it("ignores PAGE_RECEIVED from a stale epoch", () => {
    const base: BrowseEdgesState = {
      ...INITIAL_BROWSE_EDGES_STATE,
      prefixEpoch: 5,
    };
    const next = browseEdgesReducer(base, {
      type: "PAGE_RECEIVED",
      epoch: 4,
      page: page("", "x", [["a", "b"]]),
    });
    expect(next).toBe(base);
  });

  it("appends pages and tracks the current index", () => {
    let state: BrowseEdgesState = {
      ...INITIAL_BROWSE_EDGES_STATE,
      prefixEpoch: 1,
    };
    const first = page("", "c1", [["a", "b"]]);
    const second = page("c1", "", [["a", "c"]]);
    state = browseEdgesReducer(state, {
      type: "PAGE_RECEIVED",
      epoch: 1,
      page: first,
    });
    state = browseEdgesReducer(state, {
      type: "PAGE_RECEIVED",
      epoch: 1,
      page: second,
    });
    expect(state.pages).toEqual([first, second]);
    expect(state.currentPageIndex).toBe(1);
  });

  it("steps backwards through cached pages", () => {
    let state: BrowseEdgesState = {
      ...INITIAL_BROWSE_EDGES_STATE,
      prefixEpoch: 1,
    };
    state = browseEdgesReducer(state, {
      type: "PAGE_RECEIVED",
      epoch: 1,
      page: page("", "c1", [["a", "b"]]),
    });
    state = browseEdgesReducer(state, {
      type: "PAGE_RECEIVED",
      epoch: 1,
      page: page("c1", "", [["a", "c"]]),
    });
    const prev = browseEdgesReducer(state, { type: "NAVIGATE_PREVIOUS" });
    expect(prev.currentPageIndex).toBe(0);
  });

  it("replaces the current page in place on refresh without moving the index", () => {
    let state: BrowseEdgesState = {
      ...INITIAL_BROWSE_EDGES_STATE,
      prefixEpoch: 1,
    };
    const first = page("", "c1", [["a", "b"]]);
    const second = page("c1", "", [["a", "c"]]);
    state = browseEdgesReducer(state, {
      type: "PAGE_RECEIVED",
      epoch: 1,
      page: first,
    });
    state = browseEdgesReducer(state, {
      type: "PAGE_RECEIVED",
      epoch: 1,
      page: second,
    });
    state = browseEdgesReducer(state, { type: "NAVIGATE_PREVIOUS" });
    expect(state.currentPageIndex).toBe(0);
    const refreshed = page("", "c1", [["a", "b2"]]);
    const next = browseEdgesReducer(state, {
      type: "PAGE_RECEIVED",
      epoch: 1,
      page: refreshed,
      mode: "replace",
    });
    expect(next.pages).toEqual([refreshed, second]);
    expect(next.currentPageIndex).toBe(0);
    expect(next.status).toBe("ready");
  });

  it("treats a replace on empty history as a first-page load", () => {
    const base: BrowseEdgesState = {
      ...INITIAL_BROWSE_EDGES_STATE,
      prefixEpoch: 1,
    };
    const first = page("", "c1", [["a", "b"]]);
    const next = browseEdgesReducer(base, {
      type: "PAGE_RECEIVED",
      epoch: 1,
      page: first,
      mode: "replace",
    });
    expect(next.pages).toEqual([first]);
    expect(next.currentPageIndex).toBe(0);
  });
});
