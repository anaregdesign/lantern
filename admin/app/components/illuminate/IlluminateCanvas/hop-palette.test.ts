import { describe, expect, test } from "bun:test";

import { HOP_FAR_THRESHOLD, colorForHop, describeHop } from "./hop-palette";
import { FALLBACK_PALETTE } from "./palette";

describe("colorForHop", () => {
  test("returns hop0 for the origin (distance 0)", () => {
    expect(colorForHop(0, FALLBACK_PALETTE)).toBe(FALLBACK_PALETTE.hop0);
  });

  test("returns hop1 for the single-hop ring", () => {
    expect(colorForHop(1, FALLBACK_PALETTE)).toBe(FALLBACK_PALETTE.hop1);
  });

  test("returns hop2 for the two-hop ring", () => {
    expect(colorForHop(2, FALLBACK_PALETTE)).toBe(FALLBACK_PALETTE.hop2);
  });

  test("collapses every distance ≥ HOP_FAR_THRESHOLD to hopFar", () => {
    expect(colorForHop(HOP_FAR_THRESHOLD, FALLBACK_PALETTE)).toBe(
      FALLBACK_PALETTE.hopFar,
    );
    expect(colorForHop(4, FALLBACK_PALETTE)).toBe(FALLBACK_PALETTE.hopFar);
    expect(colorForHop(42, FALLBACK_PALETTE)).toBe(FALLBACK_PALETTE.hopFar);
  });

  test("returns hopUnreachable for +Infinity", () => {
    expect(colorForHop(Number.POSITIVE_INFINITY, FALLBACK_PALETTE)).toBe(
      FALLBACK_PALETTE.hopUnreachable,
    );
  });

  test("defensively returns hopUnreachable for NaN and negative inputs", () => {
    expect(colorForHop(Number.NaN, FALLBACK_PALETTE)).toBe(
      FALLBACK_PALETTE.hopUnreachable,
    );
    expect(colorForHop(-1, FALLBACK_PALETTE)).toBe(
      FALLBACK_PALETTE.hopUnreachable,
    );
  });

  test("HOP_FAR_THRESHOLD pins the spec boundary at 3", () => {
    // The spec calls for hop 0/1/2 to be distinct and everything from
    // 3 onward to collapse. Pin the constant so changing it requires
    // touching this test.
    expect(HOP_FAR_THRESHOLD).toBe(3);
  });
});

describe("describeHop", () => {
  test("renders 'origin' at distance 0", () => {
    expect(describeHop(0)).toBe("origin");
  });

  test("renders the single/two-hop labels", () => {
    expect(describeHop(1)).toBe("1 hop");
    expect(describeHop(2)).toBe("2 hops");
  });

  test("collapses ≥3 to '3+ hops'", () => {
    expect(describeHop(3)).toBe("3+ hops");
    expect(describeHop(7)).toBe("3+ hops");
  });

  test("renders 'unreachable' for Infinity and bad inputs", () => {
    expect(describeHop(Number.POSITIVE_INFINITY)).toBe("unreachable");
    expect(describeHop(Number.NaN)).toBe("unreachable");
    expect(describeHop(-1)).toBe("unreachable");
  });
});
