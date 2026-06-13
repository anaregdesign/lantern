import {
  type MetricsState,
  type PanelSeries,
  type PanelState,
  initialMetricsState,
} from "./state";
import type { AggMode } from "./mode";
import type { RangeKey } from "./range";

/**
 * MetricsAction is the union of every transition the metrics reducer
 * understands. Per-panel LOADED/ERROR actions carry the epoch they were
 * dispatched under so stale in-flight results are dropped.
 */
export type MetricsAction =
  | { type: "FETCH_STARTED"; epoch: number }
  | { type: "PANEL_LOADED"; epoch: number; id: string; series: PanelSeries[] }
  | { type: "PANEL_ERROR"; epoch: number; id: string; error: string }
  | { type: "SET_RANGE"; range: RangeKey }
  | { type: "SET_MODE"; mode: AggMode }
  | { type: "RESET" };

/**
 * metricsReducer applies a MetricsAction. fetchEpoch gating: panel
 * handlers dispatch with the epoch observed when their round began. A
 * newer epoch means a fresh round (manual refresh, range change, or
 * Prometheus-URL change) started while the previous fetch was in flight;
 * the stale result is discarded so a panel never reverts to old data.
 */
export function metricsReducer(
  state: MetricsState,
  action: MetricsAction,
): MetricsState {
  switch (action.type) {
    case "FETCH_STARTED": {
      // Each panel that has never loaded flips to "loading" on its first
      // fetch; panels already showing data stay put to avoid flashing.
      const panels: Record<string, PanelState> = {};
      for (const [id, panel] of Object.entries(state.panels)) {
        panels[id] =
          panel.status === "idle" ? { ...panel, status: "loading" } : panel;
      }
      return { ...state, fetchEpoch: action.epoch, panels };
    }
    case "PANEL_LOADED": {
      if (action.epoch !== state.fetchEpoch) return state;
      const existing = state.panels[action.id];
      if (!existing) return state;
      return {
        ...state,
        panels: {
          ...state.panels,
          [action.id]: { status: "ready", series: action.series, error: null },
        },
      };
    }
    case "PANEL_ERROR": {
      if (action.epoch !== state.fetchEpoch) return state;
      const existing = state.panels[action.id];
      if (!existing) return state;
      return {
        ...state,
        panels: {
          ...state.panels,
          [action.id]: { ...existing, status: "error", error: action.error },
        },
      };
    }
    case "SET_RANGE": {
      if (action.range === state.range) return state;
      // Reset panels to idle so the new range shows a loading state rather
      // than briefly charting the previous window's data on a new axis.
      return initialMetricsState(action.range, state.aggMode);
    }
    case "SET_MODE": {
      if (action.mode === state.aggMode) return state;
      // The aggregation mode changes the query shape (per-replica vs
      // cluster-sum), so reset panels to idle and let the next round fetch
      // the new series rather than charting incompatible data.
      return initialMetricsState(state.range, action.mode);
    }
    case "RESET":
      return initialMetricsState(state.range, state.aggMode);
  }
}
