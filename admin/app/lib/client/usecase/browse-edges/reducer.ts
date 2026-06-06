import {
  INITIAL_BROWSE_EDGES_STATE,
  type BrowseEdgesState,
  type EdgePage,
} from "./state";

export type BrowseEdgesAction =
  | { type: "PREFIXES_CHANGED"; tailPrefix: string; headPrefix: string }
  | { type: "PAGE_REQUESTED"; epoch: number }
  | { type: "PAGE_RECEIVED"; epoch: number; page: EdgePage }
  | { type: "PAGE_FAILED"; epoch: number; error: string }
  | { type: "NAVIGATE_PREVIOUS" }
  | { type: "NAVIGATE_NEXT_REQUESTED"; epoch: number }
  | { type: "RESET" };

export function browseEdgesReducer(
  state: BrowseEdgesState,
  action: BrowseEdgesAction,
): BrowseEdgesState {
  switch (action.type) {
    case "PREFIXES_CHANGED": {
      if (
        action.tailPrefix === state.tailPrefix &&
        action.headPrefix === state.headPrefix
      ) {
        return state;
      }
      return {
        ...INITIAL_BROWSE_EDGES_STATE,
        tailPrefix: action.tailPrefix,
        headPrefix: action.headPrefix,
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
        ...INITIAL_BROWSE_EDGES_STATE,
        prefixEpoch: state.prefixEpoch + 1,
      };
    default: {
      const exhaustive: never = action;
      return exhaustive;
    }
  }
}
