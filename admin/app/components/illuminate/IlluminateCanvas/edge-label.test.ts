import { describe, expect, test } from "bun:test";

import { formatEdgeWeight } from "./edge-label";

describe("formatEdgeWeight", () => {
  test("renders a whole number without a decimal point", () => {
    expect(formatEdgeWeight(3)).toBe("3");
    expect(formatEdgeWeight(0)).toBe("0");
    expect(formatEdgeWeight(42)).toBe("42");
  });

  test("renders a fractional value to one decimal", () => {
    expect(formatEdgeWeight(2.5)).toBe("2.5");
    expect(formatEdgeWeight(0.3)).toBe("0.3");
  });

  test("rounds to a single decimal place", () => {
    expect(formatEdgeWeight(1.24)).toBe("1.2");
    expect(formatEdgeWeight(1.26)).toBe("1.3");
  });

  test("returns an empty string for non-finite input", () => {
    expect(formatEdgeWeight(Number.NaN)).toBe("");
    expect(formatEdgeWeight(Number.POSITIVE_INFINITY)).toBe("");
  });
});
