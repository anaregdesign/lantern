import { describe, expect, it } from "bun:test";
import {
  selectCanGoNext,
  selectCanGoPrevious,
  selectCurrentPage,
  selectPageNumber,
  selectVisibleEdges,
} from "./selectors";
import {
  INITIAL_BROWSE_EDGES_STATE,
  type BrowseEdgesState,
  type EdgePage,
} from "./state";

const PAGE_A: EdgePage = {
  startCursor: "",
  nextCursor: "c1",
  edges: [{ tail: "a", head: "b", weight: 1 }],
};
const PAGE_B: EdgePage = {
  startCursor: "c1",
  nextCursor: "",
  edges: [{ tail: "a", head: "c", weight: 1 }],
};

function withPages(
  pages: EdgePage[],
  currentPageIndex: number,
): BrowseEdgesState {
  return {
    ...INITIAL_BROWSE_EDGES_STATE,
    pages,
    currentPageIndex,
    status: "ready",
  };
}

describe("browse-edges selectors", () => {
  it("treats initial state as empty / non-navigable", () => {
    expect(selectCurrentPage(INITIAL_BROWSE_EDGES_STATE)).toBeNull();
    expect(selectVisibleEdges(INITIAL_BROWSE_EDGES_STATE)).toEqual([]);
    expect(selectPageNumber(INITIAL_BROWSE_EDGES_STATE)).toBe(0);
    expect(selectCanGoNext(INITIAL_BROWSE_EDGES_STATE)).toBe(false);
    expect(selectCanGoPrevious(INITIAL_BROWSE_EDGES_STATE)).toBe(false);
  });

  it("returns the current page and edges", () => {
    const state = withPages([PAGE_A, PAGE_B], 0);
    expect(selectCurrentPage(state)).toBe(PAGE_A);
    expect(selectVisibleEdges(state)).toEqual([
      { tail: "a", head: "b", weight: 1 },
    ]);
    expect(selectPageNumber(state)).toBe(1);
  });

  it("permits Next when forward history exists", () => {
    const state = withPages([PAGE_A, PAGE_B], 0);
    expect(selectCanGoNext(state)).toBe(true);
  });

  it("permits Next when the current page has a nextCursor", () => {
    const state = withPages([PAGE_A], 0);
    expect(selectCanGoNext(state)).toBe(true);
  });

  it("disables Next at the tail of the stream", () => {
    const state = withPages([PAGE_A, PAGE_B], 1);
    expect(selectCanGoNext(state)).toBe(false);
    expect(selectCanGoPrevious(state)).toBe(true);
  });
});
