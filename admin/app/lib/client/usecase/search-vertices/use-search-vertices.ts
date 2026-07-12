import {
  useCallback,
  useLayoutEffect,
  useMemo,
  useReducer,
  useState,
} from "react";
import { useLanternClient } from "~/lib/client/infrastructure/api/use-lantern-client";
import { fetchSearchResults } from "./handlers";
import {
  browserSearchVerticesScheduler,
  createSearchVerticesDriver,
  type SearchVerticesInput,
} from "./driver";
import { searchVerticesReducer } from "./reducer";
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

  const [driver] = useState(() =>
    createSearchVerticesDriver({
      dispatch,
      run: fetchSearchResults,
      scheduler: browserSearchVerticesScheduler,
    }),
  );

  const input = useMemo<SearchVerticesInput>(
    () => ({
      client,
      query: rawQuery,
      limit,
      prefix,
      options: { matchMode, phrase, fuzzy },
    }),
    [client, rawQuery, limit, prefix, matchMode, phrase, fuzzy],
  );

  // Invalidate the old epoch during the commit that displays the new input.
  // Only starting the replacement RPC is debounced.
  useLayoutEffect(() => {
    driver.update(input, debounceMs);
    return () => driver.cancel();
  }, [driver, input, debounceMs]);

  const retry = useCallback(() => {
    driver.retry(input, state.queryEpoch);
  }, [driver, input, state.queryEpoch]);

  return useMemo<UseSearchVerticesResult>(
    () => ({ state, retry }),
    [state, retry],
  );
}
