import { describe, expect, it } from "bun:test";
import {
  selectCanGoNext,
  selectCanGoPrevious,
  selectCurrentPage,
  selectPageNumber,
  selectVisibleVertices,
} from "./selectors";
import {
  INITIAL_BROWSE_VERTICES_STATE,
  type BrowseVerticesState,
  type VertexPage,
} from "./state";

function stateWithPages(
  pages: VertexPage[],
  currentPageIndex: number,
): BrowseVerticesState {
  return {
    ...INITIAL_BROWSE_VERTICES_STATE,
    pages,
    currentPageIndex,
    status: "ready",
  };
}

const PAGE_A: VertexPage = {
  startCursor: "",
  nextCursor: "c1",
  vertices: [{ key: "a" }],
};
const PAGE_B: VertexPage = {
  startCursor: "c1",
  nextCursor: "",
  vertices: [{ key: "b" }],
};

describe("browse-vertices selectors", () => {
  it("returns null current page on initial state", () => {
    expect(selectCurrentPage(INITIAL_BROWSE_VERTICES_STATE)).toBeNull();
    expect(selectVisibleVertices(INITIAL_BROWSE_VERTICES_STATE)).toEqual([]);
    expect(selectCanGoNext(INITIAL_BROWSE_VERTICES_STATE)).toBe(false);
    expect(selectCanGoPrevious(INITIAL_BROWSE_VERTICES_STATE)).toBe(false);
    expect(selectPageNumber(INITIAL_BROWSE_VERTICES_STATE)).toBe(0);
  });

  it("returns the current page and its vertices", () => {
    const state = stateWithPages([PAGE_A, PAGE_B], 0);
    expect(selectCurrentPage(state)).toBe(PAGE_A);
    expect(selectVisibleVertices(state)).toEqual([{ key: "a" }]);
    expect(selectPageNumber(state)).toBe(1);
  });

  it("allows Next when a cached forward page exists", () => {
    const state = stateWithPages([PAGE_A, PAGE_B], 0);
    expect(selectCanGoNext(state)).toBe(true);
    expect(selectCanGoPrevious(state)).toBe(false);
  });

  it("allows Next when the current page has a nextCursor", () => {
    const state = stateWithPages([PAGE_A], 0);
    expect(selectCanGoNext(state)).toBe(true);
  });

  it("disables Next at the tail of the cursor stream", () => {
    const state = stateWithPages([PAGE_A, PAGE_B], 1);
    expect(selectCanGoNext(state)).toBe(false);
    expect(selectCanGoPrevious(state)).toBe(true);
  });
});
