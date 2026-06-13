import { describe, expect, it } from "bun:test";

import { AGG_MODE_OPTIONS, DEFAULT_MODE, isAggMode } from "./mode";

describe("AGG_MODE_OPTIONS", () => {
  it("offers exactly the per-replica and sum modes", () => {
    expect(AGG_MODE_OPTIONS.map((o) => o.key)).toEqual(["per-replica", "sum"]);
  });

  it("has a human label for each mode", () => {
    for (const option of AGG_MODE_OPTIONS) {
      expect(option.label.length).toBeGreaterThan(0);
    }
  });
});

describe("DEFAULT_MODE", () => {
  it("defaults to per-replica visibility", () => {
    expect(DEFAULT_MODE).toBe("per-replica");
    expect(isAggMode(DEFAULT_MODE)).toBe(true);
  });
});

describe("isAggMode", () => {
  it("accepts known modes and rejects anything else", () => {
    expect(isAggMode("per-replica")).toBe(true);
    expect(isAggMode("sum")).toBe(true);
    expect(isAggMode("cluster")).toBe(false);
    expect(isAggMode("")).toBe(false);
  });
});
