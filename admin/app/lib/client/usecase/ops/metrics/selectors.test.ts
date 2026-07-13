import { describe, expect, it } from "bun:test";

import {
  buildReplicaAliases,
  composeQuery,
  formatValue,
  rangeToWindow,
  rateWindow,
  resolveExpr,
  seriesLegend,
  summariseSeries,
  toChartSeries,
} from "./selectors";
import type { PanelQuery } from "./catalog";
import type { PanelSeries, PanelState } from "./state";

describe("rangeToWindow", () => {
  it("maps each range to a step that yields ~60–290 points", () => {
    for (const range of ["15m", "1h", "6h", "24h"] as const) {
      const { rangeSeconds, stepSeconds } = rangeToWindow(range);
      const points = rangeSeconds / stepSeconds;
      expect(points).toBeGreaterThanOrEqual(60);
      expect(points).toBeLessThanOrEqual(290);
    }
  });
});

describe("rateWindow", () => {
  it("uses max(4×step, 60s) formatted as the largest whole unit", () => {
    expect(rateWindow(15)).toBe("1m"); // 60s
    expect(rateWindow(30)).toBe("2m"); // 120s
    expect(rateWindow(120)).toBe("8m"); // 480s
    expect(rateWindow(300)).toBe("20m"); // 1200s
  });

  it("formats whole hours as hours", () => {
    expect(rateWindow(900)).toBe("1h"); // 3600s
  });
});

describe("resolveExpr", () => {
  it("substitutes every $__rate occurrence with the window", () => {
    expect(
      resolveExpr("rate(lantern_mutation_log_entries_total[$__rate])", "2m"),
    ).toBe("rate(lantern_mutation_log_entries_total[2m])");
  });

  it("treats the window as a literal, not a pattern", () => {
    expect(resolveExpr("a[$__rate] + b[$__rate]", "1m")).toBe("a[1m] + b[1m]");
  });
});

describe("seriesLegend", () => {
  it("fills template placeholders from labels", () => {
    expect(
      seriesLegend("{{grpc_method}}", { grpc_method: "GetVertex" }, "fb"),
    ).toBe("GetVertex");
  });

  it("collapses whitespace and falls back when a placeholder is missing", () => {
    expect(seriesLegend("{{peer}} ← {{origin}}", {}, "fallback")).toBe(
      "fallback",
    );
    expect(seriesLegend("scan {{op}}", { op: "GetVertices" }, "fb")).toBe(
      "scan GetVertices",
    );
  });

  it("returns a static template verbatim", () => {
    expect(seriesLegend("dropped", { op: "x" }, "fb")).toBe("dropped");
  });

  it("derives from non-synthetic labels when no template is given", () => {
    expect(
      seriesLegend(
        undefined,
        { __name__: "m", le: "0.1", kind: "vertex" },
        "fb",
      ),
    ).toBe("vertex");
  });

  it("falls back to the metric name when only synthetic labels remain", () => {
    expect(
      seriesLegend(undefined, { __name__: "lantern_vertices" }, "fb"),
    ).toBe("lantern_vertices");
  });
});

describe("formatValue", () => {
  it("renders an em dash for non-finite values", () => {
    expect(formatValue(Number.NaN, "count")).toBe("—");
    expect(formatValue(Number.POSITIVE_INFINITY, "rate")).toBe("—");
  });

  it("formats counts compactly", () => {
    expect(formatValue(12, "count")).toBe("12");
    expect(formatValue(1500, "count")).toBe("1.5K");
  });

  it("suffixes rates with /s", () => {
    expect(formatValue(2.5, "rate")).toBe("2.5/s");
  });

  it("formats ratios as percentages", () => {
    expect(formatValue(0.42, "ratio")).toBe("42.0%");
  });

  it("humanises byte values in binary units", () => {
    expect(formatValue(512, "bytes")).toBe("512 B");
    expect(formatValue(1024, "bytes")).toBe("1.0 KiB");
    expect(formatValue(1024 * 1024 * 3, "bytes")).toBe("3.0 MiB");
  });

  it("humanises sub-second durations", () => {
    expect(formatValue(0.00002, "seconds")).toBe("20 µs");
    expect(formatValue(0.0023, "seconds")).toBe("2.3 ms");
    expect(formatValue(1.5, "seconds")).toBe("1.50 s");
    expect(formatValue(90, "seconds")).toBe("1.5 min");
  });
});

describe("toChartSeries", () => {
  const series: PanelSeries[] = [
    {
      legendTemplate: "vertices",
      labels: { __name__: "lantern_vertices" },
      points: [
        { t: 0, v: 1 },
        { t: 1, v: 5 },
      ],
    },
    {
      legendTemplate: "{{grpc_method}}",
      labels: { grpc_method: "GetVertex" },
      points: [{ t: 0, v: Number.NaN }],
    },
  ];

  it("assigns stable keys, labels, colour slots, and last finite value", () => {
    const chart = toChartSeries("Panel", series);
    expect(chart).toHaveLength(2);
    expect(chart[0]).toMatchObject({
      label: "vertices",
      colorIndex: 0,
      lastValue: 5,
    });
    expect(chart[0].key).toBe("0:vertices");
    expect(chart[1].label).toBe("GetVertex");
    expect(Number.isNaN(chart[1].lastValue)).toBe(true);
  });
});

describe("composeQuery", () => {
  const raw: PanelQuery = { expr: "lantern_vertices", legend: "vertices" };
  const grouped: PanelQuery = {
    expr: "rate(grpc_server_handled_total[$__rate])",
    by: ["grpc_method"],
    legend: "{{grpc_method}}",
  };
  const quantile: PanelQuery = {
    expr: "rate(grpc_server_handling_seconds_bucket[$__rate])",
    agg: { quantile: 0.99 },
    legend: "p99",
  };

  it("adds instance to the grouping in per-replica mode", () => {
    expect(composeQuery(raw, "per-replica")).toBe(
      "sum by (instance) (lantern_vertices)",
    );
    expect(composeQuery(grouped, "per-replica")).toBe(
      "sum by (grpc_method, instance) (rate(grpc_server_handled_total[$__rate]))",
    );
  });

  it("drops instance and keeps secondary grouping in sum mode", () => {
    expect(composeQuery(raw, "sum")).toBe("sum(lantern_vertices)");
    expect(composeQuery(grouped, "sum")).toBe(
      "sum by (grpc_method) (rate(grpc_server_handled_total[$__rate]))",
    );
  });

  it("supports non-additive gauge aggregation", () => {
    const average: PanelQuery = {
      expr: "lantern_search_index_retained_ratio",
      agg: "avg",
    };
    expect(composeQuery(average, "per-replica")).toBe(
      "avg by (instance) (lantern_search_index_retained_ratio)",
    );
    expect(composeQuery(average, "sum")).toBe(
      "avg(lantern_search_index_retained_ratio)",
    );
  });

  it("wraps bucket series in histogram_quantile with le grouped", () => {
    expect(composeQuery(quantile, "per-replica")).toBe(
      "histogram_quantile(0.99, sum by (le, instance) (rate(grpc_server_handling_seconds_bucket[$__rate])))",
    );
    expect(composeQuery(quantile, "sum")).toBe(
      "histogram_quantile(0.99, sum by (le) (rate(grpc_server_handling_seconds_bucket[$__rate])))",
    );
  });

  it("preserves the $__rate placeholder for resolveExpr", () => {
    expect(composeQuery(grouped, "per-replica")).toContain("$__rate");
  });
});

describe("buildReplicaAliases", () => {
  function panel(instances: (string | undefined)[]): PanelState {
    return {
      status: "ready",
      error: null,
      series: instances.map((instance) => {
        const labels: Record<string, string> =
          instance == null ? {} : { instance };
        return { legendTemplate: "x", labels, points: [] };
      }),
    };
  }

  it("assigns stable aliases deterministically by sorted instance", () => {
    const aliases = buildReplicaAliases({
      b: panel(["10.0.0.3:6380", "10.0.0.1:6380"]),
      a: panel(["10.0.0.2:6380"]),
    });
    expect(aliases["10.0.0.1:6380"]).toEqual({
      alias: "r0",
      colorSlot: 0,
      instance: "10.0.0.1:6380",
    });
    expect(aliases["10.0.0.2:6380"].alias).toBe("r1");
    expect(aliases["10.0.0.3:6380"].alias).toBe("r2");
  });

  it("keeps a replica's colour slot stable across panels", () => {
    const aliases = buildReplicaAliases({
      throughput: panel(["10.0.0.1:6380", "10.0.0.2:6380"]),
      latency: panel(["10.0.0.2:6380", "10.0.0.1:6380"]),
    });
    expect(aliases["10.0.0.1:6380"].colorSlot).toBe(0);
    expect(aliases["10.0.0.2:6380"].colorSlot).toBe(1);
  });

  it("ignores series without an instance label", () => {
    const aliases = buildReplicaAliases({ p: panel([undefined, ""]) });
    expect(Object.keys(aliases)).toHaveLength(0);
  });
});

describe("toChartSeries replica awareness", () => {
  const aliases = buildReplicaAliases({
    cache: {
      status: "ready",
      error: null,
      series: [
        {
          legendTemplate: "x",
          labels: { instance: "10.0.0.1:6380" },
          points: [],
        },
        {
          legendTemplate: "x",
          labels: { instance: "10.0.0.2:6380" },
          points: [],
        },
      ],
    },
  });

  const series: PanelSeries[] = [
    {
      legendTemplate: "vertices",
      labels: { instance: "10.0.0.1:6380" },
      points: [{ t: 0, v: 3 }],
    },
    {
      legendTemplate: "vertices",
      labels: { instance: "10.0.0.2:6380" },
      points: [{ t: 0, v: 4 }],
    },
    {
      legendTemplate: "edges",
      labels: { instance: "10.0.0.1:6380" },
      points: [{ t: 0, v: 5 }],
    },
  ];

  it("prefixes labels with the replica alias and tracks its colour", () => {
    const chart = toChartSeries("Cache", series, aliases);
    expect(chart[0]).toMatchObject({
      label: "r0 · vertices",
      colorIndex: 0,
      dashIndex: 0,
      instance: "10.0.0.1:6380",
      key: "r0:vertices",
    });
    expect(chart[1]).toMatchObject({ label: "r1 · vertices", colorIndex: 1 });
  });

  it("uses dash for the secondary dimension within a replica colour", () => {
    const chart = toChartSeries("Cache", series, aliases);
    // Same replica (10.0.0.1) keeps colour 0; vertices vs edges differ by dash.
    expect(chart[0].colorIndex).toBe(0);
    expect(chart[2].colorIndex).toBe(0);
    expect(chart[0].dashIndex).toBe(0);
    expect(chart[2].dashIndex).toBe(1);
    expect(chart[2].key).toBe("r0:edges");
  });
});

describe("summariseSeries", () => {
  it("returns a no-data sentinel for an empty panel", () => {
    expect(summariseSeries([], "count")).toBe("No data.");
  });

  it("joins the latest values with their labels", () => {
    const chart = toChartSeries("Panel", [
      { legendTemplate: "vertices", labels: {}, points: [{ t: 0, v: 1500 }] },
      { legendTemplate: "edges", labels: {}, points: [{ t: 0, v: 3000 }] },
    ]);
    expect(summariseSeries(chart, "count")).toBe("vertices: 1.5K, edges: 3K");
  });
});
