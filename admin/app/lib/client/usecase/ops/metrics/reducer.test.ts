import { describe, expect, it } from "bun:test";

import { METRIC_PANELS } from "./catalog";
import { metricsReducer, type MetricsAction } from "./reducer";
import { initialMetricsState, type PanelSeries } from "./state";

const firstPanelId = METRIC_PANELS[0].id;
const secondPanelId = METRIC_PANELS[1].id;

const sampleSeries: PanelSeries[] = [
  { legendTemplate: "x", labels: {}, points: [{ t: 0, v: 1 }] },
];

describe("metricsReducer", () => {
  it("flips idle panels to loading on the first fetch round", () => {
    const next = metricsReducer(initialMetricsState(), {
      type: "FETCH_STARTED",
      epoch: 1,
    });
    expect(next.fetchEpoch).toBe(1);
    for (const panel of Object.values(next.panels)) {
      expect(panel.status).toBe("loading");
    }
  });

  it("keeps ready panels in place on a subsequent fetch (no flash)", () => {
    let state = metricsReducer(initialMetricsState(), {
      type: "FETCH_STARTED",
      epoch: 1,
    });
    state = metricsReducer(state, {
      type: "PANEL_LOADED",
      epoch: 1,
      id: firstPanelId,
      series: sampleSeries,
    });
    const next = metricsReducer(state, { type: "FETCH_STARTED", epoch: 2 });
    expect(next.panels[firstPanelId].status).toBe("ready");
    expect(next.panels[firstPanelId].series).toEqual(sampleSeries);
  });

  it("stores series on PANEL_LOADED for the matching epoch", () => {
    let state = metricsReducer(initialMetricsState(), {
      type: "FETCH_STARTED",
      epoch: 3,
    });
    state = metricsReducer(state, {
      type: "PANEL_LOADED",
      epoch: 3,
      id: firstPanelId,
      series: sampleSeries,
    });
    expect(state.panels[firstPanelId]).toEqual({
      status: "ready",
      series: sampleSeries,
      error: null,
    });
  });

  it("records an error on PANEL_ERROR for the matching epoch", () => {
    let state = metricsReducer(initialMetricsState(), {
      type: "FETCH_STARTED",
      epoch: 4,
    });
    state = metricsReducer(state, {
      type: "PANEL_ERROR",
      epoch: 4,
      id: secondPanelId,
      error: "boom",
    });
    expect(state.panels[secondPanelId].status).toBe("error");
    expect(state.panels[secondPanelId].error).toBe("boom");
  });

  it("drops a stale PANEL_LOADED from an older epoch", () => {
    let state = metricsReducer(initialMetricsState(), {
      type: "FETCH_STARTED",
      epoch: 5,
    });
    // A newer round started before the old result arrived.
    state = metricsReducer(state, { type: "FETCH_STARTED", epoch: 6 });
    const stale: MetricsAction = {
      type: "PANEL_LOADED",
      epoch: 5,
      id: firstPanelId,
      series: sampleSeries,
    };
    const next = metricsReducer(state, stale);
    expect(next).toBe(state);
    expect(next.panels[firstPanelId].status).not.toBe("ready");
  });

  it("ignores actions for an unknown panel id", () => {
    const state = metricsReducer(initialMetricsState(), {
      type: "FETCH_STARTED",
      epoch: 1,
    });
    const next = metricsReducer(state, {
      type: "PANEL_LOADED",
      epoch: 1,
      id: "does-not-exist",
      series: sampleSeries,
    });
    expect(next).toBe(state);
  });

  it("resets panels to a fresh idle state when the range changes", () => {
    let state = metricsReducer(initialMetricsState("1h"), {
      type: "FETCH_STARTED",
      epoch: 1,
    });
    state = metricsReducer(state, {
      type: "PANEL_LOADED",
      epoch: 1,
      id: firstPanelId,
      series: sampleSeries,
    });
    const next = metricsReducer(state, { type: "SET_RANGE", range: "6h" });
    expect(next.range).toBe("6h");
    expect(next.fetchEpoch).toBe(0);
    expect(next.panels[firstPanelId].status).toBe("idle");
    expect(next.panels[firstPanelId].series).toEqual([]);
  });

  it("is a no-op when SET_RANGE matches the current range", () => {
    const state = initialMetricsState("1h");
    const next = metricsReducer(state, { type: "SET_RANGE", range: "1h" });
    expect(next).toBe(state);
  });

  it("RESET preserves the range but clears panels and epoch", () => {
    let state = metricsReducer(initialMetricsState("6h"), {
      type: "FETCH_STARTED",
      epoch: 9,
    });
    state = metricsReducer(state, {
      type: "PANEL_LOADED",
      epoch: 9,
      id: firstPanelId,
      series: sampleSeries,
    });
    const next = metricsReducer(state, { type: "RESET" });
    expect(next.range).toBe("6h");
    expect(next.fetchEpoch).toBe(0);
    expect(next.panels[firstPanelId].status).toBe("idle");
  });
});
