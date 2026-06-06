import type { Edge } from "~/lib/client/infrastructure/api/scan-edges";
import type { BrowseEdgesState } from "./state";

export function selectCurrentPage(state: BrowseEdgesState) {
  if (state.currentPageIndex < 0) {
    return null;
  }
  return state.pages[state.currentPageIndex] ?? null;
}

export function selectVisibleEdges(state: BrowseEdgesState): Edge[] {
  return selectCurrentPage(state)?.edges ?? [];
}

export function selectCanGoPrevious(state: BrowseEdgesState): boolean {
  return state.currentPageIndex > 0;
}

export function selectCanGoNext(state: BrowseEdgesState): boolean {
  const current = selectCurrentPage(state);
  if (!current) {
    return false;
  }
  if (state.currentPageIndex + 1 < state.pages.length) {
    return true;
  }
  return current.nextCursor !== "";
}

export function selectPageNumber(state: BrowseEdgesState): number {
  return state.currentPageIndex + 1;
}
