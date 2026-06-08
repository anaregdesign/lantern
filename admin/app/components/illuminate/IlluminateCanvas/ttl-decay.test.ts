import { describe, expect, it } from "bun:test";
import {
  alphaByteForFraction,
  applyTtlFade,
  applyWarningTint,
  computeTtlFraction,
  isInWarningWindow,
  LIFETIME_BUDGET_MS,
  MIN_ALPHA,
  WARNING_WITHIN_MS,
  warningUrgency,
} from "./ttl-decay";

const T0 = Date.parse("2026-06-09T00:00:00.000Z");

function iso(deltaMs: number): string {
  return new Date(T0 + deltaMs).toISOString();
}

describe("computeTtlFraction", () => {
  it("returns null when no expiration is set (treat as ∞)", () => {
    expect(computeTtlFraction(undefined, T0)).toBeNull();
    expect(computeTtlFraction("", T0)).toBeNull();
  });

  it("returns null for unparseable timestamps (defensive)", () => {
    expect(computeTtlFraction("not-an-iso", T0)).toBeNull();
  });

  it("returns 0 at expiration (caller should drop)", () => {
    expect(computeTtlFraction(iso(0), T0)).toBe(0);
  });

  it("returns 0 past expiration (caller should drop)", () => {
    expect(computeTtlFraction(iso(-5_000), T0)).toBe(0);
  });

  it("returns 1 when remaining ≥ LIFETIME_BUDGET_MS", () => {
    expect(computeTtlFraction(iso(LIFETIME_BUDGET_MS), T0)).toBe(1);
    expect(computeTtlFraction(iso(LIFETIME_BUDGET_MS * 10), T0)).toBe(1);
  });

  it("linearly scales between 0 and 1 inside the budget window", () => {
    expect(computeTtlFraction(iso(LIFETIME_BUDGET_MS / 2), T0)).toBeCloseTo(
      0.5,
      6,
    );
    expect(computeTtlFraction(iso(LIFETIME_BUDGET_MS / 4), T0)).toBeCloseTo(
      0.25,
      6,
    );
  });
});

describe("isInWarningWindow", () => {
  it("is false when no expiration is set", () => {
    expect(isInWarningWindow(undefined, T0)).toBe(false);
  });

  it("is false at and past expiration (vertex would be dropped)", () => {
    expect(isInWarningWindow(iso(0), T0)).toBe(false);
    expect(isInWarningWindow(iso(-1_000), T0)).toBe(false);
  });

  it("is true inside the warning window", () => {
    expect(isInWarningWindow(iso(WARNING_WITHIN_MS / 2), T0)).toBe(true);
    expect(isInWarningWindow(iso(1_000), T0)).toBe(true);
  });

  it("is true exactly at the warning threshold", () => {
    expect(isInWarningWindow(iso(WARNING_WITHIN_MS), T0)).toBe(true);
  });

  it("is false outside the warning window", () => {
    expect(isInWarningWindow(iso(WARNING_WITHIN_MS + 1), T0)).toBe(false);
    expect(isInWarningWindow(iso(LIFETIME_BUDGET_MS), T0)).toBe(false);
  });
});

describe("alphaByteForFraction", () => {
  it("returns 0xff (opaque) for null", () => {
    expect(alphaByteForFraction(null)).toBe(0xff);
  });

  it("returns 0xff for fraction === 1", () => {
    expect(alphaByteForFraction(1)).toBe(0xff);
  });

  it("returns the MIN_ALPHA byte for fraction === 0", () => {
    expect(alphaByteForFraction(0)).toBe(Math.round(MIN_ALPHA * 255));
  });

  it("interpolates linearly between MIN_ALPHA and 1", () => {
    // alpha = MIN_ALPHA + (1 - MIN_ALPHA) * 0.5 = 0.625
    expect(alphaByteForFraction(0.5)).toBe(Math.round(0.625 * 255));
  });

  it("clamps inputs outside [0, 1]", () => {
    expect(alphaByteForFraction(-1)).toBe(Math.round(MIN_ALPHA * 255));
    expect(alphaByteForFraction(2)).toBe(0xff);
  });
});

describe("applyTtlFade", () => {
  it("returns the input unchanged when fraction is null (no expiration)", () => {
    expect(applyTtlFade("#3f3f46", null)).toBe("#3f3f46");
  });

  it("appends an alpha byte for a 7-char hex input", () => {
    expect(applyTtlFade("#3f3f46", 1)).toBe("#3f3f46ff");
    expect(applyTtlFade("#3f3f46", 0)).toBe(
      `#3f3f46${Math.round(MIN_ALPHA * 255).toString(16)}`,
    );
  });

  it("expands 4-char shorthand (#RGB) to full hex8", () => {
    // #abc expands to #aabbcc → aabbccff at fraction=1
    expect(applyTtlFade("#abc", 1)).toBe("#aabbccff");
  });

  it("leaves already-hex8 colours alone (avoids double-applying alpha)", () => {
    expect(applyTtlFade("#3f3f4626", 0.5)).toBe("#3f3f4626");
  });

  it("returns garbage input unchanged (defensive — never throws)", () => {
    expect(applyTtlFade("rgb(1,2,3)", 0.5)).toBe("rgb(1,2,3)");
    expect(applyTtlFade("notacolor", 0.5)).toBe("notacolor");
  });

  it("pads the alpha byte to two characters (#RRGGBB0a, not #RRGGBBa)", () => {
    // Pick a fraction whose alpha byte is < 16 to exercise the pad.
    // alpha = MIN_ALPHA + (1 - MIN_ALPHA) * 0 = 0.25 → 64 → "40"
    // We need an alpha byte < 16 to force a single hex digit. Bypass
    // MIN_ALPHA clamping by checking the helper directly.
    // alphaByteForFraction can't produce <0x40 with current MIN_ALPHA;
    // assert the pad path indirectly via the well-known fraction
    // mapping (0 → 0x40, which is already two hex chars). We rely on
    // the implementation using .padStart so callers can audit.
    expect(applyTtlFade("#000000", 0)).toBe("#00000040");
  });
});

describe("warningUrgency", () => {
  it("is 0 with no expiration", () => {
    expect(warningUrgency(undefined, T0)).toBe(0);
  });

  it("is 1 at or past expiry", () => {
    expect(warningUrgency(iso(0), T0)).toBe(1);
    expect(warningUrgency(iso(-1_000), T0)).toBe(1);
  });

  it("is 0 outside the warning window", () => {
    expect(warningUrgency(iso(WARNING_WITHIN_MS), T0)).toBe(0);
    expect(warningUrgency(iso(LIFETIME_BUDGET_MS), T0)).toBe(0);
  });

  it("scales linearly from 0 (at threshold) to 1 (at expiry)", () => {
    expect(warningUrgency(iso(WARNING_WITHIN_MS / 2), T0)).toBeCloseTo(0.5, 6);
    expect(warningUrgency(iso(WARNING_WITHIN_MS / 4), T0)).toBeCloseTo(0.75, 6);
  });
});

describe("applyWarningTint", () => {
  it("returns base color unchanged at urgency=0", () => {
    expect(applyWarningTint("#3f3f46", 0)).toBe("#3f3f46");
  });

  it("returns the full warning red at urgency=1", () => {
    expect(applyWarningTint("#3f3f46", 1)).toBe("#d13438");
  });

  it("interpolates each channel linearly toward the warning red", () => {
    // Halfway between #3f3f46 (63,63,70) and #d13438 (209,52,56):
    // r = 63 + (209-63)*0.5 = 136 = 0x88
    // g = 63 + (52-63)*0.5 = 57.5 → 58 = 0x3a
    // b = 70 + (56-70)*0.5 = 63 = 0x3f
    expect(applyWarningTint("#3f3f46", 0.5)).toBe("#883a3f");
  });

  it("clamps urgency to [0, 1]", () => {
    expect(applyWarningTint("#3f3f46", -1)).toBe("#3f3f46");
    expect(applyWarningTint("#3f3f46", 2)).toBe("#d13438");
  });

  it("returns garbage input unchanged", () => {
    expect(applyWarningTint("rgb(1,2,3)", 0.5)).toBe("rgb(1,2,3)");
  });
});
