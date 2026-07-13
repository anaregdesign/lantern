import type { MetricPoint } from "~/lib/client/infrastructure/api/prometheus-query-range";

import type { MetricUnit, PanelQuery } from "./catalog";
import type { AggMode } from "./mode";
import type { RangeKey } from "./range";
import type { PanelSeries, PanelState } from "./state";

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
 * Composes a catalog {@link PanelQuery} into a full PromQL expression for
 * the active aggregation {@link AggMode}. The catalog stores only the
 * **inner** series; the outer aggregation is added here so the same query
 * renders per-replica (one line per `instance`) or as a cluster total.
 *
 * - `"sum"` (default) → `sum by (<by…>[, instance]) (<expr>)`, or `sum(<expr>)`
 *   when nothing is grouped.
 * - `{ quantile }` → `histogram_quantile(q, sum by (le, <by…>[, instance]) (<expr>))`
 *   for `_bucket` series.
 *
 * In per-replica mode `instance` is appended to the grouping set; in sum
 * mode it is dropped, collapsing every replica into one cluster line. The
 * `$__rate` placeholder (if any) is preserved for {@link resolveExpr}.
 */
export function composeQuery(query: PanelQuery, mode: AggMode): string {
  const by = query.by ?? [];
  const groupBy = mode === "per-replica" ? [...by, "instance"] : [...by];
  const agg = query.agg ?? "sum";
  if (typeof agg === "object") {
    const labels = ["le", ...groupBy].join(", ");
    return `histogram_quantile(${agg.quantile}, sum by (${labels}) (${query.expr}))`;
  }
  if (groupBy.length === 0) {
    return agg + "(" + query.expr + ")";
  }
  return agg + " by (" + groupBy.join(", ") + ") (" + query.expr + ")";
}

/**
 * A resolved replica identity: a short stable alias (`r0`, `r1`, …), the
 * colour slot driving its line hue across every panel, and the full
 * Prometheus `instance` value (`IP:port`) surfaced on hover.
 */
export interface ReplicaAlias {
  alias: string;
  colorSlot: number;
  instance: string;
}

/** Maps a full `instance` label to its resolved {@link ReplicaAlias}. */
export type ReplicaAliasMap = Record<string, ReplicaAlias>;

/**
 * Builds one `instance → alias` map for the whole Metrics section. The
 * distinct `instance` labels are collected across **all** panels in a
 * round and stable-sorted, so a given replica is assigned the same alias
 * and colour slot in every panel (consistent cross-panel identity) and the
 * assignment is deterministic regardless of Prometheus' series order.
 */
export function buildReplicaAliases(
  panels: Record<string, PanelState>,
): ReplicaAliasMap {
  const instances = new Set<string>();
  for (const panel of Object.values(panels)) {
    for (const entry of panel.series) {
      const instance = entry.labels.instance;
      if (instance != null && instance !== "") {
        instances.add(instance);
      }
    }
  }
  const sorted = [...instances].sort((a, b) => a.localeCompare(b));
  const map: ReplicaAliasMap = {};
  sorted.forEach((instance, index) => {
    map[instance] = { alias: `r${index}`, colorSlot: index, instance };
  });
  return map;
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

/** A chart-ready series: a stable key, display label, colour/dash slots, points. */
export interface ChartSeries {
  key: string;
  label: string;
  /**
   * Drives the line hue. For a per-replica series this is the replica's
   * stable colour slot, so the **same replica keeps the same colour across
   * every panel**. For a series with no `instance` (cluster-sum mode, or an
   * inherently single-cluster metric) it falls back to the per-series
   * secondary slot.
   */
  colorIndex: number;
  /**
   * Drives the line dash pattern, keyed off the **secondary** label
   * (e.g. `vertices` vs `edges`, `p50` vs `p99`). Within one replica's
   * colour, the dash disambiguates the second dimension so lines stay
   * distinguishable without relying on colour alone.
   */
  dashIndex: number;
  /** Full Prometheus `instance` (`IP:port`), surfaced on hover when present. */
  instance?: string;
  lastValue: number;
  points: MetricPoint[];
}

/**
 * Maps a panel's resolved series into chart-ready series with stable keys,
 * replica-aware display labels, colour/dash slots, and the latest finite
 * value. When a series carries an `instance` label present in `aliases`,
 * its label is prefixed with the short replica alias (`r0 · …`), its colour
 * tracks the replica, and its dash tracks the secondary label; otherwise it
 * falls back to a per-series slot (cluster-sum and single-cluster panels).
 */
export function toChartSeries(
  panelTitle: string,
  series: PanelSeries[],
  aliases: ReplicaAliasMap = {},
): ChartSeries[] {
  const dashSlots = new Map<string, number>();
  return series.map((entry, index) => {
    const baseLabel = seriesLegend(
      entry.legendTemplate,
      entry.labels,
      panelTitle,
    );
    if (!dashSlots.has(baseLabel)) {
      dashSlots.set(baseLabel, dashSlots.size);
    }
    const dashIndex = dashSlots.get(baseLabel) ?? 0;
    const instance = entry.labels.instance;
    const replica =
      instance != null && instance !== "" ? aliases[instance] : undefined;
    const label = replica ? `${replica.alias} · ${baseLabel}` : baseLabel;
    return {
      key: replica ? `${replica.alias}:${baseLabel}` : `${index}:${baseLabel}`,
      label,
      colorIndex: replica ? replica.colorSlot : dashIndex,
      dashIndex,
      instance: replica?.instance ?? instance,
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
