/**
 * Tests for the Web CLI Tab-completion engine (#515).
 *
 * `completeCommandLine` is the pure grammar core; these tests pin the
 * slot classification and candidate vocabularies so a parser/grammar
 * drift surfaces here rather than as a dead Tab key in the browser.
 */

import { describe, expect, test } from "bun:test";

import { completeCommandLine, longestCommonPrefix } from "./complete";

const KEYS = ["alice", "alaska", "bob", "Bobby", "carol"];

describe("completeCommandLine — verbs (slot 0)", () => {
  test("lists every verb for an empty buffer", () => {
    const { candidates } = completeCommandLine("", KEYS);
    expect(candidates).toEqual([
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
    ]);
  });

  test("filters verbs by prefix", () => {
    expect(completeCommandLine("ge", KEYS).candidates).toEqual(["get"]);
    expect(completeCommandLine("b", KEYS).candidates).toEqual(["bfs"]);
  });

  test("is case-insensitive on the verb", () => {
    expect(completeCommandLine("GE", KEYS).candidates).toEqual(["get"]);
  });

  test("reports the active token span", () => {
    const res = completeCommandLine("ge", KEYS);
    expect(res.token).toBe("ge");
    expect(res.start).toBe(0);
  });
});

describe("completeCommandLine — objectives (slot 1)", () => {
  test("get offers vertex/edge", () => {
    expect(completeCommandLine("get ", KEYS).candidates).toEqual([
      "vertex",
      "edge",
    ]);
  });

  test("add offers edge and decaying-edge", () => {
    expect(completeCommandLine("add ", KEYS).candidates).toEqual([
      "edge",
      "decaying-edge",
    ]);
  });

  test("add filters to decaying-edge by prefix", () => {
    expect(completeCommandLine("add d", KEYS).candidates).toEqual([
      "decaying-edge",
    ]);
  });

  test("scan offers the plural objectives", () => {
    expect(completeCommandLine("scan ", KEYS).candidates).toEqual([
      "vertices",
      "edges",
    ]);
  });

  test("filters the objective by prefix", () => {
    expect(completeCommandLine("get v", KEYS).candidates).toEqual(["vertex"]);
    expect(completeCommandLine("scan e", KEYS).candidates).toEqual(["edges"]);
  });

  test("active token span points at the objective", () => {
    const res = completeCommandLine("get v", KEYS);
    expect(res.token).toBe("v");
    expect(res.start).toBe("get ".length);
  });
});

describe("completeCommandLine — key slots", () => {
  test("get vertex completes the key from knownKeys", () => {
    expect(completeCommandLine("get vertex al", KEYS).candidates).toEqual([
      "alice",
      "alaska",
    ]);
  });

  test("key matching is case-insensitive but preserves stored casing", () => {
    expect(completeCommandLine("get vertex bob", KEYS).candidates).toEqual([
      "bob",
      "Bobby",
    ]);
  });

  test("edge verbs complete both endpoints", () => {
    expect(completeCommandLine("add edge al", KEYS).candidates).toEqual([
      "alice",
      "alaska",
    ]);
    expect(completeCommandLine("add edge alice b", KEYS).candidates).toEqual([
      "bob",
      "Bobby",
    ]);
  });

  test("delete edge completes both endpoints", () => {
    expect(
      completeCommandLine("delete edge alice ca", KEYS).candidates,
    ).toEqual(["carol"]);
  });

  test("scan completes its prefix slot from known keys", () => {
    expect(completeCommandLine("scan vertices al", KEYS).candidates).toEqual([
      "alice",
      "alaska",
    ]);
  });

  test("put vertex value slot offers nothing", () => {
    expect(completeCommandLine("put vertex alice ", KEYS).candidates).toEqual(
      [],
    );
  });

  test("bfs seed (slot 1) completes from known keys", () => {
    expect(completeCommandLine("bfs al", KEYS).candidates).toEqual([
      "alice",
      "alaska",
    ]);
  });

  test("caps key candidates at fifty", () => {
    const many = Array.from({ length: 80 }, (_, i) => `node${i}`);
    const { candidates } = completeCommandLine("get vertex node", many);
    expect(candidates).toHaveLength(50);
  });
});

describe("completeCommandLine — family option kwargs (slot ≥ 2)", () => {
  test("bfs offers its option keys right after the seed", () => {
    expect(completeCommandLine("bfs alice ", KEYS).candidates).toEqual([
      "step=",
      "fan_out=",
      "reduction=",
      "objective=",
      "weighting=",
      "prefix=",
    ]);
  });

  test("pagerank offers its own option keys", () => {
    expect(completeCommandLine("pagerank alice ", KEYS).candidates).toEqual([
      "top_n=",
      "restart_prob=",
      "epsilon=",
      "weighting=",
      "prefix=",
    ]);
  });

  test("community offers its own option keys", () => {
    expect(completeCommandLine("community alice ", KEYS).candidates).toEqual([
      "max_size=",
      "restart_prob=",
      "epsilon=",
      "reduction=",
      "objective=",
      "weighting=",
      "prefix=",
    ]);
  });

  test("a positional walk-size arg still surfaces the kwarg keys", () => {
    // step/fan_out are positional too, but the completer offers the whole
    // kwarg namespace for discoverability (#975); a bare int removes no key
    // because it never matches a key name.
    expect(completeCommandLine("bfs alice 2 ", KEYS).candidates).toEqual([
      "step=",
      "fan_out=",
      "reduction=",
      "objective=",
      "weighting=",
      "prefix=",
    ]);
  });

  test("filters option keys by prefix", () => {
    expect(completeCommandLine("bfs alice re", KEYS).candidates).toEqual([
      "reduction=",
    ]);
  });

  test("drops option keys already present on the line", () => {
    expect(
      completeCommandLine("bfs alice reduction=mst ", KEYS).candidates,
    ).toEqual(["step=", "fan_out=", "objective=", "weighting=", "prefix="]);
  });

  test("completes reduction values once = is typed", () => {
    expect(
      completeCommandLine("bfs alice reduction=", KEYS).candidates,
    ).toEqual(["reduction=none", "reduction=mst", "reduction=spt"]);
  });

  test("filters enum values by their prefix", () => {
    expect(
      completeCommandLine("bfs alice objective=m", KEYS).candidates,
    ).toEqual(["objective=min", "objective=max"]);
    expect(
      completeCommandLine("bfs alice weighting=tf", KEYS).candidates,
    ).toEqual(["weighting=tfidf"]);
    expect(
      completeCommandLine("bfs alice weighting=b", KEYS).candidates,
    ).toEqual(["weighting=bm25"]);
  });

  test("enumerates every weighting value once = is typed", () => {
    expect(
      completeCommandLine("bfs alice weighting=", KEYS).candidates,
    ).toEqual(["weighting=raw", "weighting=tfidf", "weighting=bm25"]);
  });

  test("unknown option keyword yields no value candidates", () => {
    expect(completeCommandLine("bfs alice bogus=", KEYS).candidates).toEqual(
      [],
    );
  });

  test("free-text prefix= yields no value candidates (#606)", () => {
    expect(completeCommandLine("bfs alice prefix=", KEYS).candidates).toEqual(
      [],
    );
  });

  test("free-form push knobs yield no value candidates (#801)", () => {
    expect(
      completeCommandLine("pagerank alice restart_prob=", KEYS).candidates,
    ).toEqual([]);
    expect(
      completeCommandLine("community alice epsilon=", KEYS).candidates,
    ).toEqual([]);
  });
});

describe("longestCommonPrefix", () => {
  test("returns the shared lead of the candidates", () => {
    expect(longestCommonPrefix(["alice", "alaska"])).toBe("al");
    expect(longestCommonPrefix(["reduction=", "objective="])).toBe("");
  });

  test("returns the single value unchanged", () => {
    expect(longestCommonPrefix(["alice"])).toBe("alice");
  });

  test("returns empty for no candidates", () => {
    expect(longestCommonPrefix([])).toBe("");
  });
});
