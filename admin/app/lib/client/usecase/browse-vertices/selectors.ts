import type { Vertex } from "~/lib/client/infrastructure/api/scan-vertices";
import type { BrowseVerticesState } from "./state";

export function selectCurrentPage(state: BrowseVerticesState) {
  if (state.currentPageIndex < 0) {
    return null;
  }
  return state.pages[state.currentPageIndex] ?? null;
}

export function selectVisibleVertices(state: BrowseVerticesState): Vertex[] {
  return selectCurrentPage(state)?.vertices ?? [];
}

export function selectCanGoPrevious(state: BrowseVerticesState): boolean {
  return state.currentPageIndex > 0;
}

/**
 * Returns true when the user can advance one more page. The forward step is
 * allowed when either:
 *   - we already cached a subsequent page (history-forward), OR
 *   - the current page reported a non-empty `nextCursor`.
 */
export function selectCanGoNext(state: BrowseVerticesState): boolean {
  const current = selectCurrentPage(state);
  if (!current) {
    return false;
  }
  if (state.currentPageIndex + 1 < state.pages.length) {
    return true;
  }
  return current.nextCursor !== "";
}

export function selectPageNumber(state: BrowseVerticesState): number {
  return state.currentPageIndex + 1;
}
