import { describe, expect, it } from "bun:test";
import { formatResultCount, formatScore, selectCaption } from "./selectors";
import {
  INITIAL_SEARCH_VERTICES_STATE,
  type SearchResultRow,
  type SearchVerticesState,
} from "./state";

const ROW: SearchResultRow = {
  key: "doc:alpha",
  score: 1.5,
  vertex: { key: "doc:alpha", string: "alpha beta" },
};

function state(patch: Partial<SearchVerticesState>): SearchVerticesState {
  return { ...INITIAL_SEARCH_VERTICES_STATE, ...patch };
}

describe("formatResultCount", () => {
  it("singularises a single result", () => {
    expect(formatResultCount(1)).toBe("1 result");
  });

  it("pluralises zero and many results", () => {
    expect(formatResultCount(0)).toBe("0 results");
    expect(formatResultCount(42)).toBe("42 results");
  });
});

describe("formatScore", () => {
  it("renders three decimal places", () => {
    expect(formatScore(1.5)).toBe("1.500");
    expect(formatScore(0)).toBe("0.000");
    expect(formatScore(12.34567)).toBe("12.346");
  });
});

describe("selectCaption", () => {
  it("prompts the user when the query is empty", () => {
    expect(selectCaption(INITIAL_SEARCH_VERTICES_STATE)).toBe(
      "Type a query to search vertex content.",
    );
  });

  it("explains a disabled index calmly", () => {
    const caption = selectCaption(
      state({ query: "alpha", status: "disabled" }),
    );
    expect(caption).toBe("Content search is not enabled on this server.");
  });

  it("surfaces the error message on failure", () => {
    const caption = selectCaption(
      state({ query: "alpha", status: "error", error: "unavailable" }),
    );
    expect(caption).toBe("unavailable");
  });

  it("falls back to a generic message when the error is missing", () => {
    const caption = selectCaption(
      state({ query: "alpha", status: "error", error: null }),
    );
    expect(caption).toBe("Search failed.");
  });

  it("reports the result count when ready", () => {
    expect(
      selectCaption(state({ query: "alpha", status: "ready", results: [ROW] })),
    ).toBe("1 result");
  });

  it("labels a truncated bounded page as top N shown", () => {
    expect(
      selectCaption(
        state({
          query: "alpha",
          status: "ready",
          results: [ROW],
          truncated: true,
        }),
      ),
    ).toBe("Top 1 shown.");
    expect(
      selectCaption(
        state({
          query: "alpha",
          status: "ready",
          results: [ROW],
          truncated: true,
          continuationLimited: true,
        }),
      ),
    ).toContain("Server continuation limit reached");
  });

  it("reports an empty match set when ready with no results", () => {
    expect(
      selectCaption(state({ query: "nomatch", status: "ready", results: [] })),
    ).toBe("No vertices match this query.");
  });

  it("shows a settling message while loading", () => {
    expect(selectCaption(state({ query: "alpha", status: "loading" }))).toBe(
      "Searching…",
    );
  });
});
