/**
 * Unit tests for the verb parsers in `verbs.ts`.
 *
 * Scoped today to `parseFloatStrict` (#434) — the regression where the
 * admin CLI rejected literal float forms (`1e3`, `.5`, `-.5`, `5.`,
 * `1.5e-3`) that the Go REPL happily parses via `strconv.ParseFloat`.
 *
 * Also asserts that the `add edge` / `put edge` happy paths now accept
 * those forms (since `weight` is the only place `parseFloatStrict` is
 * invoked in production).
 */
import { describe, expect, test } from "bun:test";

import { parseAdd, parsePut, parseFloatStrict } from "./verbs";

describe("parseFloatStrict (#434 — Go strconv.ParseFloat parity)", () => {
  test.each([
    ["1e3", 1000],
    ["1.5e-3", 0.0015],
    ["0.5", 0.5],
    [".5", 0.5],
    ["-.5", -0.5],
    ["5.", 5],
    ["+2.5", 2.5],
    ["-1.25", -1.25],
    ["0", 0],
    ["1E+2", 100],
  ])("accepts %p → %p", (input, expected) => {
    expect(parseFloatStrict(input)).toBe(expected);
  });

  test.each([
    [""],
    ["abc"],
    ["abc1"],
    ["1abc"],
    ["nan"],
    ["NaN"],
    ["+nan"],
    ["inf"],
    ["+Inf"],
    ["-Inf"],
    ["infinity"],
    ["-Infinity"],
    [".."],
    ["."],
    ["1e"],
    ["1.5e"],
    ["e3"],
    ["1.5.5"],
  ])("rejects %p → null", (input) => {
    expect(parseFloatStrict(input)).toBeNull();
  });
});

describe("parseAdd / parsePut weight uses parseFloatStrict (#434)", () => {
  test("add edge accepts 1e3 as weight", () => {
    const r = parseAdd(["edge", "alice", "bob", "1e3"]);
    expect(r.ok).toBe(true);
    if (r.ok) {
      expect(r.command).toEqual({
        verb: "add",
        objective: "edge",
        tail: "alice",
        head: "bob",
        weight: 1000,
        ttlSeconds: expect.any(Number),
      });
    }
  });

  test("put edge accepts .5 as weight", () => {
    const r = parsePut(["edge", "alice", "bob", ".5"]);
    expect(r.ok).toBe(true);
    if (r.ok) {
      expect((r.command as { weight: number }).weight).toBe(0.5);
    }
  });

  test("add edge rejects NaN as weight", () => {
    const r = parseAdd(["edge", "alice", "bob", "NaN"]);
    expect(r.ok).toBe(false);
  });

  test("add edge rejects garbled token", () => {
    const r = parseAdd(["edge", "alice", "bob", "1abc"]);
    expect(r.ok).toBe(false);
  });
});
