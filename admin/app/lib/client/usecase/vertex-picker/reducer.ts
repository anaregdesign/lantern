import { INITIAL_VERTEX_PICKER_STATE, type VertexPickerState } from "./state";

export type VertexPickerAction =
  | { type: "PREFIX_CHANGED"; prefix: string }
  | { type: "SCAN_REQUESTED"; epoch: number }
  | { type: "SCAN_RECEIVED"; epoch: number; suggestions: string[] }
  | { type: "SCAN_FAILED"; epoch: number; error: string }
  | { type: "COUNT_RECEIVED"; epoch: number; count: number };

/**
 * Pure reducer for the vertex picker. The `epoch` guards make stale
 * responses inert: any SCAN_* / COUNT_RECEIVED carrying an epoch other
 * than the live `prefixEpoch` is dropped, so a slow reply from an
 * abandoned prefix cannot overwrite fresher results even if its
 * AbortController loses the race. This is the unit-testable heart of the
 * debounce-and-cancel behaviour required by #457.
 */
export function vertexPickerReducer(
  state: VertexPickerState,
  action: VertexPickerAction,
): VertexPickerState {
  switch (action.type) {
    case "PREFIX_CHANGED": {
      if (action.prefix === state.prefix) {
        return state;
      }
      const prefixEpoch = state.prefixEpoch + 1;
      if (action.prefix.length === 0) {
        // Empty prefix shows no suggestions and issues no request, but the
        // epoch still advances so any in-flight reply is discarded.
        return { ...INITIAL_VERTEX_PICKER_STATE, prefixEpoch };
      }
      return {
        ...state,
        prefix: action.prefix,
        prefixEpoch,
        status: "idle",
        suggestions: [],
        matchCount: null,
        error: null,
      };
    }
    case "SCAN_REQUESTED": {
      if (action.epoch !== state.prefixEpoch) {
        return state;
      }
      return { ...state, status: "loading", error: null };
    }
    case "SCAN_RECEIVED": {
      if (action.epoch !== state.prefixEpoch) {
        return state;
      }
      return { ...state, status: "ready", suggestions: action.suggestions };
    }
    case "SCAN_FAILED": {
      if (action.epoch !== state.prefixEpoch) {
        return state;
      }
      return {
        ...state,
        status: "error",
        error: action.error,
        suggestions: [],
      };
    }
    case "COUNT_RECEIVED": {
      if (action.epoch !== state.prefixEpoch) {
        return state;
      }
      return { ...state, matchCount: action.count };
    }
    default: {
      return state;
    }
  }
}
