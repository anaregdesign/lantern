import { describe, expect, it } from "bun:test";
import { searchVerticesReducer, type SearchVerticesAction } from "./reducer";
import {
  INITIAL_SEARCH_VERTICES_STATE,
  type SearchResultRow,
  type SearchVerticesState,
} from "./state";

function reduce(
  state: SearchVerticesState,
  ...actions: SearchVerticesAction[]
): SearchVerticesState {
  return actions.reduce(searchVerticesReducer, state);
}

const ROW: SearchResultRow = {
  key: "doc:alpha",
  score: 1.5,
  vertex: { key: "doc:alpha", string: "alpha beta" },
};

describe("searchVerticesReducer", () => {
  it("bumps the epoch and clears prior results on a new query", () => {
    const next = reduce(INITIAL_SEARCH_VERTICES_STATE, {
      type: "QUERY_CHANGED",
      query: "alpha",
    });
    expect(next.query).toBe("alpha");
    expect(next.queryEpoch).toBe(1);
    expect(next.status).toBe("idle");
    expect(next.results).toEqual([]);
    expect(next.error).toBeNull();
  });

  it("ignores a QUERY_CHANGED that does not change the query", () => {
    const seeded = reduce(INITIAL_SEARCH_VERTICES_STATE, {
      type: "QUERY_CHANGED",
      query: "alpha",
    });
    const same = searchVerticesReducer(seeded, {
      type: "QUERY_CHANGED",
      query: "alpha",
    });
    // Identity preserved → no epoch bump, no re-fetch.
    expect(same).toBe(seeded);
    expect(same.queryEpoch).toBe(1);
  });

  it("resets to the empty state (but advances epoch) when the query is cleared", () => {
    const seeded = reduce(
      INITIAL_SEARCH_VERTICES_STATE,
      { type: "QUERY_CHANGED", query: "alpha" },
      { type: "SEARCH_REQUESTED", epoch: 1 },
      { type: "SEARCH_RECEIVED", epoch: 1, results: [ROW] },
    );
    const cleared = searchVerticesReducer(seeded, {
      type: "QUERY_CHANGED",
      query: "",
    });
    expect(cleared.query).toBe("");
    expect(cleared.results).toEqual([]);
    expect(cleared.status).toBe("idle");
    // Epoch still advances so any in-flight reply for "alpha" is discarded.
    expect(cleared.queryEpoch).toBe(2);
  });

  it("applies a search result that matches the live epoch", () => {
    const next = reduce(
      INITIAL_SEARCH_VERTICES_STATE,
      { type: "QUERY_CHANGED", query: "alpha" },
      { type: "SEARCH_REQUESTED", epoch: 1 },
      { type: "SEARCH_RECEIVED", epoch: 1, results: [ROW] },
    );
    expect(next.status).toBe("ready");
    expect(next.results).toEqual([ROW]);
  });

  it("treats a zero-result response as a ready, empty success", () => {
    const next = reduce(
      INITIAL_SEARCH_VERTICES_STATE,
      { type: "QUERY_CHANGED", query: "nomatch" },
      { type: "SEARCH_REQUESTED", epoch: 1 },
      { type: "SEARCH_RECEIVED", epoch: 1, results: [] },
    );
    expect(next.status).toBe("ready");
    expect(next.results).toEqual([]);
    expect(next.error).toBeNull();
  });

  it("drops a stale search response from an abandoned query (epoch guard)", () => {
    // Type "ab" (epoch 1), then "abc" (epoch 2) before "ab"'s search lands.
    const seeded = reduce(
      INITIAL_SEARCH_VERTICES_STATE,
      { type: "QUERY_CHANGED", query: "ab" },
      { type: "SEARCH_REQUESTED", epoch: 1 },
      { type: "QUERY_CHANGED", query: "abc" },
    );
    expect(seeded.queryEpoch).toBe(2);

    // The slow reply for "ab" (epoch 1) arrives — it must be ignored.
    const afterStale = searchVerticesReducer(seeded, {
      type: "SEARCH_RECEIVED",
      epoch: 1,
      results: [ROW],
    });
    expect(afterStale).toBe(seeded);
    expect(afterStale.results).toEqual([]);

    // The fresh reply for "abc" (epoch 2) is applied.
    const fresh: SearchResultRow = { key: "abc", score: 2, vertex: null };
    const afterFresh = searchVerticesReducer(afterStale, {
      type: "SEARCH_RECEIVED",
      epoch: 2,
      results: [fresh],
    });
    expect(afterFresh.results).toEqual([fresh]);
  });

  it("records a search failure and clears results on the live epoch", () => {
    const next = reduce(
      INITIAL_SEARCH_VERTICES_STATE,
      { type: "QUERY_CHANGED", query: "boom" },
      { type: "SEARCH_REQUESTED", epoch: 1 },
      { type: "SEARCH_RECEIVED", epoch: 1, results: [ROW] },
      { type: "SEARCH_FAILED", epoch: 1, error: "unavailable" },
    );
    expect(next.status).toBe("error");
    expect(next.error).toBe("unavailable");
    expect(next.results).toEqual([]);
  });

  it("enters the disabled state (no error) when the index is off", () => {
    const next = reduce(
      INITIAL_SEARCH_VERTICES_STATE,
      { type: "QUERY_CHANGED", query: "alpha" },
      { type: "SEARCH_REQUESTED", epoch: 1 },
      { type: "SEARCH_DISABLED", epoch: 1 },
    );
    expect(next.status).toBe("disabled");
    expect(next.results).toEqual([]);
    expect(next.error).toBeNull();
  });

  it("drops a stale SEARCH_DISABLED from an abandoned query", () => {
    const seeded = reduce(
      INITIAL_SEARCH_VERTICES_STATE,
      { type: "QUERY_CHANGED", query: "ab" },
      { type: "QUERY_CHANGED", query: "abc" },
    );
    const afterStale = searchVerticesReducer(seeded, {
      type: "SEARCH_DISABLED",
      epoch: 1,
    });
    expect(afterStale).toBe(seeded);
    expect(afterStale.status).toBe("idle");
  });
});
