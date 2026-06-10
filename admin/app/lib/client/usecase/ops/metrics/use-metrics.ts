import { useCallback, useEffect, useMemo, useReducer, useRef } from "react";

import {
  queryRange,
  type MetricSeries,
} from "~/lib/client/infrastructure/api/prometheus-query-range";
import {
  browserStorage,
  metricsRangeStorageKey,
} from "~/lib/client/infrastructure/browser/storage";
import { METRIC_PANELS } from "./catalog";
import { DEFAULT_RANGE, isRangeKey, type RangeKey } from "./range";
import { metricsReducer } from "./reducer";
import { rangeToWindow, rateWindow, resolveExpr } from "./selectors";
import { initialMetricsState, type PanelSeries } from "./state";

export interface UseMetricsArgs {
  /** Prometheus query base URL (same-origin proxy path or absolute URL). */
  prometheusUrl: string;
  /** Poll interval in ms; <= 0 disables the timer (single fetch on mount). */
  pollMs: number;
  /** Bumped by the Ops toolbar Refresh button to force an immediate round. */
  refreshNonce: number;
}

export interface UseMetrics {
  state: ReturnType<typeof metricsReducer>;
  range: RangeKey;
  setRange: (range: RangeKey) => void;
}

/**
 * useMetrics owns the Prometheus time-series polling for the Ops Metrics
 * section. It mirrors `useOps`: one AbortController per round, epoch-gated
 * dispatches, a polling timer cleared on unmount / dependency change. Each
 * round queries every catalog panel in parallel via `Promise.allSettled`,
 * so a single failing panel (e.g. a metric not yet emitted) never tears
 * down the others.
 *
 * The selected range is the single source of truth here (not a sibling
 * hook): it drives both the query window and the panel reset, and is
 * persisted to `localStorage` so it survives reloads. Changing the range
 * recreates `fetchRound`, which the effect observes to refetch immediately.
 */
export function useMetrics({
  prometheusUrl,
  pollMs,
  refreshNonce,
}: UseMetricsArgs): UseMetrics {
  const storage = useMemo(() => browserStorage(), []);
  const [state, dispatch] = useReducer(metricsReducer, undefined, () => {
    const stored = storage.get(metricsRangeStorageKey);
    const range = stored && isRangeKey(stored) ? stored : DEFAULT_RANGE;
    return initialMetricsState(range);
  });

  // epochRef carries the latest dispatched epoch so the polling closure
  // does not depend on state.fetchEpoch (which would reset the timer).
  const epochRef = useRef(state.fetchEpoch);
  epochRef.current = state.fetchEpoch;

  const setRange = useCallback(
    (range: RangeKey) => {
      storage.set(metricsRangeStorageKey, range);
      dispatch({ type: "SET_RANGE", range });
    },
    [storage],
  );

  const range = state.range;

  const fetchRound = useCallback(
    async (signal: AbortSignal) => {
      const epoch = epochRef.current + 1;
      epochRef.current = epoch;
      dispatch({ type: "FETCH_STARTED", epoch });

      const { rangeSeconds, stepSeconds } = rangeToWindow(range);
      const window = rateWindow(stepSeconds);
      const end = Math.floor(Date.now() / 1000);
      const start = end - rangeSeconds;

      await Promise.allSettled(
        METRIC_PANELS.map(async (panel) => {
          try {
            const resolved = await Promise.all(
              panel.queries.map((query) =>
                queryRange(prometheusUrl, {
                  query: resolveExpr(query.expr, window),
                  start,
                  end,
                  step: stepSeconds,
                  signal,
                }).then((series: MetricSeries[]) => ({ query, series })),
              ),
            );
            const panelSeries: PanelSeries[] = resolved.flatMap(
              ({ query, series }) =>
                series.map((s) => ({
                  legendTemplate: query.legend,
                  labels: s.labels,
                  points: s.points,
                })),
            );
            dispatch({
              type: "PANEL_LOADED",
              epoch,
              id: panel.id,
              series: panelSeries,
            });
          } catch (err: unknown) {
            if ((err as Error)?.name === "AbortError") return;
            dispatch({
              type: "PANEL_ERROR",
              epoch,
              id: panel.id,
              error: errorMessage(err),
            });
          }
        }),
      );
    },
    [prometheusUrl, range],
  );

  // First-paint fetch + polling timer. refreshNonce in the dependency
  // list lets the toolbar force a fresh round without changing pollMs.
  useEffect(() => {
    const ctl = new AbortController();
    void fetchRound(ctl.signal);
    if (pollMs <= 0) {
      return () => ctl.abort();
    }
    const id = window.setInterval(() => {
      void fetchRound(ctl.signal);
    }, pollMs);
    return () => {
      window.clearInterval(id);
      ctl.abort();
    };
  }, [fetchRound, pollMs, refreshNonce]);

  return { state, range, setRange };
}

function errorMessage(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}
