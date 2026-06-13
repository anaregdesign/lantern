import { useId } from "react";

import type { MetricUnit } from "~/lib/client/usecase/ops/metrics/catalog";
import {
  formatValue,
  type ChartSeries,
} from "~/lib/client/usecase/ops/metrics/selectors";
import styles from "./TimeSeriesChart.module.css";

export interface TimeSeriesChartProps {
  series: ChartSeries[];
  unit: MetricUnit;
  ariaLabel: string;
}

// SVG user-space geometry. The chart scales to its container via the
// viewBox; these numbers are the coordinate system, not CSS pixels.
const WIDTH = 760;
const HEIGHT = 240;
const PAD_LEFT = 56;
const PAD_RIGHT = 16;
const PAD_TOP = 16;
const PAD_BOTTOM = 28;
const PLOT_W = WIDTH - PAD_LEFT - PAD_RIGHT;
const PLOT_H = HEIGHT - PAD_TOP - PAD_BOTTOM;
const Y_TICKS = 4;
// Number of distinct hue / dash slots defined in the CSS module
// (.color0…N / .dash0…N). Colour encodes identity (replica, or series) and
// dash encodes the secondary dimension, so a chart is never colour-only.
const COLOR_SLOTS = 8;
const DASH_SLOTS = 8;

/**
 * TimeSeriesChart is a dependency-free multi-series line chart. The admin
 * SPA ships no charting library, so this renders raw SVG. Accessibility is
 * a first-class concern: every series carries both a distinct colour and a
 * distinct dash pattern (never colour alone), the chart exposes
 * `role="img"` + `<title>`/`<desc>`, and the consuming MetricPanel always
 * pairs it with a text legend and a values summary.
 */
export function TimeSeriesChart({
  series,
  unit,
  ariaLabel,
}: TimeSeriesChartProps) {
  const titleId = useId();
  const descId = useId();
  const domain = computeDomain(series);

  if (!domain) {
    return (
      <p className={styles.empty} role="img" aria-label={ariaLabel}>
        No data in range.
      </p>
    );
  }

  const { minT, spanT, minV, spanV } = domain;
  const xScale = (t: number) => PAD_LEFT + ((t - minT) / spanT) * PLOT_W;
  const yScale = (v: number) => PAD_TOP + (1 - (v - minV) / spanV) * PLOT_H;

  const yTicks = buildYTicks(minV, spanV);
  const xTicks = buildXTicks(minT, spanT);

  return (
    <figure className={styles.figure}>
      <svg
        className={styles.chart}
        viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
        role="img"
        aria-labelledby={`${titleId} ${descId}`}
        preserveAspectRatio="xMidYMid meet"
      >
        <title id={titleId}>{ariaLabel}</title>
        <desc id={descId}>
          {series.length} series, latest values:{" "}
          {series
            .map(
              (s) =>
                `${s.label} ${formatValue(s.lastValue, unit)}` +
                (s.instance ? ` (${s.instance})` : ""),
            )
            .join("; ")}
          .
        </desc>
        {yTicks.map((tick) => {
          const y = yScale(tick).toFixed(1);
          return (
            <g key={`y-${tick}`}>
              <line
                className={styles.grid}
                x1={PAD_LEFT}
                y1={y}
                x2={PAD_LEFT + PLOT_W}
                y2={y}
              />
              <text
                className={styles.axisLabel}
                x={PAD_LEFT - 8}
                y={y}
                textAnchor="end"
                dominantBaseline="central"
              >
                {formatValue(tick, unit)}
              </text>
            </g>
          );
        })}
        {xTicks.map((t, i) => {
          const x = xScale(t);
          const anchor =
            i === 0 ? "start" : i === xTicks.length - 1 ? "end" : "middle";
          return (
            <text
              key={`x-${t}`}
              className={styles.axisLabel}
              x={x.toFixed(1)}
              y={HEIGHT - PAD_BOTTOM + 18}
              textAnchor={anchor}
            >
              {formatClock(t)}
            </text>
          );
        })}
        {series.map((s) => {
          const d = buildPath(s.points, xScale, yScale);
          if (!d) return null;
          return (
            <path
              key={s.key}
              className={`${styles.line} ${colorClass(s.colorIndex)} ${dashClass(s.dashIndex)}`}
              d={d}
            >
              <title>
                {s.label} {formatValue(s.lastValue, unit)}
                {s.instance ? ` — ${s.instance}` : ""}
              </title>
            </path>
          );
        })}
      </svg>
      <ul className={styles.legend}>
        {series.map((s) => (
          <li
            key={s.key}
            className={styles.legendItem}
            title={s.instance ?? undefined}
          >
            <svg
              className={styles.swatch}
              viewBox="0 0 24 8"
              aria-hidden="true"
            >
              <line
                className={`${styles.line} ${colorClass(s.colorIndex)} ${dashClass(s.dashIndex)}`}
                x1="0"
                y1="4"
                x2="24"
                y2="4"
              />
            </svg>
            <span className={styles.legendLabel}>{s.label}</span>
            <span className={styles.legendValue}>
              {formatValue(s.lastValue, unit)}
            </span>
          </li>
        ))}
      </ul>
    </figure>
  );
}

function colorClass(colorIndex: number): string {
  const slot = ((colorIndex % COLOR_SLOTS) + COLOR_SLOTS) % COLOR_SLOTS;
  return styles[`color${slot}`] ?? "";
}

function dashClass(dashIndex: number): string {
  const slot = ((dashIndex % DASH_SLOTS) + DASH_SLOTS) % DASH_SLOTS;
  return styles[`dash${slot}`] ?? "";
}

interface Domain {
  minT: number;
  spanT: number;
  minV: number;
  spanV: number;
}

function computeDomain(series: ChartSeries[]): Domain | null {
  let minT = Number.POSITIVE_INFINITY;
  let maxT = Number.NEGATIVE_INFINITY;
  let minV = Number.POSITIVE_INFINITY;
  let maxV = Number.NEGATIVE_INFINITY;
  for (const s of series) {
    for (const p of s.points) {
      if (p.t < minT) minT = p.t;
      if (p.t > maxT) maxT = p.t;
      if (Number.isFinite(p.v)) {
        if (p.v < minV) minV = p.v;
        if (p.v > maxV) maxV = p.v;
      }
    }
  }
  if (!Number.isFinite(minT) || !Number.isFinite(minV)) {
    return null;
  }
  // Expand a flat domain so a constant series renders as a centred line.
  if (minV === maxV) {
    const pad = minV === 0 ? 1 : Math.abs(minV) * 0.1;
    minV -= pad;
    maxV += pad;
  }
  return {
    minT,
    spanT: maxT - minT || 1,
    minV,
    spanV: maxV - minV || 1,
  };
}

function buildPath(
  points: ChartSeries["points"],
  xScale: (t: number) => number,
  yScale: (v: number) => number,
): string {
  let d = "";
  let pen = false;
  for (const p of points) {
    if (!Number.isFinite(p.v)) {
      // A gap (missing/NaN sample) lifts the pen so the line breaks
      // instead of interpolating across an absent region.
      pen = false;
      continue;
    }
    const x = xScale(p.t).toFixed(1);
    const y = yScale(p.v).toFixed(1);
    d += `${pen ? "L" : "M"}${x},${y} `;
    pen = true;
  }
  return d.trim();
}

function buildYTicks(minV: number, spanV: number): number[] {
  const ticks: number[] = [];
  for (let i = 0; i <= Y_TICKS; i += 1) {
    ticks.push(minV + (spanV * i) / Y_TICKS);
  }
  return ticks;
}

function buildXTicks(minT: number, spanT: number): number[] {
  return [minT, minT + spanT / 2, minT + spanT];
}

function formatClock(t: number): string {
  return new Date(t * 1000).toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
  });
}
