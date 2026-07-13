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
    const searchPanels = METRIC_PANELS.filter(
      (panel) => panel.group === "search",
    );
    expect(searchPanels.length).toBeGreaterThanOrEqual(8);
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
    for (const panel of METRIC_PANELS.filter(
      (candidate) => candidate.group === "search",
    )) {
      for (const query of panel.queries) {
        for (const label of query.by ?? []) {
          expect(allowed.has(label)).toBe(true);
          expect(label).not.toMatch(/query|prefix|key|value/);
        }
      }
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
});
