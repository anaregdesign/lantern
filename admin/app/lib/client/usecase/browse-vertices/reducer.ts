import {
  INITIAL_BROWSE_VERTICES_STATE,
  type BrowseVerticesState,
  type BrowseVerticesFailure,
  type VertexPage,
} from "./state";

export type BrowseVerticesAction =
  | { type: "PREFIX_CHANGED"; prefix: string }
  | { type: "PAGE_REQUESTED"; epoch: number; requestId: number }
  | {
      type: "PAGE_RECEIVED";
      epoch: number;
      requestId: number;
      page: VertexPage;
      /**
       * "append" (default) pushes a new page onto the history and advances the
       * index — used by goNext. "replace" overwrites the page
       * at `currentPageIndex` in place without moving the index — used by
       * Refresh/retry, which re-fetches the page already on screen. "reset"
       * starts fresh history for the first page of a prefix or client.
       */
      mode?: "append" | "replace" | "reset";
    }
  | {
      type: "PAGE_FAILED";
      epoch: number;
      requestId: number;
      error: BrowseVerticesFailure;
    }
  | { type: "COUNT_REQUESTED"; epoch: number; requestId: number }
  | { type: "COUNT_RECEIVED"; epoch: number; requestId: number; count: number }
  | {
      type: "COUNT_FAILED";
      epoch: number;
      requestId: number;
      error: BrowseVerticesFailure;
    }
  | { type: "NAVIGATE_PREVIOUS"; requestId: number }
  | { type: "NAVIGATE_NEXT_REQUESTED"; epoch: number; requestId: number }
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
      if (
        action.epoch !== state.prefixEpoch ||
        action.requestId <= state.pageRequestId
      ) {
        return state;
      }
      return {
        ...state,
        status: "loading",
        error: null,
        pageRequestId: action.requestId,
      };
    }
    case "PAGE_RECEIVED": {
      if (
        action.epoch !== state.prefixEpoch ||
        action.requestId !== state.pageRequestId
      ) {
        return state;
      }
      if (action.mode === "reset") {
        return {
          ...state,
          pages: [action.page],
          currentPageIndex: 0,
          status: "ready",
          error: null,
        };
      }
      // Refresh/retry re-fetches the page already on screen: overwrite it in
      // place and keep the index (and Previous/Next enablement) unchanged.
      if (action.mode === "replace" && state.pages.length > 0) {
        const pages = state.pages.slice();
        pages[state.currentPageIndex] = action.page;
        return { ...state, pages, status: "ready", error: null };
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
      if (
        action.epoch !== state.prefixEpoch ||
        action.requestId !== state.pageRequestId
      ) {
        return state;
      }
      return { ...state, status: "error", error: action.error };
    }
    case "COUNT_REQUESTED": {
      if (
        action.epoch !== state.prefixEpoch ||
        action.requestId <= state.countRequestId
      ) {
        return state;
      }
      return {
        ...state,
        count: { status: "loading" },
        countRequestId: action.requestId,
      };
    }
    case "COUNT_RECEIVED": {
      if (
        action.epoch !== state.prefixEpoch ||
        action.requestId !== state.countRequestId
      ) {
        return state;
      }
      return { ...state, count: { status: "success", count: action.count } };
    }
    case "COUNT_FAILED": {
      if (
        action.epoch !== state.prefixEpoch ||
        action.requestId !== state.countRequestId
      ) {
        return state;
      }
      return {
        ...state,
        count: { status: action.error.kind, error: action.error.message },
      };
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
        pageRequestId: action.requestId,
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
          pageRequestId: action.requestId,
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
