import type { MetricPoint } from "~/lib/client/infrastructure/api/prometheus-query-range";

import { METRIC_PANELS } from "./catalog";
import { DEFAULT_MODE, type AggMode } from "./mode";
import { DEFAULT_RANGE, type RangeKey } from "./range";

/**
 * MetricsStatus is the lifecycle phase of a single panel. "idle" is the
 * pre-first-fetch state; "ready" means the panel has series to render;
 * "error" surfaces the last PrometheusError message. "loading" is only
 * set on a panel's first fetch — subsequent revalidates keep the prior
 * status so charts do not flash while polling.
 */
export type MetricsStatus = "idle" | "loading" | "ready" | "error";

/**
 * One resolved time series belonging to a panel. The hook resolves each
 * Prometheus series into this shape, carrying the originating query's
 * legend template so the selector layer can compute a display label
 * without re-reading the catalog.
 */
export interface PanelSeries {
  legendTemplate?: string;
  labels: Record<string, string>;
  points: MetricPoint[];
}

export interface PanelState {
  status: MetricsStatus;
  series: PanelSeries[];
  error: string | null;
}

/**
 * MetricsState is the aggregate the Ops Metrics reducer manages. Panels
 * are independent — one panel erroring (e.g. a metric not yet emitted)
 * does not tear down the others. fetchEpoch is bumped on every refresh
 * round so stale handlers can discard their result, mirroring the Ops
 * cards reducer.
 */
export interface MetricsState {
  panels: Record<string, PanelState>;
  range: RangeKey;
  aggMode: AggMode;
  fetchEpoch: number;
}

export function initialMetricsState(
  range: RangeKey = DEFAULT_RANGE,
  aggMode: AggMode = DEFAULT_MODE,
): MetricsState {
  const panels: Record<string, PanelState> = {};
  for (const panel of METRIC_PANELS) {
    panels[panel.id] = { status: "idle", series: [], error: null };
  }
  return { panels, range, aggMode, fetchEpoch: 0 };
}

export const INITIAL_METRICS_STATE = initialMetricsState();
