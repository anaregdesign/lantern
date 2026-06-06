import {
  INITIAL_ILLUMINATE_STATE,
  type IlluminateControls,
  type IlluminateFrame,
  type IlluminateState,
} from "./state";

export type IlluminateAction =
  | { type: "SEED_CHANGED"; seed: string }
  | { type: "SEED_PUSHED"; seed: string }
  | { type: "SEED_POPPED" }
  | { type: "CONTROLS_CHANGED"; controls: IlluminateControls }
  | { type: "REFETCH_REQUESTED" }
  | { type: "FETCH_REQUESTED"; epoch: number }
  | { type: "FETCH_RECEIVED"; epoch: number; frame: IlluminateFrame }
  | { type: "FETCH_FAILED"; epoch: number; error: string }
  | { type: "RESET" };

/**
 * Pure reducer for the Illuminate view. Async I/O lives in `handlers.ts`;
 * this module is the single source of truth for state transitions and is
 * therefore unit-testable without touching the network.
 */
export function illuminateReducer(
  state: IlluminateState,
  action: IlluminateAction,
): IlluminateState {
  switch (action.type) {
    case "SEED_CHANGED": {
      if (action.seed === state.seed) {
        return state;
      }
      // Replace history entirely — `SEED_CHANGED` represents a URL-level
      // navigation (e.g. arriving on the page from the Browse screen), not
      // a click inside the canvas. Use `SEED_PUSHED` for the latter.
      const history = action.seed === "" ? [] : [action.seed];
      return {
        ...state,
        seed: action.seed,
        history,
        frame: null,
        status: action.seed === "" ? "idle" : state.status,
        error: null,
        fetchEpoch: state.fetchEpoch + 1,
      };
    }
    case "SEED_PUSHED": {
      if (action.seed === "" || action.seed === state.seed) {
        return state;
      }
      return {
        ...state,
        seed: action.seed,
        history: [...state.history, action.seed],
        frame: null,
        error: null,
        fetchEpoch: state.fetchEpoch + 1,
      };
    }
    case "SEED_POPPED": {
      if (state.history.length <= 1) {
        return state;
      }
      const history = state.history.slice(0, -1);
      const seed = history[history.length - 1] ?? "";
      return {
        ...state,
        seed,
        history,
        frame: null,
        error: null,
        fetchEpoch: state.fetchEpoch + 1,
      };
    }
    case "CONTROLS_CHANGED": {
      if (controlsEqual(action.controls, state.controls)) {
        return state;
      }
      return {
        ...state,
        controls: action.controls,
        // Keep the existing frame on screen until the new one arrives so
        // the user doesn't see a flash of empty canvas while dragging a
        // slider; clear `error` because the old failure no longer applies.
        error: null,
        fetchEpoch: state.fetchEpoch + 1,
      };
    }
    case "REFETCH_REQUESTED": {
      // No-op when there's no seed to fetch — guard rail so toolbar can
      // dispatch unconditionally.
      if (state.seed === "") {
        return state;
      }
      return {
        ...state,
        error: null,
        fetchEpoch: state.fetchEpoch + 1,
      };
    }
    case "FETCH_REQUESTED": {
      if (action.epoch !== state.fetchEpoch) {
        return state;
      }
      return { ...state, status: "loading", error: null };
    }
    case "FETCH_RECEIVED": {
      if (action.epoch !== state.fetchEpoch) {
        return state;
      }
      return {
        ...state,
        frame: action.frame,
        status: "ready",
        error: null,
      };
    }
    case "FETCH_FAILED": {
      if (action.epoch !== state.fetchEpoch) {
        return state;
      }
      return { ...state, status: "error", error: action.error };
    }
    case "RESET":
      return {
        ...INITIAL_ILLUMINATE_STATE,
        fetchEpoch: state.fetchEpoch + 1,
      };
    default: {
      const exhaustive: never = action;
      return exhaustive;
    }
  }
}

function controlsEqual(a: IlluminateControls, b: IlluminateControls): boolean {
  return (
    a.step === b.step &&
    a.k === b.k &&
    a.algorithm === b.algorithm &&
    a.objective === b.objective &&
    a.weighting === b.weighting
  );
}
