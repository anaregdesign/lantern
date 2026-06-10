import type { MetricPoint } from "~/lib/client/infrastructure/api/prometheus-query-range";

import type { MetricUnit } from "./catalog";
import type { RangeKey } from "./range";
import type { PanelSeries } from "./state";

/**
 * Concrete `(rangeSeconds, stepSeconds)` window for each range key. Steps
 * are chosen to keep every range at ~60–290 points — enough resolution for
 * a compact chart without over-fetching.
 */
const RANGE_WINDOWS: Record<
  RangeKey,
  { rangeSeconds: number; stepSeconds: number }
> = {
  "15m": { rangeSeconds: 900, stepSeconds: 15 },
  "1h": { rangeSeconds: 3600, stepSeconds: 30 },
  "6h": { rangeSeconds: 21600, stepSeconds: 120 },
  "24h": { rangeSeconds: 86400, stepSeconds: 300 },
};

export function rangeToWindow(range: RangeKey): {
  rangeSeconds: number;
  stepSeconds: number;
} {
  return RANGE_WINDOWS[range];
}

/**
 * Derives the `rate()` range-vector window for a given step. Prometheus
 * needs a window covering several scrape steps for `rate()` to be stable,
 * so we use `max(4 × step, 60s)` and format it as the largest whole unit.
 */
export function rateWindow(stepSeconds: number): string {
  const seconds = Math.max(4 * stepSeconds, 60);
  return formatPromDuration(seconds);
}

function formatPromDuration(seconds: number): string {
  if (seconds % 3600 === 0) return `${seconds / 3600}h`;
  if (seconds % 60 === 0) return `${seconds / 60}m`;
  return `${seconds}s`;
}

/**
 * Substitutes the `$__rate` placeholder in a catalog expression with a
 * concrete window. Uses split/join (not RegExp) so the window string is
 * never interpreted as a pattern.
 */
export function resolveExpr(expr: string, window: string): string {
  return expr.split("$__rate").join(window);
}

/**
 * Computes a display label for one series. A legend template containing
 * `{{label}}` tokens is filled from the series' label set; a static
 * template is returned verbatim; with no template the label is derived
 * from the non-synthetic labels, falling back to the panel title.
 */
export function seriesLegend(
  template: string | undefined,
  labels: Record<string, string>,
  fallback: string,
): string {
  if (template && template.includes("{{")) {
    let anyFilled = false;
    const filled = template.replace(
      /\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}/g,
      (_match, key: string) => {
        const value = labels[key];
        if (value != null && value !== "") {
          anyFilled = true;
          return value;
        }
        return "";
      },
    );
    const cleaned = filled.replace(/\s+/g, " ").trim();
    return anyFilled && cleaned.length > 0 ? cleaned : fallback;
  }
  if (template) {
    return template;
  }
  const entries = Object.entries(labels).filter(
    ([key]) => key !== "__name__" && key !== "le" && key !== "quantile",
  );
  if (entries.length === 0) {
    return labels.__name__ ?? fallback;
  }
  return entries.map(([, value]) => value).join(", ");
}

/** A chart-ready series: a stable key, display label, colour slot, points. */
export interface ChartSeries {
  key: string;
  label: string;
  colorIndex: number;
  lastValue: number;
  points: MetricPoint[];
}

/**
 * Maps a panel's resolved series into chart-ready series with stable keys,
 * display labels, colour slots, and the latest finite value.
 */
export function toChartSeries(
  panelTitle: string,
  series: PanelSeries[],
): ChartSeries[] {
  return series.map((entry, index) => {
    const label = seriesLegend(entry.legendTemplate, entry.labels, panelTitle);
    return {
      key: `${index}:${label}`,
      label,
      colorIndex: index,
      lastValue: lastFiniteValue(entry.points),
      points: entry.points,
    };
  });
}

function lastFiniteValue(points: MetricPoint[]): number {
  for (let i = points.length - 1; i >= 0; i -= 1) {
    if (Number.isFinite(points[i].v)) {
      return points[i].v;
    }
  }
  return Number.NaN;
}

const compactNumber = new Intl.NumberFormat("en", {
  notation: "compact",
  maximumFractionDigits: 2,
});

/** Formats a value for axes, legends, and accessibility text. */
export function formatValue(value: number, unit: MetricUnit): string {
  if (!Number.isFinite(value)) {
    return "—";
  }
  switch (unit) {
    case "bytes":
      return formatBytes(value);
    case "seconds":
      return formatSeconds(value);
    case "ratio":
      return `${(value * 100).toFixed(1)}%`;
    case "rate":
      return `${compactNumber.format(value)}/s`;
    case "count":
    default:
      return compactNumber.format(value);
  }
}

function formatBytes(value: number): string {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let scaled = value;
  let unit = 0;
  while (Math.abs(scaled) >= 1024 && unit < units.length - 1) {
    scaled /= 1024;
    unit += 1;
  }
  const digits = unit === 0 ? 0 : 1;
  return `${scaled.toFixed(digits)} ${units[unit]}`;
}

function formatSeconds(value: number): string {
  const abs = Math.abs(value);
  if (abs === 0) return "0 s";
  if (abs < 1e-3) return `${(value * 1e6).toFixed(0)} µs`;
  if (abs < 1) return `${(value * 1e3).toFixed(1)} ms`;
  if (abs < 60) return `${value.toFixed(2)} s`;
  return `${(value / 60).toFixed(1)} min`;
}

/**
 * Builds a one-line text summary of a panel's latest values, used as the
 * accessible description alongside the chart (no color-only encoding).
 */
export function summariseSeries(
  series: ChartSeries[],
  unit: MetricUnit,
): string {
  if (series.length === 0) {
    return "No data.";
  }
  return series
    .map((entry) => `${entry.label}: ${formatValue(entry.lastValue, unit)}`)
    .join(", ");
}
