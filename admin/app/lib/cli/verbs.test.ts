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

import {
  HELP_TEXT,
  parseAdd,
  parseHelp,
  parseIlluminate,
  parsePut,
  parseFloatStrict,
} from "./verbs";

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
        ttlSeconds: null,
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

describe("parseHelp (#436 — help verb)", () => {
  test("bare `help` returns a help command", () => {
    const r = parseHelp([]);
    expect(r.ok).toBe(true);
    if (r.ok) {
      expect(r.command).toEqual({ verb: "help" });
    }
  });

  test("extra args accepted silently (mirrors exit)", () => {
    const r = parseHelp(["me"]);
    expect(r.ok).toBe(true);
    if (r.ok) {
      expect(r.command).toEqual({ verb: "help" });
    }
  });
});

describe("HELP_TEXT (#436 — grammar contract)", () => {
  test("enumerates illuminate kwarg names", () => {
    expect(HELP_TEXT).toContain("algorithm=");
    expect(HELP_TEXT).toContain("objective=");
    expect(HELP_TEXT).toContain("weighting=");
    expect(HELP_TEXT).toContain("prefix=");
  });

  test("enumerates illuminate kwarg valid values", () => {
    expect(HELP_TEXT).toContain("none");
    expect(HELP_TEXT).toContain("mst");
    expect(HELP_TEXT).toContain("spt");
    expect(HELP_TEXT).toContain("min");
    expect(HELP_TEXT).toContain("max");
    expect(HELP_TEXT).toContain("raw");
    expect(HELP_TEXT).toContain("tfidf");
  });

  test("documents illuminate kwarg defaults", () => {
    expect(HELP_TEXT).toContain("default=none");
    expect(HELP_TEXT).toContain("default=max");
    expect(HELP_TEXT).toContain("default=raw");
    expect(HELP_TEXT).toContain("default=all keys");
  });

  test("lists every verb (including help and exit)", () => {
    for (const verb of [
      "get",
      "put",
      "add",
      "delete",
      "scan",
      "illuminate",
      "help",
      "exit",
    ]) {
      expect(HELP_TEXT).toContain(verb);
    }
  });
});

describe("parseIlluminate prefix= kwarg (#606)", () => {
  function illuminate(rest: string[]) {
    const r = parseIlluminate(rest);
    if (!r.ok) {
      throw new Error(`parse failed: ${r.usage}`);
    }
    if (r.command.verb !== "illuminate") {
      throw new Error(`not an illuminate command: ${r.command.verb}`);
    }
    return r.command;
  }

  test("captures a free-text prefix value verbatim", () => {
    expect(illuminate(["alice", "2", "5", "prefix=team:"]).vertexPrefix).toBe(
      "team:",
    );
  });

  test("keeps the prefix value case (key folds, value is case-sensitive)", () => {
    expect(
      illuminate(["alice", "2", "5", "PREFIX=Users/Alice"]).vertexPrefix,
    ).toBe("Users/Alice");
  });

  test("omitting prefix leaves it empty (no filter)", () => {
    expect(illuminate(["alice", "2", "5"]).vertexPrefix).toBe("");
  });

  test("parses prefix= alongside the closed-set axes in any order", () => {
    const cmd = illuminate([
      "alice",
      "2",
      "5",
      "prefix=users/",
      "algorithm=spt",
      "objective=min",
    ]);
    expect(cmd.vertexPrefix).toBe("users/");
    expect(cmd.algorithm).toBe("spt");
    expect(cmd.objective).toBe("min");
  });

  test("rejects an explicit empty prefix= value", () => {
    expect(parseIlluminate(["alice", "2", "5", "prefix="]).ok).toBe(false);
  });
});
