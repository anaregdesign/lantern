import { useCallback, useEffect, useMemo, useReducer, useRef } from "react";
import { useLanternClient } from "~/lib/client/infrastructure/api/use-lantern-client";
import { browseEdgesReducer, type BrowseEdgesAction } from "./reducer";
import { INITIAL_BROWSE_EDGES_STATE, type BrowseEdgesState } from "./state";
import { fetchEdgePage } from "./handlers";
import {
  selectCanGoNext,
  selectCanGoPrevious,
  selectCurrentPage,
  selectPageNumber,
  selectVisibleEdges,
} from "./selectors";

export const DEFAULT_EDGE_PAGE_SIZE = 50;
export const PREFIX_DEBOUNCE_MS = 200;

export interface UseBrowseEdgesOptions {
  pageSize?: number;
  debounceMs?: number;
}

export interface UseBrowseEdgesResult {
  state: BrowseEdgesState;
  tailPrefix: string;
  headPrefix: string;
  pageNumber: number;
  edges: ReturnType<typeof selectVisibleEdges>;
  canGoPrevious: boolean;
  canGoNext: boolean;
  setTailPrefix: (next: string) => void;
  setHeadPrefix: (next: string) => void;
  goPrevious: () => void;
  goNext: () => void;
  retry: () => void;
}

export function useBrowseEdges(
  rawTailPrefix: string,
  rawHeadPrefix: string,
  options: UseBrowseEdgesOptions = {},
): UseBrowseEdgesResult {
  const pageSize = options.pageSize ?? DEFAULT_EDGE_PAGE_SIZE;
  const debounceMs = options.debounceMs ?? PREFIX_DEBOUNCE_MS;
  const client = useLanternClient();
  const [state, dispatch] = useReducer(
    browseEdgesReducer,
    INITIAL_BROWSE_EDGES_STATE,
  );

  useEffect(() => {
    const handle = window.setTimeout(() => {
      dispatch({
        type: "PREFIXES_CHANGED",
        tailPrefix: rawTailPrefix,
        headPrefix: rawHeadPrefix,
      });
    }, debounceMs);
    return () => window.clearTimeout(handle);
  }, [rawTailPrefix, rawHeadPrefix, debounceMs]);

  const lastEpochRef = useRef<number>(-1);
  useEffect(() => {
    if (state.prefixEpoch === lastEpochRef.current) {
      return;
    }
    lastEpochRef.current = state.prefixEpoch;
    const controller = new AbortController();
    void fetchEdgePage(
      {
        client,
        tailPrefix: state.tailPrefix,
        headPrefix: state.headPrefix,
        cursor: "",
        pageSize,
        epoch: state.prefixEpoch,
        signal: controller.signal,
      },
      dispatch as (action: BrowseEdgesAction) => void,
    );
    return () => controller.abort();
  }, [client, state.tailPrefix, state.headPrefix, state.prefixEpoch, pageSize]);

  const setTailPrefix = useCallback(
    (next: string) => {
      dispatch({
        type: "PREFIXES_CHANGED",
        tailPrefix: next,
        headPrefix: state.headPrefix,
      });
    },
    [state.headPrefix],
  );

  const setHeadPrefix = useCallback(
    (next: string) => {
      dispatch({
        type: "PREFIXES_CHANGED",
        tailPrefix: state.tailPrefix,
        headPrefix: next,
      });
    },
    [state.tailPrefix],
  );

  const goPrevious = useCallback(() => {
    dispatch({ type: "NAVIGATE_PREVIOUS" });
  }, []);

  const goNext = useCallback(() => {
    const current = selectCurrentPage(state);
    if (!current) {
      return;
    }
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
    void fetchEdgePage(
      {
        client,
        tailPrefix: state.tailPrefix,
        headPrefix: state.headPrefix,
        cursor: current.nextCursor,
        pageSize,
        epoch: state.prefixEpoch,
      },
      dispatch as (action: BrowseEdgesAction) => void,
    );
  }, [client, pageSize, state]);

  const retry = useCallback(() => {
    const current = selectCurrentPage(state);
    const cursor = current?.startCursor ?? "";
    void fetchEdgePage(
      {
        client,
        tailPrefix: state.tailPrefix,
        headPrefix: state.headPrefix,
        cursor,
        pageSize,
        epoch: state.prefixEpoch,
        mode: "replace",
      },
      dispatch as (action: BrowseEdgesAction) => void,
    );
  }, [client, pageSize, state]);

  return useMemo<UseBrowseEdgesResult>(
    () => ({
      state,
      tailPrefix: state.tailPrefix,
      headPrefix: state.headPrefix,
      pageNumber: selectPageNumber(state),
      edges: selectVisibleEdges(state),
      canGoPrevious: selectCanGoPrevious(state),
      canGoNext: selectCanGoNext(state),
      setTailPrefix,
      setHeadPrefix,
      goPrevious,
      goNext,
      retry,
    }),
    [state, setTailPrefix, setHeadPrefix, goPrevious, goNext, retry],
  );
}
