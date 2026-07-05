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

/**
 * Total number of pages for the active prefix, derived from the cached
 * `CountVerticesByPrefix` result so the pager can read "Page N of M" instead
 * of a bare "Page N".
 *
 * Returns `null` when there is no usable total to show:
 *   - `state.count === null` — no fresh count. The reducer resets `count` to
 *     `null` on every prefix change, so a non-null count is always fresh for
 *     the prefix currently on screen; a stale total never renders while the
 *     operator retypes the prefix.
 *   - `pageSize <= 0` — guards against a divide-by-zero / negative page size.
 *
 * `count === 0` yields `1` (via the `Math.max`) so an empty prefix still reads
 * "Page 1 of 1" beside the empty table the pager is rendered next to.
 *
 * Display-only: navigability stays cursor-driven (see `selectCanGoNext`)
 * because the count is approximate under concurrent writes.
 */
export function selectTotalPages(
  state: BrowseVerticesState,
  pageSize: number,
): number | null {
  if (state.count === null || pageSize <= 0) {
    return null;
  }
  return Math.max(1, Math.ceil(state.count / pageSize));
}
