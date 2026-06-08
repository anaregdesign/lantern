import { useCallback, useEffect, useMemo, useReducer, useRef } from "react";
import { useLanternClient } from "~/lib/client/infrastructure/api/use-lantern-client";
import { runExpansion } from "./handlers";
import { illuminateReducer, type IlluminateAction } from "./reducer";
import {
  selectCanClear,
  selectExpansionCount,
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
  canClear: boolean;
  expansionCount: number;
  /** Fire an Illuminate from `origin` and merge the response into the accumulator. */
  expand: (origin: string) => void;
  /** Empty accumulator + expansions; preserves `initialSeed` so the seed expansion re-fires. */
  clear: () => void;
  setControls: (next: IlluminateControls) => void;
  /** Re-fire the most recent expansion's origin with current controls. */
  refresh: () => void;
}

/**
 * React-facing hook that wires the pure reducer + handlers into the live
 * Lantern client. Each expansion gets its own `AbortController` so later
 * clicks do NOT cancel earlier in-flight requests — they merge into the
 * accumulator as they return (#466).
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

  // Map of in-flight expansion id → AbortController. Lives in a ref so
  // re-renders don't reset it. `clear()` aborts every entry; unmount
  // aborts every entry; per-expansion settle removes its entry.
  const controllersRef = useRef<Map<number, AbortController>>(new Map());
  // Monotonic id source for the expansion id used to thread
  // request ↔ controller ↔ reducer action.
  const nextExpansionIdRef = useRef<number>(1);
  // Latest state for `refresh()`/`clear()` to read without re-binding.
  const stateRef = useRef(state);
  useEffect(() => {
    stateRef.current = state;
  }, [state]);
  // Latest client so the imperative helpers can use it without becoming
  // unstable on hot-reload.
  const clientRef = useRef(client);
  useEffect(() => {
    clientRef.current = client;
  }, [client]);

  const launchExpansion = useCallback((origin: string) => {
    if (origin === "") return;
    const id = nextExpansionIdRef.current++;
    const controller = new AbortController();
    controllersRef.current.set(id, controller);
    const controls = stateRef.current.controls;
    const startedAtMs =
      typeof performance !== "undefined" ? performance.now() : Date.now();
    void runExpansion(
      {
        client: clientRef.current,
        expansionId: id,
        origin,
        controls,
        startedAtMs,
        signal: controller.signal,
      },
      dispatch as (action: IlluminateAction) => void,
    ).finally(() => {
      controllersRef.current.delete(id);
    });
  }, []);

  const abortAll = useCallback(() => {
    for (const ctrl of controllersRef.current.values()) {
      ctrl.abort();
    }
    controllersRef.current.clear();
  }, []);

  // Sync URL seed into reducer. URL is the source of truth for the
  // initial seed. When the URL changes (either user navigation or our
  // own pushState), abort in-flight expansions, reset the accumulator,
  // and kick off the seed expansion.
  const lastSeedRef = useRef<string | null>(null);
  useEffect(() => {
    const normalised = urlSeed === "" ? null : urlSeed;
    if (normalised === lastSeedRef.current) return;
    lastSeedRef.current = normalised;
    abortAll();
    dispatch({ type: "INITIAL_SEED_CHANGED", seed: normalised });
    if (normalised !== null) {
      launchExpansion(normalised);
    }
  }, [urlSeed, abortAll, launchExpansion]);

  // Abort all in-flight expansions on unmount so the responses can't
  // race a remount.
  useEffect(() => {
    return () => {
      abortAll();
    };
  }, [abortAll]);

  const expand = useCallback(
    (origin: string) => {
      launchExpansion(origin);
    },
    [launchExpansion],
  );

  const clear = useCallback(() => {
    abortAll();
    dispatch({ type: "CLEARED" });
    const seed = stateRef.current.initialSeed;
    if (seed !== null) {
      launchExpansion(seed);
    }
  }, [abortAll, launchExpansion]);

  const refresh = useCallback(() => {
    const expansions = stateRef.current.expansions;
    const last = expansions[expansions.length - 1];
    if (last) {
      launchExpansion(last.origin);
      return;
    }
    const seed = stateRef.current.initialSeed;
    if (seed !== null) {
      launchExpansion(seed);
    }
  }, [launchExpansion]);

  const setControls = useCallback((next: IlluminateControls) => {
    dispatch({ type: "CONTROLS_CHANGED", controls: next });
  }, []);

  const view = useMemo(() => selectGraphView(state), [state]);
  const isBusy = selectIsBusy(state);
  const canClear = selectCanClear(state);
  const expansionCount = selectExpansionCount(state);

  return useMemo<UseIlluminateResult>(
    () => ({
      state,
      view,
      isBusy,
      canClear,
      expansionCount,
      expand,
      clear,
      setControls,
      refresh,
    }),
    [
      state,
      view,
      isBusy,
      canClear,
      expansionCount,
      expand,
      clear,
      setControls,
      refresh,
    ],
  );
}
