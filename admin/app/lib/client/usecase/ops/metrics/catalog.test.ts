import { describe, expect, it } from "bun:test";

import { METRIC_PANELS, PANEL_GROUPS, type PanelGroup } from "./catalog";

const KNOWN_UNITS = new Set(["count", "rate", "ratio", "seconds", "bytes"]);
const KNOWN_GROUPS = new Set<PanelGroup>(PANEL_GROUPS.map((g) => g.id));

describe("METRIC_PANELS", () => {
  it("is non-empty", () => {
    expect(METRIC_PANELS.length).toBeGreaterThan(0);
  });

  it("has unique kebab-case ids", () => {
    const ids = METRIC_PANELS.map((p) => p.id);
    expect(new Set(ids).size).toBe(ids.length);
    for (const id of ids) {
      expect(id).toMatch(/^[a-z0-9]+(-[a-z0-9]+)*$/);
    }
  });

  it("references only known groups and units", () => {
    for (const panel of METRIC_PANELS) {
      expect(KNOWN_GROUPS.has(panel.group)).toBe(true);
      expect(KNOWN_UNITS.has(panel.unit)).toBe(true);
    }
  });

  it("gives every panel a title, description, and at least one query", () => {
    for (const panel of METRIC_PANELS) {
      expect(panel.title.length).toBeGreaterThan(0);
      expect(panel.description.length).toBeGreaterThan(0);
      expect(panel.queries.length).toBeGreaterThan(0);
      for (const query of panel.queries) {
        expect(query.expr.trim().length).toBeGreaterThan(0);
      }
    }
  });

  it("uses the $__rate placeholder inside every rate()/irate() window", () => {
    for (const panel of METRIC_PANELS) {
      for (const query of panel.queries) {
        if (/\b(?:rate|irate)\(/.test(query.expr)) {
          expect(query.expr).toContain("[$__rate]");
        }
      }
    }
  });

  it("never hard-codes a literal range window like [5m]", () => {
    for (const panel of METRIC_PANELS) {
      for (const query of panel.queries) {
        expect(query.expr).not.toMatch(/\[\d+[smhdwy]\]/);
      }
    }
  });

  it("balances parentheses in every expression", () => {
    for (const panel of METRIC_PANELS) {
      for (const query of panel.queries) {
        const opens = (query.expr.match(/\(/g) ?? []).length;
        const closes = (query.expr.match(/\)/g) ?? []).length;
        expect(opens).toBe(closes);
      }
    }
  });

  it("covers search outcomes, p99 phases, index health, and retention", () => {
    const searchPanels = METRIC_PANELS.filter((panel) =>
      panel.queries.some((query) => query.expr.includes("lantern_search_")),
    );
    expect(searchPanels.length).toBeGreaterThanOrEqual(16);
    const expressions = searchPanels.flatMap((panel) =>
      panel.queries.map((query) => query.expr),
    );
    for (const family of [
      "lantern_search_calls_total",
      "lantern_search_duration_seconds_bucket",
      "lantern_search_phase_duration_seconds_bucket",
      "lantern_search_results_bucket",
      "lantern_search_work_bucket",
      "lantern_search_index_state",
      "lantern_search_config_match",
      "lantern_search_index_retained_ratio",
      "lantern_search_rejections_total",
    ]) {
      expect(expressions.some((expr) => expr.includes(family))).toBe(true);
    }
  });

  it("uses only bounded grouping labels for search panels", () => {
    const allowed = new Set([
      "mode",
      "outcome",
      "reason",
      "phase",
      "kind",
      "state",
      "peer",
    ]);
    for (const panel of METRIC_PANELS.filter((candidate) =>
      candidate.queries.some((query) => query.expr.includes("lantern_search_")),
    )) {
      for (const query of panel.queries) {
        for (const label of query.by ?? []) {
          expect(allowed.has(label)).toBe(true);
          expect(label).not.toMatch(/query|prefix|key|value/);
        }
      }
    }
  });

  it("splits Illuminate outcomes by family and suppresses pre-warmed zero series", () => {
    const panels = METRIC_PANELS.filter((panel) =>
      panel.id.startsWith("illuminate-outcomes-"),
    );
    expect(panels.map((panel) => panel.id)).toEqual([
      "illuminate-outcomes-bfs",
      "illuminate-outcomes-ppr",
      "illuminate-outcomes-community",
    ]);
    expect(
      METRIC_PANELS.some((panel) => panel.id === "traversal-outcomes"),
    ).toBe(false);

    for (const [panel, algorithm] of panels.map(
      (panel, index) => [panel, ["bfs", "ppr", "community"][index]] as const,
    )) {
      expect(panel.queries).toHaveLength(1);
      const query = panel.queries[0];
      expect(query.expr).toContain(`algorithm="${algorithm}"`);
      expect(query.expr).toEndWith(") > 0");
      expect(query.by).toEqual(["phase", "code"]);
      expect(query.legend).toBe("{{phase}} · {{code}}");
    }
  });

  it("splits mixed high-cardinality panels by operational decision", () => {
    const ids = new Set(METRIC_PANELS.map((panel) => panel.id));

    for (const retired of [
      "rpc-throughput",
      "traversal-latency",
      "search-outcomes",
      "search-latency",
      "search-phase-latency",
      "search-work",
      "search-index-health",
      "search-index-population",
      "search-index-structures",
      "rejections",
    ]) {
      expect(ids.has(retired)).toBe(false);
    }

    for (const focused of [
      "rpc-read-throughput",
      "rpc-write-throughput",
      "rpc-query-throughput",
      "rpc-status-throughput",
      "illuminate-latency",
      "scan-latency",
      "search-successes",
      "search-interruptions",
      "search-refusals",
      "search-internal-failures",
      "search-analysis-latency",
      "search-expansion-latency",
      "search-selection-latency",
      "search-query-work",
      "search-expansion-work",
      "search-index-work",
      "search-candidate-work",
      "search-index-state",
      "search-config-agreement",
      "search-index-documents",
      "search-expiration-backlog",
      "search-index-dictionary",
      "search-index-positions",
      "replication-apply",
      "replication-drops",
      "validation-rejections",
      "rate-limit-rejections",
      "tombstone-clamp-rejections",
    ]) {
      expect(ids.has(focused)).toBe(true);
    }
  });

  it("filters pre-warmed counter series out of dense legends", () => {
    for (const id of [
      "rpc-read-throughput",
      "rpc-write-throughput",
      "rpc-query-throughput",
      "rpc-status-throughput",
      "search-successes",
      "search-interruptions",
      "search-refusals",
      "search-internal-failures",
      "search-rejections",
      "replication-apply",
      "replication-drops",
      "validation-rejections",
    ]) {
      const panel = METRIC_PANELS.find((candidate) => candidate.id === id);
      expect(panel).toBeDefined();
      expect(
        panel?.queries.every((query) => query.expr.endsWith(") > 0")),
      ).toBe(true);
    }
  });

  it("covers every read scan method in the focused RPC panel", () => {
    const panel = METRIC_PANELS.find(
      (candidate) => candidate.id === "rpc-read-throughput",
    );
    expect(panel).toBeDefined();
    const expression = panel?.queries[0].expr ?? "";
    for (const method of ["ScanVertices", "ScanVertexKeys", "ScanEdges"]) {
      expect(expression).toContain(method);
    }
  });
});

describe("PANEL_GROUPS", () => {
  it("covers every group referenced by a panel", () => {
    const used = new Set(METRIC_PANELS.map((p) => p.group));
    for (const group of used) {
      expect(KNOWN_GROUPS.has(group)).toBe(true);
    }
  });

  it("has unique group ids", () => {
    const ids = PANEL_GROUPS.map((g) => g.id);
    expect(new Set(ids).size).toBe(ids.length);
  });

  it("provides operator-oriented labels and descriptions", () => {
    expect(PANEL_GROUPS.map((group) => group.label)).toEqual([
      "Store inventory",
      "Request traffic",
      "Illuminate",
      "Search requests",
      "Search index",
      "Maintenance",
      "Replication",
      "Guardrails",
      "Process / Go runtime",
    ]);
    for (const group of PANEL_GROUPS) {
      expect(group.description.length).toBeGreaterThan(0);
    }
  });
});
