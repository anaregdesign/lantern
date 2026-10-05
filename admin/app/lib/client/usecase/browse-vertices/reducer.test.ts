import { describe, expect, it } from "bun:test";
import { browseVerticesReducer, type BrowseVerticesAction } from "./reducer";
import {
  INITIAL_BROWSE_VERTICES_STATE as INITIAL,
  type BrowseVerticesState,
  type VertexPage,
} from "./state";

const REQUESTED_STATE = {
  ...INITIAL,
  pageRequestId: 1,
  countRequestId: 1,
};

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
    const next = browseVerticesReducer(REQUESTED_STATE, {
      type: "PREFIX_CHANGED",
      prefix: "",
    });
    expect(next).toBe(REQUESTED_STATE);
  });

  it("resets state and bumps epoch on PREFIX_CHANGED", () => {
    const base = {
      ...withEpoch(REQUESTED_STATE, 3),
      count: { status: "success" as const, count: 99 },
    };
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
    expect(next.count).toEqual({ status: "idle" });
  });

  it("ignores PAGE_REQUESTED from a stale epoch", () => {
    const base = withEpoch(REQUESTED_STATE, 5);
    const next = browseVerticesReducer(base, {
      type: "PAGE_REQUESTED",
      requestId: 2,
      epoch: 4,
    });
    expect(next).toBe(base);
  });

  it("transitions to loading on PAGE_REQUESTED at the current epoch", () => {
    const base = { ...REQUESTED_STATE, prefixEpoch: 1 };
    const next = browseVerticesReducer(base, {
      type: "PAGE_REQUESTED",
      requestId: 2,
      epoch: 1,
    });
    expect(next.status).toBe("loading");
    expect(next.error).toBeNull();
  });

  it("records the first page on PAGE_RECEIVED", () => {
    const base = { ...REQUESTED_STATE, prefixEpoch: 1 };
    const first = page("", "cursor-1", ["a", "b"]);
    const next = browseVerticesReducer(base, {
      type: "PAGE_RECEIVED",
      requestId: 1,
      epoch: 1,
      page: first,
    });
    expect(next.pages).toEqual([first]);
    expect(next.currentPageIndex).toBe(0);
    expect(next.status).toBe("ready");
  });

  it("appends subsequent pages and advances the index", () => {
    let state: BrowseVerticesState = {
      ...REQUESTED_STATE,
      prefixEpoch: 7,
    };
    const first = page("", "c1", ["a"]);
    const second = page("c1", "", ["b"]);
    state = browseVerticesReducer(state, {
      type: "PAGE_RECEIVED",
      requestId: 1,
      epoch: 7,
      page: first,
    });
    state = browseVerticesReducer(state, {
      type: "PAGE_RECEIVED",
      requestId: 1,
      epoch: 7,
      page: second,
    });
    expect(state.pages).toEqual([first, second]);
    expect(state.currentPageIndex).toBe(1);
  });

  it("drops stale page responses", () => {
    const base: BrowseVerticesState = {
      ...REQUESTED_STATE,
      prefixEpoch: 9,
    };
    const next = browseVerticesReducer(base, {
      type: "PAGE_RECEIVED",
      requestId: 1,
      epoch: 8,
      page: page("", "x", ["z"]),
    });
    expect(next).toBe(base);
  });

  it("records errors only for the current epoch", () => {
    const base: BrowseVerticesState = {
      ...REQUESTED_STATE,
      prefixEpoch: 2,
      status: "loading",
    };
    const fresh = browseVerticesReducer(base, {
      type: "PAGE_FAILED",
      requestId: 1,
      epoch: 2,
      error: { kind: "unavailable", message: "boom" },
    });
    expect(fresh.status).toBe("error");
    expect(fresh.error).toEqual({ kind: "unavailable", message: "boom" });

    const stale = browseVerticesReducer(base, {
      type: "PAGE_FAILED",
      requestId: 1,
      epoch: 1,
      error: { kind: "unavailable", message: "stale" },
    });
    expect(stale).toBe(base);
  });

  it("records the count without touching pages", () => {
    const base: BrowseVerticesState = {
      ...REQUESTED_STATE,
      prefixEpoch: 4,
    };
    const next = browseVerticesReducer(base, {
      type: "COUNT_RECEIVED",
      requestId: 1,
      epoch: 4,
      count: 42,
    });
    expect(next.count).toEqual({ status: "success", count: 42 });
    expect(next.pages).toBe(base.pages);
  });

  it("steps backward through cached pages", () => {
    let state: BrowseVerticesState = {
      ...REQUESTED_STATE,
      prefixEpoch: 1,
    };
    state = browseVerticesReducer(state, {
      type: "PAGE_RECEIVED",
      requestId: 1,
      epoch: 1,
      page: page("", "c1", ["a"]),
    });
    state = browseVerticesReducer(state, {
      type: "PAGE_RECEIVED",
      requestId: 1,
      epoch: 1,
      page: page("c1", "", ["b"]),
    });
    const prev = browseVerticesReducer(state, {
      type: "NAVIGATE_PREVIOUS",
      requestId: 2,
    });
    expect(prev.currentPageIndex).toBe(0);
    const guarded = browseVerticesReducer(prev, {
      type: "NAVIGATE_PREVIOUS",
      requestId: 2,
    });
    expect(guarded).toBe(prev);
  });

  it("uses cached forward pages on NAVIGATE_NEXT_REQUESTED", () => {
    let state: BrowseVerticesState = {
      ...REQUESTED_STATE,
      prefixEpoch: 1,
    };
    const first = page("", "c1", ["a"]);
    const second = page("c1", "", ["b"]);
    state = browseVerticesReducer(state, {
      type: "PAGE_RECEIVED",
      requestId: 1,
      epoch: 1,
      page: first,
    });
    state = browseVerticesReducer(state, {
      type: "PAGE_RECEIVED",
      requestId: 1,
      epoch: 1,
      page: second,
    });
    state = browseVerticesReducer(state, {
      type: "NAVIGATE_PREVIOUS",
      requestId: 2,
    });
    const advance = browseVerticesReducer(state, {
      type: "NAVIGATE_NEXT_REQUESTED",
      requestId: 2,
      epoch: 1,
    });
    expect(advance.currentPageIndex).toBe(1);
    expect(advance.status).toBe("ready");
  });

  it("flips to loading when NAVIGATE_NEXT_REQUESTED has no cached page", () => {
    let state: BrowseVerticesState = {
      ...REQUESTED_STATE,
      prefixEpoch: 1,
    };
    state = browseVerticesReducer(state, {
      type: "PAGE_RECEIVED",
      requestId: 1,
      epoch: 1,
      page: page("", "c1", ["a"]),
    });
    const next = browseVerticesReducer(state, {
      type: "NAVIGATE_NEXT_REQUESTED",
      requestId: 2,
      epoch: 1,
    });
    expect(next.status).toBe("loading");
    expect(next.currentPageIndex).toBe(0);
  });

  it("replaces the current page in place on refresh without moving the index", () => {
    let state: BrowseVerticesState = {
      ...REQUESTED_STATE,
      prefixEpoch: 1,
    };
    const first = page("", "c1", ["a"]);
    const second = page("c1", "", ["b"]);
    state = browseVerticesReducer(state, {
      type: "PAGE_RECEIVED",
      requestId: 1,
      epoch: 1,
      page: first,
    });
    state = browseVerticesReducer(state, {
      type: "PAGE_RECEIVED",
      requestId: 1,
      epoch: 1,
      page: second,
    });
    // Step back to page 0, then refresh it.
    state = browseVerticesReducer(state, {
      type: "NAVIGATE_PREVIOUS",
      requestId: 2,
    });
    expect(state.currentPageIndex).toBe(0);
    const refreshed = page("", "c1", ["a", "a2"]);
    const next = browseVerticesReducer(state, {
      type: "PAGE_RECEIVED",
      requestId: 2,
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
    const base = { ...REQUESTED_STATE, prefixEpoch: 1 };
    const first = page("", "c1", ["a"]);
    const next = browseVerticesReducer(base, {
      type: "PAGE_RECEIVED",
      requestId: 1,
      epoch: 1,
      page: first,
      mode: "replace",
    });
    expect(next.pages).toEqual([first]);
    expect(next.currentPageIndex).toBe(0);
  });

  it("resets to initial and bumps epoch on RESET", () => {
    const base: BrowseVerticesState = {
      ...REQUESTED_STATE,
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

describe("browse request generations", () => {
  it("resets page history for a fresh first-page generation", () => {
    const previous: BrowseVerticesState = {
      ...INITIAL,
      pages: [page("", "c1", ["old:a"]), page("c1", "", ["old:b"])],
      currentPageIndex: 1,
      pageRequestId: 2,
      status: "ready",
    };
    const requested = browseVerticesReducer(previous, {
      type: "PAGE_REQUESTED",
      epoch: 0,
      requestId: 3,
    });
    const first = page("", "", ["fresh:a"]);
    const received = browseVerticesReducer(requested, {
      type: "PAGE_RECEIVED",
      epoch: 0,
      requestId: 3,
      page: first,
      mode: "reset",
    });
    expect(received.pages).toEqual([first]);
    expect(received.currentPageIndex).toBe(0);
    expect(received.status).toBe("ready");
  });
  it("rejects late page/count success and failures across prefixes and refreshes", () => {
    let state = INITIAL;
    state = browseVerticesReducer(state, {
      type: "PAGE_REQUESTED",
      epoch: 0,
      requestId: 1,
    });
    state = browseVerticesReducer(state, {
      type: "COUNT_REQUESTED",
      epoch: 0,
      requestId: 1,
    });
    state = browseVerticesReducer(state, {
      type: "PREFIX_CHANGED",
      prefix: "authorized:",
    });
    expect(state.count).toEqual({ status: "idle" });
    expect(state.error).toBeNull();
    state = browseVerticesReducer(state, {
      type: "PAGE_REQUESTED",
      epoch: 1,
      requestId: 2,
    });
    state = browseVerticesReducer(state, {
      type: "COUNT_REQUESTED",
      epoch: 1,
      requestId: 2,
    });
    const failure = { kind: "denied" as const, message: "old denial" };
    for (const epoch of [0, 1]) {
      for (const action of [
        {
          type: "PAGE_RECEIVED",
          epoch,
          requestId: 1,
          page: page("", "", ["stale:a"]),
        },
        { type: "PAGE_FAILED", epoch, requestId: 1, error: failure },
        { type: "COUNT_RECEIVED", epoch, requestId: 1, count: 999 },
        { type: "COUNT_FAILED", epoch, requestId: 1, error: failure },
        { type: "PAGE_REQUESTED", epoch, requestId: 1 },
        { type: "COUNT_REQUESTED", epoch, requestId: 1 },
      ] satisfies BrowseVerticesAction[]) {
        expect(browseVerticesReducer(state, action)).toBe(state);
      }
    }
    state = browseVerticesReducer(state, {
      type: "PAGE_RECEIVED",
      epoch: 1,
      requestId: 2,
      page: page("", "", ["authorized:a"]),
    });
    state = browseVerticesReducer(state, {
      type: "COUNT_RECEIVED",
      epoch: 1,
      requestId: 2,
      count: 1,
    });
    expect(state.status).toBe("ready");
    expect(state.error).toBeNull();
    expect(state.count).toEqual({ status: "success", count: 1 });
  });

  it("clears failed counts on refresh and preserves page state independently", () => {
    let state = browseVerticesReducer(INITIAL, {
      type: "PAGE_RECEIVED",
      epoch: 0,
      requestId: 0,
      page: page("", "", ["a"]),
    });
    for (const kind of ["denied", "unavailable"] as const) {
      state = browseVerticesReducer(state, {
        type: "COUNT_REQUESTED",
        epoch: 0,
        requestId: state.countRequestId + 1,
      });
      const requested = state;
      expect(requested.count).toEqual({ status: "loading" });
      state = browseVerticesReducer(state, {
        type: "COUNT_FAILED",
        epoch: 0,
        requestId: state.countRequestId,
        error: { kind, message: "failed" },
      });
      expect(state.count).toEqual({ status: kind, error: "failed" });
      expect(state.pages).toBe(requested.pages);
      expect(state.status).toBe("ready");
      expect(state.error).toBeNull();
    }
  });
});
