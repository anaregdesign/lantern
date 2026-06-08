import { useEffect, useMemo, useReducer, useRef } from "react";
import { useLanternClient } from "~/lib/client/infrastructure/api/use-lantern-client";
import { fetchMatchCount, fetchSuggestions } from "./handlers";
import { vertexPickerReducer, type VertexPickerAction } from "./reducer";
import { INITIAL_VERTEX_PICKER_STATE, type VertexPickerState } from "./state";

/**
 * Debounce window for prefix typing, in milliseconds. Tighter than Browse
 * Vertices (200 ms) because the picker is a foreground type-and-pick
 * affordance where latency is felt keenly, yet still wide enough that a
 * fast typist never fires a request per keystroke (#457 acceptance: no
 * request sooner than 100 ms after the previous one).
 */
export const SUGGEST_DEBOUNCE_MS = 150;

/**
 * Cap on the number of suggestions requested per keystroke. Kept small so
 * the dropdown stays scannable and well under the server's scan default.
 */
export const DEFAULT_SUGGESTION_LIMIT = 20;

export interface UseVertexPickerOptions {
  limit?: number;
  debounceMs?: number;
}

export interface UseVertexPickerResult {
  state: VertexPickerState;
  suggestions: string[];
}

/**
 * Wires the pure picker reducer + handlers to the live Lantern client.
 * Owns a single AbortController per prefix epoch so a fresh keystroke
 * cancels the previous scan and count immediately; the reducer's epoch
 * guards make any reply that still lands inert.
 */
export function useVertexPicker(
  rawPrefix: string,
  options: UseVertexPickerOptions = {},
): UseVertexPickerResult {
  const limit = options.limit ?? DEFAULT_SUGGESTION_LIMIT;
  const debounceMs = options.debounceMs ?? SUGGEST_DEBOUNCE_MS;
  const client = useLanternClient();
  const [state, dispatch] = useReducer(
    vertexPickerReducer,
    INITIAL_VERTEX_PICKER_STATE,
  );

  // Debounce the live prefix into the reducer.
  useEffect(() => {
    const handle = window.setTimeout(() => {
      dispatch({ type: "PREFIX_CHANGED", prefix: rawPrefix });
    }, debounceMs);
    return () => window.clearTimeout(handle);
  }, [rawPrefix, debounceMs]);

  // On every prefix change, scan + count under a fresh AbortController.
  const lastEpochRef = useRef<number>(-1);
  useEffect(() => {
    if (state.prefixEpoch === lastEpochRef.current) {
      return;
    }
    lastEpochRef.current = state.prefixEpoch;
    if (state.prefix.length === 0) {
      // Empty prefix: nothing to fetch (the reducer already cleared state).
      return;
    }
    const controller = new AbortController();
    void fetchSuggestions(
      {
        client,
        prefix: state.prefix,
        limit,
        epoch: state.prefixEpoch,
        signal: controller.signal,
      },
      dispatch as (action: VertexPickerAction) => void,
    );
    void fetchMatchCount(
      {
        client,
        prefix: state.prefix,
        epoch: state.prefixEpoch,
        signal: controller.signal,
      },
      dispatch as (action: VertexPickerAction) => void,
    );
    return () => controller.abort();
  }, [client, state.prefix, state.prefixEpoch, limit]);

  return useMemo<UseVertexPickerResult>(
    () => ({ state, suggestions: state.suggestions }),
    [state],
  );
}
