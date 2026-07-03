import {
  INITIAL_SEARCH_VERTICES_STATE,
  type SearchQueryOptions,
  type SearchResultRow,
  type SearchVerticesState,
} from "./state";

export type SearchVerticesAction =
  | { type: "QUERY_CHANGED"; query: string }
  | { type: "OPTIONS_CHANGED"; options: SearchQueryOptions }
  | { type: "SEARCH_REQUESTED"; epoch: number }
  | { type: "SEARCH_RECEIVED"; epoch: number; results: SearchResultRow[] }
  | { type: "SEARCH_FAILED"; epoch: number; error: string }
  | { type: "SEARCH_DISABLED"; epoch: number };

/**
 * Pure reducer for vertex content-search. The `epoch` guards make stale
 * responses inert: any SEARCH_* carrying an epoch other than the live
 * `queryEpoch` is dropped, so a slow reply from an abandoned query cannot
 * overwrite fresher results even if its AbortController loses the race.
 * This is the unit-testable heart of the debounce-and-cancel behaviour
 * required by #627.
 */
export function searchVerticesReducer(
  state: SearchVerticesState,
  action: SearchVerticesAction,
): SearchVerticesState {
  switch (action.type) {
    case "QUERY_CHANGED": {
      if (action.query === state.query) {
        return state;
      }
      const queryEpoch = state.queryEpoch + 1;
      if (action.query.length === 0) {
        // Empty query shows no results and issues no request, but the
        // epoch still advances so any in-flight reply is discarded. The
        // operator's chosen options survive the clear.
        return {
          ...INITIAL_SEARCH_VERTICES_STATE,
          options: state.options,
          queryEpoch,
        };
      }
      return {
        ...state,
        query: action.query,
        queryEpoch,
        status: "idle",
        results: [],
        error: null,
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
      };
    }
    case "SEARCH_FAILED": {
      if (action.epoch !== state.queryEpoch) {
        return state;
      }
      return { ...state, status: "error", error: action.error, results: [] };
    }
    case "SEARCH_DISABLED": {
      if (action.epoch !== state.queryEpoch) {
        return state;
      }
      // The server has the keyword index turned off — a calm, expected
      // outcome (opt-out), not a failure. Clear any prior error.
      return { ...state, status: "disabled", results: [], error: null };
    }
    case "OPTIONS_CHANGED": {
      if (sameOptions(action.options, state.options)) {
        return state;
      }
      // A changed relevance control re-runs the live query under a fresh
      // epoch, exactly like a keystroke. An empty query stays inert (the
      // effect skips the fetch), but the epoch still advances so a slow
      // reply from the previous options cannot land.
      const queryEpoch = state.queryEpoch + 1;
      if (state.query.length === 0) {
        return { ...state, options: action.options, queryEpoch };
      }
      return {
        ...state,
        options: action.options,
        queryEpoch,
        status: "idle",
        results: [],
        error: null,
      };
    }
    default: {
      return state;
    }
  }
}

/** Structural equality for the relevance controls, used to drop no-op changes. */
function sameOptions(a: SearchQueryOptions, b: SearchQueryOptions): boolean {
  return (
    a.matchMode === b.matchMode && a.phrase === b.phrase && a.fuzzy === b.fuzzy
  );
}
