import {
  useCallback,
  useLayoutEffect,
  useMemo,
  useReducer,
  useRef,
  useState,
} from "react";
import type { LanternClient } from "~/lib/client/infrastructure/api/lantern-client";
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
  type SearchFuzziness,
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
 * Page size for ranked hits. Kept modest so each FULL_VERTEX response stays
 * small and the results list can render incrementally.
 */
export const DEFAULT_SEARCH_LIMIT = 25;

export interface UseSearchVerticesOptions {
  limit?: number;
  /** Optional key-prefix filter applied to the ranked hits server-side. */
  prefix?: string;
  debounceMs?: number;
  /** Word combination, or "server" to preserve the server default. */
  matchMode?: SearchMatchMode;
  /** Explicit threshold used by "min-should". */
  minShouldMatch?: number;
  /** Require the query's words to occur adjacently, in order. */
  phrase?: boolean;
  /** Maximum fuzzy edit distance. */
  fuzziness?: SearchFuzziness;
  /** Match dictionary terms that extend a query word. */
  prefixTerms?: boolean;
}

export interface UseSearchVerticesResult {
  state: SearchVerticesState;
  /** Re-runs the current query (e.g. after the operator enables the index). */
  retry: () => void;
  /** Whether a retained endpoint-sticky page can be loaded. */
  canLoadMore: boolean;
  /** Appends the next retained page without hiding existing rows. */
  loadMore: () => void;
}

/**
 * Wires the pure search reducer + handlers to the live Lantern client.
 * Owns a single AbortController per query epoch so a fresh keystroke
 * cancels the previous search immediately; the reducer's
 * epoch guards make any reply that still lands inert.
 */
export function useSearchVertices(
  client: LanternClient,
  rawQuery: string,
  options: UseSearchVerticesOptions = {},
): UseSearchVerticesResult {
  const limit = options.limit ?? DEFAULT_SEARCH_LIMIT;
  const prefix = options.prefix;
  const debounceMs = options.debounceMs ?? SEARCH_DEBOUNCE_MS;
  const matchMode = options.matchMode ?? "server";
  const minShouldMatch = options.minShouldMatch ?? 2;
  const phrase = options.phrase ?? false;
  const fuzziness = options.fuzziness ?? 0;
  const prefixTerms = options.prefixTerms ?? false;
  const [state, dispatch] = useReducer(
    searchVerticesReducer,
    INITIAL_SEARCH_VERTICES_STATE,
  );
  const loadMoreController = useRef<AbortController | null>(null);

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
      options: {
        matchMode,
        minShouldMatch,
        phrase,
        fuzziness,
        prefixTerms,
      },
    }),
    [
      client,
      rawQuery,
      limit,
      prefix,
      matchMode,
      minShouldMatch,
      phrase,
      fuzziness,
      prefixTerms,
    ],
  );

  // Invalidate the old epoch during the commit that displays the new input.
  // Only starting the replacement RPC is debounced.
  useLayoutEffect(() => {
    loadMoreController.current?.abort();
    loadMoreController.current = null;
    driver.update(input, debounceMs);
    return () => {
      loadMoreController.current?.abort();
      loadMoreController.current = null;
      driver.cancel();
    };
  }, [driver, input, debounceMs]);

  const retry = useCallback(() => {
    loadMoreController.current?.abort();
    loadMoreController.current = null;
    driver.retry(input, state.queryEpoch);
  }, [driver, input, state.queryEpoch]);

  const loadMore = useCallback(() => {
    if (
      state.nextCursor === null ||
      state.loadingMore ||
      loadMoreController.current !== null
    ) {
      return;
    }
    const controller = new AbortController();
    loadMoreController.current = controller;
    void fetchSearchResults(
      {
        ...input,
        epoch: state.queryEpoch,
        cursor: state.nextCursor,
        append: true,
        signal: controller.signal,
      },
      dispatch,
    ).finally(() => {
      if (loadMoreController.current === controller) {
        loadMoreController.current = null;
      }
    });
  }, [input, state.loadingMore, state.nextCursor, state.queryEpoch]);

  const canLoadMore = state.nextCursor !== null && !state.loadingMore;

  return useMemo<UseSearchVerticesResult>(
    () => ({ state, retry, canLoadMore, loadMore }),
    [state, retry, canLoadMore, loadMore],
  );
}
