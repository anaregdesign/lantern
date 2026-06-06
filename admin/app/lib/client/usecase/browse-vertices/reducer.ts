import {
  INITIAL_BROWSE_VERTICES_STATE,
  type BrowseVerticesState,
  type VertexPage,
} from "./state";

export type BrowseVerticesAction =
  | { type: "PREFIX_CHANGED"; prefix: string }
  | { type: "PAGE_REQUESTED"; epoch: number }
  | { type: "PAGE_RECEIVED"; epoch: number; page: VertexPage }
  | { type: "PAGE_FAILED"; epoch: number; error: string }
  | { type: "COUNT_RECEIVED"; epoch: number; count: number }
  | { type: "NAVIGATE_PREVIOUS" }
  | { type: "NAVIGATE_NEXT_REQUESTED"; epoch: number }
  | { type: "RESET" };

/**
 * Pure reducer for the Browse Vertices view. Async I/O lives in
 * `handlers.ts`; this module is the single source of truth for state
 * transitions and is therefore unit-testable without touching the network.
 */
export function browseVerticesReducer(
  state: BrowseVerticesState,
  action: BrowseVerticesAction,
): BrowseVerticesState {
  switch (action.type) {
    case "PREFIX_CHANGED": {
      if (action.prefix === state.prefix) {
        return state;
      }
      return {
        ...INITIAL_BROWSE_VERTICES_STATE,
        prefix: action.prefix,
        prefixEpoch: state.prefixEpoch + 1,
      };
    }
    case "PAGE_REQUESTED": {
      if (action.epoch !== state.prefixEpoch) {
        return state;
      }
      return { ...state, status: "loading", error: null };
    }
    case "PAGE_RECEIVED": {
      if (action.epoch !== state.prefixEpoch) {
        return state;
      }
      // If this is the first page for the epoch, replace history.
      // Otherwise we append (the previous step requested NEXT).
      const isFirstPage = state.pages.length === 0;
      const pages = isFirstPage ? [action.page] : [...state.pages, action.page];
      return {
        ...state,
        pages,
        currentPageIndex: pages.length - 1,
        status: "ready",
        error: null,
      };
    }
    case "PAGE_FAILED": {
      if (action.epoch !== state.prefixEpoch) {
        return state;
      }
      return { ...state, status: "error", error: action.error };
    }
    case "COUNT_RECEIVED": {
      if (action.epoch !== state.prefixEpoch) {
        return state;
      }
      return { ...state, count: action.count };
    }
    case "NAVIGATE_PREVIOUS": {
      if (state.currentPageIndex <= 0) {
        return state;
      }
      return {
        ...state,
        currentPageIndex: state.currentPageIndex - 1,
        status: "ready",
        error: null,
      };
    }
    case "NAVIGATE_NEXT_REQUESTED": {
      if (action.epoch !== state.prefixEpoch) {
        return state;
      }
      // If we have a cached forward page, surface it immediately.
      if (state.currentPageIndex + 1 < state.pages.length) {
        return {
          ...state,
          currentPageIndex: state.currentPageIndex + 1,
          status: "ready",
          error: null,
        };
      }
      return { ...state, status: "loading", error: null };
    }
    case "RESET":
      return {
        ...INITIAL_BROWSE_VERTICES_STATE,
        prefixEpoch: state.prefixEpoch + 1,
      };
    default: {
      const exhaustive: never = action;
      return exhaustive;
    }
  }
}
