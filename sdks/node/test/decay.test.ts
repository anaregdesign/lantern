/**
 * Pure unit tests for the client-side geometric decay helpers (#953),
 * mirroring the Go SDK's `decay_test.go` vectors. The `addDecayingEdge`
 * wire round-trip (one expanded `addEdges` batch, returned post-add live
 * weight) lives in `client.test.ts` alongside the other transport tests.
 */

import { describe, expect, test } from "bun:test";

import { MAX_DECAY_STEPS, decayContributions, halfLifeDecay } from "../src/decay.js";
import { InvalidArgumentError } from "../src/errors.js";
import type { DecayOptions } from "../src/index.js";

const BASE_MS = Date.UTC(2024, 0, 1, 0, 0, 0);

describe("decayContributions", () => {
  test("golden staircase initialWeight=16 ratio=0.5 steps=5 → drops 8,4,2,1,1", () => {
    const opts: DecayOptions = { initialWeight: 16, ratio: 0.5, steps: 5, intervalSeconds: 1 };
    const got = decayContributions("a", "b", opts, BASE_MS);
    const wantW = [8, 4, 2, 1, 1];
    expect(got.length).toBe(wantW.length);
    let sum = 0;
    got.forEach((e, i) => {
      expect(e.tail).toBe("a");
      expect(e.head).toBe("b");
      expect(e.weight).toBeCloseTo(wantW[i], 4);
      // Contribution j (1-indexed) expires at base + j*intervalSeconds.
      expect(e.expiration?.getTime()).toBe(BASE_MS + (i + 1) * 1000);
      sum += e.weight;
    });
    // Telescoping: the live sum right after the add is initialWeight (16),
    // NOT the sum of a raw 16,8,4,2,1 schedule (31).
    expect(sum).toBeCloseTo(16, 4);
  });

  test("telescoping sum equals initialWeight across several curves", () => {
    const cases: DecayOptions[] = [
      { initialWeight: 100, ratio: 0.9, steps: 16, intervalSeconds: 1 },
      { initialWeight: 7, ratio: 0.3, steps: 4, intervalSeconds: 60 },
      { initialWeight: 1, ratio: 0.5, steps: 1, intervalSeconds: 1 },
    ];
    for (const opts of cases) {
      const got = decayContributions("x", "y", opts, BASE_MS);
      const sum = got.reduce((acc, e) => acc + e.weight, 0);
      const tol = Math.max(Math.abs(opts.initialWeight) * 1e-4, 1e-4);
      expect(Math.abs(sum - opts.initialWeight)).toBeLessThanOrEqual(tol);
    }
  });

  test("steps===1 is a single addEdge(initialWeight)", () => {
    const opts: DecayOptions = { initialWeight: 5, ratio: 0.5, steps: 1, intervalSeconds: 2 };
    const got = decayContributions("a", "b", opts, BASE_MS);
    expect(got.length).toBe(1);
    expect(got[0].weight).toBeCloseTo(5, 6);
    expect(got[0].expiration?.getTime()).toBe(BASE_MS + 2000);
  });

  test("negative initialWeight yields negative drops summing to it", () => {
    const opts: DecayOptions = { initialWeight: -16, ratio: 0.5, steps: 5, intervalSeconds: 1 };
    const got = decayContributions("a", "b", opts, BASE_MS);
    let sum = 0;
    for (const e of got) {
      expect(e.weight).toBeLessThan(0);
      sum += e.weight;
    }
    expect(sum).toBeCloseTo(-16, 4);
  });

  test("sub-second fractional interval is honoured", () => {
    const opts: DecayOptions = { initialWeight: 4, ratio: 0.5, steps: 2, intervalSeconds: 0.5 };
    const got = decayContributions("a", "b", opts, BASE_MS);
    expect(got.length).toBe(2);
    expect(got[0].expiration?.getTime()).toBe(BASE_MS + 500);
    expect(got[1].expiration?.getTime()).toBe(BASE_MS + 1000);
  });

  test("whole-curve underflow is rejected", () => {
    // Smallest positive float32 subnormal is ~1.4e-45; halving it underflows
    // every contribution to zero, so the whole curve is rejected.
    const opts: DecayOptions = {
      initialWeight: 1.401298464324817e-45,
      ratio: 0.5,
      steps: 5,
      intervalSeconds: 1,
    };
    expect(() => decayContributions("a", "b", opts, BASE_MS)).toThrow(InvalidArgumentError);
  });

  test("deep-curve underflow skips zero contributions (shorter than steps)", () => {
    // A fast decay from a tiny initial weight: the leading drops survive as
    // float32 subnormals, the deepest ones round to zero and are dropped, so
    // the returned array is non-empty but shorter than steps — and carries no
    // zero weights.
    const opts: DecayOptions = { initialWeight: 1e-44, ratio: 0.5, steps: 16, intervalSeconds: 1 };
    const got = decayContributions("a", "b", opts, BASE_MS);
    expect(got.length).toBeGreaterThan(0);
    expect(got.length).toBeLessThan(opts.steps);
    for (const e of got) expect(e.weight).not.toBe(0);
  });
});

describe("decayContributions validation", () => {
  const valid: DecayOptions = { initialWeight: 16, ratio: 0.5, steps: 5, intervalSeconds: 1 };
  const table: { name: string; opts: DecayOptions }[] = [
    { name: "zero initialWeight", opts: { ...valid, initialWeight: 0 } },
    { name: "non-finite initialWeight", opts: { ...valid, initialWeight: Number.NaN } },
    { name: "ratio zero", opts: { ...valid, ratio: 0 } },
    { name: "ratio one", opts: { ...valid, ratio: 1 } },
    { name: "ratio above one", opts: { ...valid, ratio: 1.5 } },
    { name: "ratio negative", opts: { ...valid, ratio: -0.5 } },
    { name: "steps zero", opts: { ...valid, steps: 0 } },
    { name: "steps non-integer", opts: { ...valid, steps: 2.5 } },
    { name: "steps above cap", opts: { ...valid, steps: MAX_DECAY_STEPS + 1 } },
    { name: "interval zero", opts: { ...valid, intervalSeconds: 0 } },
    { name: "interval negative", opts: { ...valid, intervalSeconds: -1 } },
  ];
  for (const tc of table) {
    test(`rejects ${tc.name}`, () => {
      expect(() => decayContributions("a", "b", tc.opts, BASE_MS)).toThrow(InvalidArgumentError);
    });
  }

  test("accepts the well-formed baseline", () => {
    expect(() => decayContributions("a", "b", valid, BASE_MS)).not.toThrow();
  });
});

describe("halfLifeDecay", () => {
  test("interval == halfLife gives ratio 0.5", () => {
    const opts = halfLifeDecay(10, 1, 1, 5);
    expect(opts.ratio).toBeCloseTo(0.5, 6);
    expect(opts.steps).toBe(5);
  });

  test("interval == 2*halfLife gives ratio 0.25", () => {
    const opts = halfLifeDecay(10, 1, 2, 4);
    expect(opts.ratio).toBeCloseTo(0.25, 6);
    expect(opts.steps).toBe(2);
  });

  test("steps clamp to MAX_DECAY_STEPS", () => {
    const opts = halfLifeDecay(10, 1, 1, 1000);
    expect(opts.steps).toBe(MAX_DECAY_STEPS);
  });

  test("non-positive horizon clamps to 1 step", () => {
    const opts = halfLifeDecay(10, 1, 1, 0);
    expect(opts.steps).toBe(1);
  });

  test("result validates and expands", () => {
    const opts = halfLifeDecay(64, 60, 30, 180);
    const got = decayContributions("a", "b", opts, BASE_MS);
    expect(got.length).toBeGreaterThan(0);
  });
});
