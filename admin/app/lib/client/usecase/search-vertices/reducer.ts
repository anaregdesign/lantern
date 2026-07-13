import {
  INITIAL_SEARCH_VERTICES_STATE,
  type SearchQueryOptions,
  type SearchResultRow,
  type SearchVerticesState,
} from "./state";

export type SearchVerticesAction =
  | {
      type: "INPUT_CHANGED";
      query: string;
      options: SearchQueryOptions;
      epoch: number;
    }
  | { type: "SEARCH_REQUESTED"; epoch: number }
  | {
      type: "SEARCH_RECEIVED";
      epoch: number;
      results: SearchResultRow[];
      nextCursor?: Uint8Array;
      truncated?: boolean;
      continuationLimited?: boolean;
    }
  | { type: "SEARCH_MORE_REQUESTED"; epoch: number }
  | {
      type: "SEARCH_MORE_RECEIVED";
      epoch: number;
      results: SearchResultRow[];
      nextCursor: Uint8Array;
      truncated: boolean;
      continuationLimited: boolean;
    }
  | { type: "SEARCH_MORE_FAILED"; epoch: number; error: string }
  | { type: "SEARCH_FAILED"; epoch: number; error: string }
  | { type: "SEARCH_DISABLED"; epoch: number };

/**
 * Pure reducer for vertex content-search. The `epoch` guards make stale
 * responses inert: any SEARCH_* carrying an epoch other than the live
 * `queryEpoch` is dropped, so a slow reply from an abandoned query cannot
 * overwrite fresher results even if its AbortController loses the race.
 * This is the unit-testable heart of the debounce-and-cancel behaviour
 * required by #627 and the latest-input-wins contract in #1052.
 */
export function searchVerticesReducer(
  state: SearchVerticesState,
  action: SearchVerticesAction,
): SearchVerticesState {
  switch (action.type) {
    case "INPUT_CHANGED": {
      if (action.epoch <= state.queryEpoch) {
        return state;
      }
      if (action.query.length === 0) {
        // Empty query shows no results and issues no request, but the
        // epoch still advances so any in-flight reply is discarded. The
        // operator's chosen options survive the clear.
        return {
          ...INITIAL_SEARCH_VERTICES_STATE,
          options: action.options,
          queryEpoch: action.epoch,
        };
      }
      return {
        ...state,
        query: action.query,
        queryEpoch: action.epoch,
        options: action.options,
        status: "idle",
        results: [],
        error: null,
        nextCursor: null,
        truncated: false,
        continuationLimited: false,
        loadingMore: false,
        loadMoreError: null,
      };
    }
    case "SEARCH_REQUESTED": {
      if (action.epoch !== state.queryEpoch) {
        return state;
      }
      return { ...state, status: "loading", error: null };
    }
    case "SEARCH_RECEIVED": {
      if (action.epoch !== state.queryEpoch) {
        return state;
      }
      return {
        ...state,
        status: "ready",
        results: action.results,
        error: null,
        nextCursor:
          action.nextCursor && action.nextCursor.length > 0
            ? action.nextCursor
            : null,
        truncated: action.truncated ?? false,
        continuationLimited: action.continuationLimited ?? false,
        loadingMore: false,
        loadMoreError: null,
      };
    }
    case "SEARCH_MORE_REQUESTED": {
      if (action.epoch !== state.queryEpoch || state.nextCursor === null) {
        return state;
      }
      return { ...state, loadingMore: true, loadMoreError: null };
    }
    case "SEARCH_MORE_RECEIVED": {
      if (action.epoch !== state.queryEpoch) {
        return state;
      }
      return {
        ...state,
        status: "ready",
        results: [...state.results, ...action.results],
        nextCursor: action.nextCursor.length > 0 ? action.nextCursor : null,
        truncated: action.truncated,
        continuationLimited: action.continuationLimited,
        loadingMore: false,
        loadMoreError: null,
      };
    }
    case "SEARCH_MORE_FAILED": {
      if (action.epoch !== state.queryEpoch) {
        return state;
      }
      return { ...state, loadingMore: false, loadMoreError: action.error };
    }
    case "SEARCH_FAILED": {
      if (action.epoch !== state.queryEpoch) {
        return state;
      }
      return {
        ...state,
        status: "error",
        error: action.error,
        results: [],
        nextCursor: null,
        truncated: false,
        continuationLimited: false,
        loadingMore: false,
        loadMoreError: null,
      };
    }
    case "SEARCH_DISABLED": {
      if (action.epoch !== state.queryEpoch) {
        return state;
      }
      // The server has the keyword index turned off — a calm, expected
      // outcome (opt-out), not a failure. Clear any prior error.
      return {
        ...state,
        status: "disabled",
        results: [],
        error: null,
        nextCursor: null,
        truncated: false,
        continuationLimited: false,
        loadingMore: false,
        loadMoreError: null,
      };
    }
    default: {
      return state;
    }
  }
}
