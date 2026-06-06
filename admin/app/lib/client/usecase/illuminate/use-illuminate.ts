import { useCallback, useEffect, useMemo, useReducer, useRef } from "react";
import { useLanternClient } from "~/lib/client/infrastructure/api/use-lantern-client";
import { fetchIlluminate } from "./handlers";
import { illuminateReducer, type IlluminateAction } from "./reducer";
import {
  selectCanPop,
  selectGraphView,
  selectIsBusy,
  type GraphView,
} from "./selectors";
import {
  INITIAL_ILLUMINATE_STATE,
  type IlluminateControls,
  type IlluminateState,
} from "./state";

export interface UseIlluminateResult {
  state: IlluminateState;
  view: GraphView;
  isBusy: boolean;
  canPop: boolean;
  push: (seed: string) => void;
  pop: () => void;
  setControls: (next: IlluminateControls) => void;
  refresh: () => void;
}

/**
 * React-facing hook that wires the pure reducer + handlers into the live
 * Lantern client. Owns its own AbortController so a fresh seed or knob
 * cancels any in-flight request immediately.
 *
 * `urlSeed` is the URL-decoded seed read from the `?seed=` query param.
 * Pass `""` when the route has no seed yet (the SeedPrompt UI takes over).
 */
export function useIlluminate(urlSeed: string): UseIlluminateResult {
  const client = useLanternClient();
  const [state, dispatch] = useReducer(
    illuminateReducer,
    INITIAL_ILLUMINATE_STATE,
  );

  // Sync URL seed into reducer. `SEED_CHANGED` is a no-op when the value
  // already matches, so this is safe to fire on every render.
  useEffect(() => {
    dispatch({ type: "SEED_CHANGED", seed: urlSeed });
  }, [urlSeed]);

  // Refetch whenever the epoch advances (seed or knob change). The
  // controller cancels any prior request to keep latency tight.
  const lastEpochRef = useRef<number>(-1);
  useEffect(() => {
    if (state.seed === "") {
      lastEpochRef.current = state.fetchEpoch;
      return;
    }
    if (state.fetchEpoch === lastEpochRef.current) {
      return;
    }
    lastEpochRef.current = state.fetchEpoch;
    const controller = new AbortController();
    void fetchIlluminate(
      {
        client,
        seed: state.seed,
        controls: state.controls,
        epoch: state.fetchEpoch,
        signal: controller.signal,
      },
      dispatch as (action: IlluminateAction) => void,
    );
    return () => controller.abort();
  }, [client, state.seed, state.controls, state.fetchEpoch]);

  const push = useCallback((seed: string) => {
    dispatch({ type: "SEED_PUSHED", seed });
  }, []);

  const pop = useCallback(() => {
    dispatch({ type: "SEED_POPPED" });
  }, []);

  const setControls = useCallback((next: IlluminateControls) => {
    dispatch({ type: "CONTROLS_CHANGED", controls: next });
  }, []);

  const refresh = useCallback(() => {
    dispatch({ type: "REFETCH_REQUESTED" });
  }, []);

  const view = useMemo(() => selectGraphView(state), [state]);
  const isBusy = selectIsBusy(state);
  const canPop = selectCanPop(state);

  return useMemo<UseIlluminateResult>(
    () => ({
      state,
      view,
      isBusy,
      canPop,
      push,
      pop,
      setControls,
      refresh,
    }),
    [state, view, isBusy, canPop, push, pop, setControls, refresh],
  );
}
