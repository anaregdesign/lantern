import { useCallback, useEffect, useMemo, useReducer, useRef } from "react";
import { useLanternClient } from "~/lib/client/infrastructure/api/use-lantern-client";
import { fetchSearchResults } from "./handlers";
import { searchVerticesReducer, type SearchVerticesAction } from "./reducer";
import {
  INITIAL_SEARCH_VERTICES_STATE,
  type SearchMatchMode,
  type SearchVerticesState,
} from "./state";

/**
 * Debounce window for query typing, in milliseconds. Matches Browse
 * Vertices (200 ms): content search is a heavier server operation than a
 * prefix scan, so a slightly wider window than the foreground picker
 * (150 ms) keeps a fast typist from firing a request per keystroke.
 */
export const SEARCH_DEBOUNCE_MS = 200;

/**
 * Cap on the number of ranked hits requested per query. Kept modest so the
 * single hydration batch stays small and the results list stays scannable.
 */
export const DEFAULT_SEARCH_LIMIT = 25;

export interface UseSearchVerticesOptions {
  limit?: number;
  /** Optional key-prefix filter applied to the ranked hits server-side. */
  prefix?: string;
  debounceMs?: number;
  /** Word combination: "any" (OR, default), "all" (AND), or "min-should". */
  matchMode?: SearchMatchMode;
  /** Require the query's words to occur adjacently, in order. */
  phrase?: boolean;
  /** Tolerate typos and match word prefixes. */
  fuzzy?: boolean;
}

export interface UseSearchVerticesResult {
  state: SearchVerticesState;
  /** Re-runs the current query (e.g. after the operator enables the index). */
  retry: () => void;
}

/**
 * Wires the pure search reducer + handlers to the live Lantern client.
 * Owns a single AbortController per query epoch so a fresh keystroke
 * cancels the previous search and hydration immediately; the reducer's
 * epoch guards make any reply that still lands inert.
 */
export function useSearchVertices(
  rawQuery: string,
  options: UseSearchVerticesOptions = {},
): UseSearchVerticesResult {
  const limit = options.limit ?? DEFAULT_SEARCH_LIMIT;
  const prefix = options.prefix;
  const debounceMs = options.debounceMs ?? SEARCH_DEBOUNCE_MS;
  const matchMode = options.matchMode ?? "any";
  const phrase = options.phrase ?? false;
  const fuzzy = options.fuzzy ?? false;
  const client = useLanternClient();
  const [state, dispatch] = useReducer(
    searchVerticesReducer,
    INITIAL_SEARCH_VERTICES_STATE,
  );

  // Debounce the live query into the reducer.
  useEffect(() => {
    const handle = window.setTimeout(() => {
      dispatch({ type: "QUERY_CHANGED", query: rawQuery });
    }, debounceMs);
    return () => window.clearTimeout(handle);
  }, [rawQuery, debounceMs]);

  // Fold option changes into the reducer immediately: toggling a control is
  // a deliberate act, not a per-keystroke storm, so it needs no debounce.
  // The reducer bumps the epoch, which re-runs the live query below.
  useEffect(() => {
    dispatch({
      type: "OPTIONS_CHANGED",
      options: { matchMode, phrase, fuzzy },
    });
  }, [matchMode, phrase, fuzzy]);

  // On every query change, search + hydrate under a fresh AbortController.
  const lastEpochRef = useRef<number>(-1);
  useEffect(() => {
    if (state.queryEpoch === lastEpochRef.current) {
      return;
    }
    lastEpochRef.current = state.queryEpoch;
    if (state.query.length === 0) {
      // Empty query: nothing to fetch (the reducer already cleared state).
      return;
    }
    const controller = new AbortController();
    void fetchSearchResults(
      {
        client,
        query: state.query,
        limit,
        prefix,
        options: state.options,
        epoch: state.queryEpoch,
        signal: controller.signal,
      },
      dispatch as (action: SearchVerticesAction) => void,
    );
    return () => controller.abort();
  }, [client, state.query, state.queryEpoch, state.options, limit, prefix]);

  const retry = useCallback(() => {
    if (state.query.length === 0) {
      return;
    }
    // Re-run under the live epoch so the reducer accepts the response; the
    // detached controller mirrors Browse Vertices' manual refresh.
    void fetchSearchResults(
      {
        client,
        query: state.query,
        limit,
        prefix,
        options: state.options,
        epoch: state.queryEpoch,
      },
      dispatch as (action: SearchVerticesAction) => void,
    );
  }, [client, state.query, state.queryEpoch, state.options, limit, prefix]);

  return useMemo<UseSearchVerticesResult>(
    () => ({ state, retry }),
    [state, retry],
  );
}
