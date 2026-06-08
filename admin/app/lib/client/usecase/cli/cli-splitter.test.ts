import { describe, expect, test } from "bun:test";
import {
  SPLITTER_DEFAULT_RATIO,
  SPLITTER_MIN_PANE_PX,
  clampRatio,
  formatStoredRatio,
  nudgeRatio,
  parseStoredRatio,
} from "./cli-splitter";

describe("cli-splitter clampRatio", () => {
  test("honours min pane size on both sides for a wide container", () => {
    // 1600px container, 360px min → ratio bounded to [0.225, 0.775].
    const w = 1600;
    expect(clampRatio(0.6, w)).toBeCloseTo(0.6, 6);
    expect(clampRatio(0.1, w)).toBeCloseTo(SPLITTER_MIN_PANE_PX / w, 6);
    expect(clampRatio(0.95, w)).toBeCloseTo(1 - SPLITTER_MIN_PANE_PX / w, 6);
  });

  test("falls back to soft [0.1, 0.9] clamp when container cannot honour min sizes", () => {
    // 600px container, 360px min → 2*360 >= 600, soft clamp.
    expect(clampRatio(0.6, 600)).toBeCloseTo(0.6, 6);
    expect(clampRatio(0.05, 600)).toBeCloseTo(0.1, 6);
    expect(clampRatio(0.99, 600)).toBeCloseTo(0.9, 6);
  });

  test("returns default ratio for non-finite desired", () => {
    expect(clampRatio(Number.NaN, 1600)).toBe(SPLITTER_DEFAULT_RATIO);
    expect(clampRatio(Number.POSITIVE_INFINITY, 1600)).toBe(
      SPLITTER_DEFAULT_RATIO,
    );
  });

  test("returns soft clamp for a zero/negative container width", () => {
    expect(clampRatio(0.6, 0)).toBeCloseTo(0.6, 6);
    expect(clampRatio(0.05, -100)).toBeCloseTo(0.1, 6);
  });

  test("respects a custom min pane size", () => {
    expect(clampRatio(0.95, 1000, 100)).toBeCloseTo(0.9, 6);
    expect(clampRatio(0.05, 1000, 100)).toBeCloseTo(0.1, 6);
  });
});

describe("cli-splitter parseStoredRatio", () => {
  test("accepts numeric strings strictly inside (0, 1)", () => {
    expect(parseStoredRatio("0.42")).toBeCloseTo(0.42, 6);
    expect(parseStoredRatio("0.0001")).toBeCloseTo(0.0001, 6);
  });

  test("rejects null", () => {
    expect(parseStoredRatio(null)).toBeNull();
  });

  test("rejects garbage", () => {
    expect(parseStoredRatio("not a number")).toBeNull();
    expect(parseStoredRatio("")).toBeNull();
  });

  test("rejects boundary values 0 and 1", () => {
    expect(parseStoredRatio("0")).toBeNull();
    expect(parseStoredRatio("1")).toBeNull();
    expect(parseStoredRatio("-0.5")).toBeNull();
    expect(parseStoredRatio("1.5")).toBeNull();
  });

  test("round-trips through formatStoredRatio", () => {
    expect(parseStoredRatio(formatStoredRatio(0.6))).toBeCloseTo(0.6, 4);
    expect(parseStoredRatio(formatStoredRatio(0.2375))).toBeCloseTo(0.2375, 4);
  });
});

describe("cli-splitter nudgeRatio", () => {
  test("ArrowLeft/ArrowRight nudge by 2% (no shift)", () => {
    expect(nudgeRatio(0.5, "ArrowLeft", { shiftKey: false })).toBeCloseTo(
      0.48,
      6,
    );
    expect(nudgeRatio(0.5, "ArrowRight", { shiftKey: false })).toBeCloseTo(
      0.52,
      6,
    );
  });

  test("ArrowLeft/ArrowRight jump by 10% with shift", () => {
    expect(nudgeRatio(0.5, "ArrowLeft", { shiftKey: true })).toBeCloseTo(
      0.4,
      6,
    );
    expect(nudgeRatio(0.5, "ArrowRight", { shiftKey: true })).toBeCloseTo(
      0.6,
      6,
    );
  });

  test("Home returns 0 (caller clamps to min)", () => {
    expect(nudgeRatio(0.7, "Home", { shiftKey: false })).toBe(0);
  });

  test("End returns 1 (caller clamps to max)", () => {
    expect(nudgeRatio(0.3, "End", { shiftKey: false })).toBe(1);
  });

  test("Enter resets to default", () => {
    expect(nudgeRatio(0.2, "Enter", { shiftKey: false })).toBe(
      SPLITTER_DEFAULT_RATIO,
    );
  });

  test("returns null for unrelated keys", () => {
    expect(nudgeRatio(0.5, "Escape", { shiftKey: false })).toBeNull();
    expect(nudgeRatio(0.5, "a", { shiftKey: false })).toBeNull();
    expect(nudgeRatio(0.5, " ", { shiftKey: false })).toBeNull();
  });
});
