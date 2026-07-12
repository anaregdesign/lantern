import { describe, expect, it } from "bun:test";
import { searchVerticesReducer, type SearchVerticesAction } from "./reducer";
import {
  DEFAULT_SEARCH_QUERY_OPTIONS,
  INITIAL_SEARCH_VERTICES_STATE,
  type SearchResultRow,
  type SearchVerticesState,
} from "./state";

function input(
  query: string,
  epoch: number,
  options = DEFAULT_SEARCH_QUERY_OPTIONS,
): SearchVerticesAction {
  return { type: "INPUT_CHANGED", query, epoch, options };
}

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
    const next = reduce(INITIAL_SEARCH_VERTICES_STATE, input("alpha", 1));
    expect(next.query).toBe("alpha");
    expect(next.queryEpoch).toBe(1);
    expect(next.status).toBe("idle");
    expect(next.results).toEqual([]);
    expect(next.error).toBeNull();
  });

  it("ignores an input action whose epoch is not newer", () => {
    const seeded = reduce(INITIAL_SEARCH_VERTICES_STATE, input("alpha", 1));
    const same = searchVerticesReducer(seeded, input("alpha", 1));
    // Identity preserved: an old effect cannot rewind the live lifecycle.
    expect(same).toBe(seeded);
    expect(same.queryEpoch).toBe(1);
  });

  it("resets to the empty state (but advances epoch) when the query is cleared", () => {
    const seeded = reduce(
      INITIAL_SEARCH_VERTICES_STATE,
      input("alpha", 1),
      { type: "SEARCH_REQUESTED", epoch: 1 },
      { type: "SEARCH_RECEIVED", epoch: 1, results: [ROW] },
    );
    const cleared = searchVerticesReducer(seeded, input("", 2));
    expect(cleared.query).toBe("");
    expect(cleared.results).toEqual([]);
    expect(cleared.status).toBe("idle");
    // Epoch still advances so any in-flight reply for "alpha" is discarded.
    expect(cleared.queryEpoch).toBe(2);
  });

  it("applies a search result that matches the live epoch", () => {
    const next = reduce(
      INITIAL_SEARCH_VERTICES_STATE,
      input("alpha", 1),
      { type: "SEARCH_REQUESTED", epoch: 1 },
      { type: "SEARCH_RECEIVED", epoch: 1, results: [ROW] },
    );
    expect(next.status).toBe("ready");
    expect(next.results).toEqual([ROW]);
  });

  it("treats a zero-result response as a ready, empty success", () => {
    const next = reduce(
      INITIAL_SEARCH_VERTICES_STATE,
      input("nomatch", 1),
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
      input("ab", 1),
      { type: "SEARCH_REQUESTED", epoch: 1 },
      input("abc", 2),
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
      input("boom", 1),
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
      input("alpha", 1),
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
      input("ab", 1),
      input("abc", 2),
    );
    const afterStale = searchVerticesReducer(seeded, {
      type: "SEARCH_DISABLED",
      epoch: 1,
    });
    expect(afterStale).toBe(seeded);
    expect(afterStale.status).toBe("idle");
  });

  it("bumps the epoch and re-runs the live query when an option changes", () => {
    const seeded = reduce(
      INITIAL_SEARCH_VERTICES_STATE,
      input("alpha beta", 1),
      { type: "SEARCH_REQUESTED", epoch: 1 },
      { type: "SEARCH_RECEIVED", epoch: 1, results: [ROW] },
    );
    const next = searchVerticesReducer(
      seeded,
      input("alpha beta", 2, {
        matchMode: "all",
        phrase: false,
        fuzzy: false,
      }),
    );
    expect(next.options.matchMode).toBe("all");
    // A changed control re-runs the query, exactly like a keystroke.
    expect(next.queryEpoch).toBe(2);
    expect(next.status).toBe("idle");
    expect(next.results).toEqual([]);
    expect(next.error).toBeNull();
  });

  it("advances the epoch but stays inert when options change with no query", () => {
    const next = searchVerticesReducer(
      INITIAL_SEARCH_VERTICES_STATE,
      input("", 1, { matchMode: "any", phrase: true, fuzzy: false }),
    );
    expect(next.options.phrase).toBe(true);
    // Epoch advances so a slow reply from the prior options cannot land,
    // but the empty query means the effect issues no fetch.
    expect(next.queryEpoch).toBe(1);
    expect(next.query).toBe("");
    expect(next.status).toBe("idle");
  });

  it("preserves the chosen options when the query is cleared", () => {
    const seeded = reduce(
      INITIAL_SEARCH_VERTICES_STATE,
      input("alpha", 1),
      input("alpha", 2, {
        matchMode: "all",
        phrase: true,
        fuzzy: false,
      }),
    );
    const cleared = searchVerticesReducer(
      seeded,
      input("", 3, { matchMode: "all", phrase: true, fuzzy: false }),
    );
    expect(cleared.query).toBe("");
    expect(cleared.options).toEqual({
      matchMode: "all",
      phrase: true,
      fuzzy: false,
    });
  });
});
