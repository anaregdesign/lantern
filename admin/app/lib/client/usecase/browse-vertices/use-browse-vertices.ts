import { useCallback, useEffect, useMemo, useReducer, useRef } from "react";
import { useLanternClient } from "~/lib/client/infrastructure/api/use-lantern-client";
import { browseVerticesReducer, type BrowseVerticesAction } from "./reducer";
import {
  INITIAL_BROWSE_VERTICES_STATE,
  type BrowseVerticesState,
} from "./state";
import { fetchCount, fetchPage } from "./handlers";
import {
  selectCanGoNext,
  selectCanGoPrevious,
  selectCurrentPage,
  selectPageNumber,
  selectVisibleVertices,
} from "./selectors";

/**
 * Page size for the Browse Vertices view. Falls well under the server's
 * default scan limit but large enough to be useful at-a-glance.
 */
export const DEFAULT_VERTEX_PAGE_SIZE = 50;

/**
 * Debounce window for prefix typing, in milliseconds. Tuned to feel
 * responsive while still avoiding a request per keystroke.
 */
export const PREFIX_DEBOUNCE_MS = 200;

export interface UseBrowseVerticesOptions {
  pageSize?: number;
  debounceMs?: number;
}

export interface UseBrowseVerticesResult {
  state: BrowseVerticesState;
  prefix: string;
  pageNumber: number;
  vertices: ReturnType<typeof selectVisibleVertices>;
  count: number | null;
  canGoPrevious: boolean;
  canGoNext: boolean;
  setPrefix: (next: string) => void;
  goPrevious: () => void;
  goNext: () => void;
  retry: () => void;
}

/**
 * React-facing hook that wires the pure reducer + handlers into the live
 * Lantern client. Owns its own AbortController so a fresh prefix cancels
 * any in-flight request immediately.
 */
export function useBrowseVertices(
  rawPrefix: string,
  options: UseBrowseVerticesOptions = {},
): UseBrowseVerticesResult {
  const pageSize = options.pageSize ?? DEFAULT_VERTEX_PAGE_SIZE;
  const debounceMs = options.debounceMs ?? PREFIX_DEBOUNCE_MS;
  const client = useLanternClient();
  const [state, dispatch] = useReducer(
    browseVerticesReducer,
    INITIAL_BROWSE_VERTICES_STATE,
  );

  // Debounce the user-supplied prefix into the reducer.
  useEffect(() => {
    const handle = window.setTimeout(() => {
      dispatch({ type: "PREFIX_CHANGED", prefix: rawPrefix });
    }, debounceMs);
    return () => window.clearTimeout(handle);
  }, [rawPrefix, debounceMs]);

  // On every prefix change, refetch the first page + the count.
  const lastEpochRef = useRef<number>(-1);
  useEffect(() => {
    if (state.prefixEpoch === lastEpochRef.current) {
      return;
    }
    lastEpochRef.current = state.prefixEpoch;
    const controller = new AbortController();
    void fetchPage(
      {
        client,
        prefix: state.prefix,
        cursor: "",
        pageSize,
        epoch: state.prefixEpoch,
        signal: controller.signal,
      },
      dispatch as (action: BrowseVerticesAction) => void,
    );
    void fetchCount(
      {
        client,
        prefix: state.prefix,
        epoch: state.prefixEpoch,
        signal: controller.signal,
      },
      dispatch as (action: BrowseVerticesAction) => void,
    );
    return () => controller.abort();
  }, [client, state.prefix, state.prefixEpoch, pageSize]);

  const setPrefix = useCallback((next: string) => {
    dispatch({ type: "PREFIX_CHANGED", prefix: next });
  }, []);

  const goPrevious = useCallback(() => {
    dispatch({ type: "NAVIGATE_PREVIOUS" });
  }, []);

  const goNext = useCallback(() => {
    const current = selectCurrentPage(state);
    if (!current) {
      return;
    }
    // Cached forward page: reducer handles the transition synchronously.
    if (state.currentPageIndex + 1 < state.pages.length) {
      dispatch({
        type: "NAVIGATE_NEXT_REQUESTED",
        epoch: state.prefixEpoch,
      });
      return;
    }
    if (current.nextCursor === "") {
      return;
    }
    dispatch({ type: "NAVIGATE_NEXT_REQUESTED", epoch: state.prefixEpoch });
    void fetchPage(
      {
        client,
        prefix: state.prefix,
        cursor: current.nextCursor,
        pageSize,
        epoch: state.prefixEpoch,
      },
      dispatch as (action: BrowseVerticesAction) => void,
    );
  }, [client, pageSize, state]);

  const retry = useCallback(() => {
    const current = selectCurrentPage(state);
    const cursor = current?.startCursor ?? "";
    void fetchPage(
      {
        client,
        prefix: state.prefix,
        cursor,
        pageSize,
        epoch: state.prefixEpoch,
      },
      dispatch as (action: BrowseVerticesAction) => void,
    );
  }, [client, pageSize, state]);

  return useMemo<UseBrowseVerticesResult>(
    () => ({
      state,
      prefix: state.prefix,
      pageNumber: selectPageNumber(state),
      vertices: selectVisibleVertices(state),
      count: state.count,
      canGoPrevious: selectCanGoPrevious(state),
      canGoNext: selectCanGoNext(state),
      setPrefix,
      goPrevious,
      goNext,
      retry,
    }),
    [state, setPrefix, goPrevious, goNext, retry],
  );
}
