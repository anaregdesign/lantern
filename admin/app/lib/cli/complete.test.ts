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
      "illuminate",
      "help",
      "exit",
    ]);
  });

  test("filters verbs by prefix", () => {
    expect(completeCommandLine("ge", KEYS).candidates).toEqual(["get"]);
    expect(completeCommandLine("i", KEYS).candidates).toEqual(["illuminate"]);
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

  test("illuminate seed (slot 1) completes from known keys", () => {
    expect(completeCommandLine("illuminate al", KEYS).candidates).toEqual([
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

describe("completeCommandLine — illuminate option kwargs (slot ≥ 4)", () => {
  test("offers every option key with a trailing =", () => {
    expect(
      completeCommandLine("illuminate alice 2 5 ", KEYS).candidates,
    ).toEqual([
      "algorithm=",
      "reduction=",
      "objective=",
      "weighting=",
      "prefix=",
      "restart_prob=",
      "epsilon=",
    ]);
  });

  test("filters option keys by prefix", () => {
    expect(
      completeCommandLine("illuminate alice 2 5 a", KEYS).candidates,
    ).toEqual(["algorithm="]);
  });

  test("drops option keys already present on the line", () => {
    expect(
      completeCommandLine("illuminate alice 2 5 algorithm=community ", KEYS)
        .candidates,
    ).toEqual([
      "reduction=",
      "objective=",
      "weighting=",
      "prefix=",
      "restart_prob=",
      "epsilon=",
    ]);
  });

  test("completes algorithm (family) values once = is typed", () => {
    expect(
      completeCommandLine("illuminate alice 2 5 algorithm=", KEYS).candidates,
    ).toEqual(["algorithm=bfs", "algorithm=ppr", "algorithm=community"]);
  });

  test("completes reduction values once = is typed", () => {
    expect(
      completeCommandLine("illuminate alice 2 5 reduction=", KEYS).candidates,
    ).toEqual(["reduction=none", "reduction=mst", "reduction=spt"]);
  });

  test("filters enum values by their prefix", () => {
    expect(
      completeCommandLine("illuminate alice 2 5 objective=m", KEYS).candidates,
    ).toEqual(["objective=min", "objective=max"]);
    expect(
      completeCommandLine("illuminate alice 2 5 weighting=tf", KEYS).candidates,
    ).toEqual(["weighting=tfidf"]);
    expect(
      completeCommandLine("illuminate alice 2 5 weighting=b", KEYS).candidates,
    ).toEqual(["weighting=bm25"]);
  });

  test("enumerates every weighting value once = is typed", () => {
    expect(
      completeCommandLine("illuminate alice 2 5 weighting=", KEYS).candidates,
    ).toEqual(["weighting=raw", "weighting=tfidf", "weighting=bm25"]);
  });

  test("unknown option keyword yields no value candidates", () => {
    expect(
      completeCommandLine("illuminate alice 2 5 bogus=", KEYS).candidates,
    ).toEqual([]);
  });

  test("free-text prefix= yields no value candidates (#606)", () => {
    expect(
      completeCommandLine("illuminate alice 2 5 prefix=", KEYS).candidates,
    ).toEqual([]);
  });

  test("free-form ppr knobs yield no value candidates (#801)", () => {
    expect(
      completeCommandLine("illuminate alice 2 5 restart_prob=", KEYS)
        .candidates,
    ).toEqual([]);
    expect(
      completeCommandLine("illuminate alice 2 5 epsilon=", KEYS).candidates,
    ).toEqual([]);
  });

  test("illuminate step/k slots offer nothing", () => {
    expect(completeCommandLine("illuminate alice ", KEYS).candidates).toEqual(
      [],
    );
    expect(completeCommandLine("illuminate alice 2 ", KEYS).candidates).toEqual(
      [],
    );
  });
});

describe("longestCommonPrefix", () => {
  test("returns the shared lead of the candidates", () => {
    expect(longestCommonPrefix(["alice", "alaska"])).toBe("al");
    expect(longestCommonPrefix(["algorithm=", "objective="])).toBe("");
  });

  test("returns the single value unchanged", () => {
    expect(longestCommonPrefix(["alice"])).toBe("alice");
  });

  test("returns empty for no candidates", () => {
    expect(longestCommonPrefix([])).toBe("");
  });
});
