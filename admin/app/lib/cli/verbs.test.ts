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

import { parse } from "./parser";
import type { Command } from "./types";
import {
  CLI_COMMAND_REFERENCE,
  HELP_TEXT,
  parseAdd,
  parseBfs,
  parseCommunity,
  parseHelp,
  parsePagerank,
  parsePut,
  parseFloatStrict,
} from "./verbs";

/**
 * The canonical verb set, duplicated here (not imported) on purpose so the
 * test fails loudly if a verb is ever added to the parser without being
 * surfaced in the reference and `HELP_TEXT`. Mirrors `parser.ts`'s `VERBS`
 * and the Go `parser.Verbs`.
 */
const CANONICAL_VERBS = [
  "get",
  "put",
  "delete",
  "delete-prefix",
  "add",
  "scan",
  "count",
  "keys",
  "bfs",
  "pagerank",
  "community",
  "help",
  "exit",
] as const;

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

describe("parseAdd decaying-edge (#953 — client-side decay staircase)", () => {
  test("accepts the canonical 6-operand form", () => {
    const r = parseAdd([
      "decaying-edge",
      "alice",
      "bob",
      "16",
      "0.5",
      "5",
      "1",
    ]);
    expect(r.ok).toBe(true);
    if (r.ok) {
      expect(r.command).toEqual({
        verb: "add",
        objective: "decaying-edge",
        tail: "alice",
        head: "bob",
        initialWeight: 16,
        ratio: 0.5,
        steps: 5,
        intervalSeconds: 1,
      });
    }
  });

  test("validates operand types only — an out-of-range ratio still parses", () => {
    // Range enforcement (ratio ∈ (0,1), steps ∈ [1,MAX], interval > 0) is the
    // SDK's job; the parser must not reject a well-typed but out-of-range curve,
    // matching the Go parser which defers to client.DecayOpts.
    const r = parseAdd(["decaying-edge", "alice", "bob", "16", "2", "5", "1"]);
    expect(r.ok).toBe(true);
    if (
      r.ok &&
      r.command.verb === "add" &&
      r.command.objective === "decaying-edge"
    ) {
      expect(r.command.ratio).toBe(2);
    }
  });

  test("rejects a missing interval_seconds (5 operands)", () => {
    const r = parseAdd(["decaying-edge", "alice", "bob", "16", "0.5", "5"]);
    expect(r.ok).toBe(false);
  });

  test("rejects a trailing token after interval_seconds (7 operands)", () => {
    const r = parseAdd([
      "decaying-edge",
      "alice",
      "bob",
      "16",
      "0.5",
      "5",
      "1",
      "extra",
    ]);
    expect(r.ok).toBe(false);
  });

  test("rejects a non-numeric initial_weight", () => {
    const r = parseAdd([
      "decaying-edge",
      "alice",
      "bob",
      "notafloat",
      "0.5",
      "5",
      "1",
    ]);
    expect(r.ok).toBe(false);
  });

  test("rejects non-integer steps", () => {
    const r = parseAdd([
      "decaying-edge",
      "alice",
      "bob",
      "16",
      "0.5",
      "2.5",
      "1",
    ]);
    expect(r.ok).toBe(false);
  });

  test("rejects a fractional interval_seconds (bare integer of seconds)", () => {
    const r = parseAdd([
      "decaying-edge",
      "alice",
      "bob",
      "16",
      "0.5",
      "5",
      "1.5",
    ]);
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

describe("HELP_TEXT (#436 / #975 — grammar contract)", () => {
  test("enumerates the family verb kwarg names", () => {
    expect(HELP_TEXT).toContain("reduction=");
    expect(HELP_TEXT).toContain("objective=");
    expect(HELP_TEXT).toContain("weighting=");
    expect(HELP_TEXT).toContain("prefix=");
    expect(HELP_TEXT).toContain("restart_prob=");
    expect(HELP_TEXT).toContain("epsilon=");
  });

  test("enumerates the family verb kwarg valid values", () => {
    expect(HELP_TEXT).toContain("bfs");
    expect(HELP_TEXT).toContain("pagerank");
    expect(HELP_TEXT).toContain("community");
    expect(HELP_TEXT).toContain("none");
    expect(HELP_TEXT).toContain("mst");
    expect(HELP_TEXT).toContain("spt");
    expect(HELP_TEXT).toContain("min");
    expect(HELP_TEXT).toContain("max");
    expect(HELP_TEXT).toContain("raw");
    expect(HELP_TEXT).toContain("tfidf");
    expect(HELP_TEXT).toContain("bm25");
  });

  test("documents the family verb kwarg + positional defaults", () => {
    expect(HELP_TEXT).toContain("default=none");
    expect(HELP_TEXT).toContain("default=max");
    expect(HELP_TEXT).toContain("default=raw");
    expect(HELP_TEXT).toContain("default=all keys");
    // Per-verb positional defaults (#975).
    expect(HELP_TEXT).toContain("step=5");
    expect(HELP_TEXT).toContain("fan_out=3");
    expect(HELP_TEXT).toContain("top_n=10");
    expect(HELP_TEXT).toContain("max_size=0");
  });

  test("lists every verb (including help and exit)", () => {
    for (const verb of [
      "get",
      "put",
      "add",
      "delete",
      "scan",
      "keys",
      "bfs",
      "pagerank",
      "community",
      "help",
      "exit",
    ]) {
      expect(HELP_TEXT).toContain(verb);
    }
  });
});

describe("parseBfs prefix= kwarg (#606 / #975)", () => {
  function bfs(rest: string[]) {
    const r = parseBfs(rest);
    if (!r.ok) {
      throw new Error(`parse failed: ${r.usage}`);
    }
    if (r.command.verb !== "bfs") {
      throw new Error(`not a bfs command: ${r.command.verb}`);
    }
    return r.command;
  }

  test("captures a free-text prefix value verbatim (seed-only positional)", () => {
    expect(bfs(["alice", "prefix=team:"]).vertexPrefix).toBe("team:");
  });

  test("keeps the prefix value case (key folds, value is case-sensitive)", () => {
    expect(bfs(["alice", "2", "5", "PREFIX=Users/Alice"]).vertexPrefix).toBe(
      "Users/Alice",
    );
  });

  test("omitting prefix leaves it empty (no filter)", () => {
    expect(bfs(["alice", "2", "5"]).vertexPrefix).toBe("");
  });

  test("parses prefix= alongside the closed-set axes in any order", () => {
    const cmd = bfs([
      "alice",
      "2",
      "5",
      "prefix=users/",
      "reduction=spt",
      "objective=min",
    ]);
    expect(cmd.vertexPrefix).toBe("users/");
    expect(cmd.reduction).toBe("spt");
    expect(cmd.objective).toBe("min");
  });

  test("rejects an explicit empty prefix= value", () => {
    expect(parseBfs(["alice", "2", "5", "prefix="]).ok).toBe(false);
  });
});

describe("parsePagerank knobs (#801 / #975)", () => {
  function pagerank(rest: string[]) {
    const r = parsePagerank(rest);
    if (!r.ok) {
      throw new Error(`parse failed: ${r.usage}`);
    }
    if (r.command.verb !== "pagerank") {
      throw new Error(`not a pagerank command: ${r.command.verb}`);
    }
    return r.command;
  }

  test("defaults top_n to 10 and the knobs to 0 (server defaults)", () => {
    const cmd = pagerank(["alice"]);
    expect(cmd.topN).toBe(10);
    expect(cmd.restartProb).toBe(0);
    expect(cmd.epsilon).toBe(0);
  });

  test("parses restart_prob= and epsilon= floats in any order", () => {
    const cmd = pagerank(["alice", "5", "epsilon=0.001", "restart_prob=0.25"]);
    expect(cmd.topN).toBe(5);
    expect(cmd.restartProb).toBeCloseTo(0.25);
    expect(cmd.epsilon).toBeCloseTo(0.001);
  });

  test("accepts scientific-notation knob values (Go ParseFloat parity)", () => {
    const cmd = pagerank(["alice", "5", "epsilon=1e-4"]);
    expect(cmd.epsilon).toBeCloseTo(0.0001);
  });

  test("rejects a non-numeric restart_prob value", () => {
    expect(parsePagerank(["alice", "5", "restart_prob=high"]).ok).toBe(false);
  });

  test("rejects a non-numeric epsilon value", () => {
    expect(parsePagerank(["alice", "5", "epsilon=tiny"]).ok).toBe(false);
  });

  test("rejects reduction= / objective= (the relevance star is already a tree)", () => {
    expect(parsePagerank(["alice", "5", "reduction=mst"]).ok).toBe(false);
    expect(parsePagerank(["alice", "5", "objective=min"]).ok).toBe(false);
  });
});

describe("parseCommunity (#845 / #975)", () => {
  function community(rest: string[]) {
    const r = parseCommunity(rest);
    if (!r.ok) {
      throw new Error(`parse failed: ${r.usage}`);
    }
    if (r.command.verb !== "community") {
      throw new Error(`not a community command: ${r.command.verb}`);
    }
    return r.command;
  }

  test("defaults max_size to 0 (sweep decides) and the knobs to 0", () => {
    const cmd = community(["alice"]);
    expect(cmd.maxSize).toBe(0);
    expect(cmd.restartProb).toBe(0);
    expect(cmd.epsilon).toBe(0);
    expect(cmd.reduction).toBe("none");
    expect(cmd.objective).toBe("max");
  });

  test("captures the positional max_size plus the push + tree knobs", () => {
    const cmd = community([
      "alice",
      "20",
      "restart_prob=0.25",
      "epsilon=1e-4",
      "reduction=mst",
      "objective=min",
    ]);
    expect(cmd.maxSize).toBe(20);
    expect(cmd.restartProb).toBeCloseTo(0.25);
    expect(cmd.epsilon).toBeCloseTo(0.0001);
    expect(cmd.reduction).toBe("mst");
    expect(cmd.objective).toBe("min");
  });

  test("rejects an unknown kwarg (top_n=) and a second bare positional", () => {
    expect(parseCommunity(["alice", "20", "top_n=5"]).ok).toBe(false);
    expect(parseCommunity(["alice", "20", "30"]).ok).toBe(false);
  });
});

describe("CLI_COMMAND_REFERENCE (#646 — structured reference ⇄ parser binding)", () => {
  test("is non-empty", () => {
    expect(CLI_COMMAND_REFERENCE.length).toBeGreaterThan(0);
  });

  test("every entry's verb is the first token of its signature", () => {
    for (const doc of CLI_COMMAND_REFERENCE) {
      expect(doc.signature.split(/\s+/)[0]).toBe(doc.verb);
    }
  });

  test("every example parses successfully via the real parser", () => {
    for (const doc of CLI_COMMAND_REFERENCE) {
      const r = parse(doc.example);
      if (!r.ok) {
        throw new Error(`example did not parse: "${doc.example}" → ${r.usage}`);
      }
      // The parsed verb must match the documented verb so an example can
      // never silently document the wrong command. The parser narrows
      // `verb` to a literal union, so widen to string for the comparison.
      expect(r.command.verb as string).toBe(doc.verb);
    }
  });

  test("covers exactly the canonical verb set — no missing, no invented", () => {
    const referenced = new Set(CLI_COMMAND_REFERENCE.map((d) => d.verb));
    expect([...referenced].sort()).toEqual([...CANONICAL_VERBS].sort());
  });

  test("every referenced verb also appears in HELP_TEXT (cross-view parity)", () => {
    for (const verb of new Set(CLI_COMMAND_REFERENCE.map((d) => d.verb))) {
      expect(HELP_TEXT).toContain(verb);
    }
  });

  test("entries carry a non-empty group, summary, and example", () => {
    for (const doc of CLI_COMMAND_REFERENCE) {
      expect(doc.group.length).toBeGreaterThan(0);
      expect(doc.summary.length).toBeGreaterThan(0);
      expect(doc.example.length).toBeGreaterThan(0);
    }
  });
});

describe("#679 grammar — count / delete-prefix / batch delete / scan paging", () => {
  function ok(input: string): Command {
    const r = parse(input);
    if (!r.ok) throw new Error(`expected ok for ${input}: ${r.usage}`);
    return r.command;
  }

  test("delete vertex is variadic (one key → keys[1])", () => {
    expect(ok("delete vertex a")).toEqual({
      verb: "delete",
      objective: "vertex",
      keys: ["a"],
    });
  });

  test("delete vertex collects every key", () => {
    expect(ok("delete vertex a b c")).toEqual({
      verb: "delete",
      objective: "vertex",
      keys: ["a", "b", "c"],
    });
  });

  test("delete edge groups tokens into (tail, head) pairs", () => {
    expect(ok("delete edge a b c d")).toEqual({
      verb: "delete",
      objective: "edge",
      pairs: [
        { tail: "a", head: "b" },
        { tail: "c", head: "d" },
      ],
    });
  });

  test("delete edge rejects an odd token count", () => {
    expect(parse("delete edge a b c").ok).toBe(false);
  });

  test("scan vertices extracts the all kwarg and positional limit", () => {
    expect(ok("scan vertices users/ 100 all=true")).toEqual({
      verb: "scan",
      objective: "vertices",
      prefix: "users/",
      limit: 100,
      all: true,
    });
  });

  test("scan edges extracts head and all kwargs", () => {
    expect(ok("scan edges alice head=post: all=true")).toEqual({
      verb: "scan",
      objective: "edges",
      tailPrefix: "alice",
      headPrefix: "post:",
      limit: 0,
      all: true,
    });
  });

  test("scan rejects a non-boolean all and unknown kwargs", () => {
    expect(parse("scan vertices p all=maybe").ok).toBe(false);
    expect(parse("scan vertices p bogus=1").ok).toBe(false);
  });

  test("count vertices extracts the prefix", () => {
    expect(ok("count vertices users/")).toEqual({
      verb: "count",
      objective: "vertices",
      prefix: "users/",
    });
  });

  test("count rejects a missing or wrong objective", () => {
    expect(parse("count users/").ok).toBe(false);
    expect(parse("count edges x").ok).toBe(false);
  });

  test("delete-prefix requires exactly one of confirm=yes / dry_run=true", () => {
    expect(ok("delete-prefix vertices tmp/ confirm=yes")).toEqual({
      verb: "delete-prefix",
      objective: "vertices",
      prefix: "tmp/",
      limit: 0,
      dryRun: false,
      confirm: true,
    });
    expect(ok("delete-prefix vertices tmp/ dry_run=true limit=50")).toEqual({
      verb: "delete-prefix",
      objective: "vertices",
      prefix: "tmp/",
      limit: 50,
      dryRun: true,
      confirm: false,
    });
    expect(parse("delete-prefix vertices tmp/").ok).toBe(false);
    expect(
      parse("delete-prefix vertices tmp/ confirm=yes dry_run=true").ok,
    ).toBe(false);
    expect(parse("delete-prefix vertices tmp/ confirm=no").ok).toBe(false);
  });

  test("put vertex extracts the type= override and ttl in either order", () => {
    expect(ok("put vertex k 1234 type=int")).toEqual({
      verb: "put",
      objective: "vertex",
      key: "k",
      value: "1234",
      ttlSeconds: null,
      valueType: "int",
    });
    expect(ok("put vertex k v 60 type=string")).toEqual({
      verb: "put",
      objective: "vertex",
      key: "k",
      value: "v",
      ttlSeconds: 60,
      valueType: "string",
    });
    expect(ok("put vertex k v")).toEqual({
      verb: "put",
      objective: "vertex",
      key: "k",
      value: "v",
      ttlSeconds: null,
      valueType: "auto",
    });
  });

  test("put vertex rejects an unknown type and a bare type token", () => {
    expect(parse("put vertex k v type=bogus").ok).toBe(false);
    expect(parse("put vertex k v type").ok).toBe(false);
  });
});
