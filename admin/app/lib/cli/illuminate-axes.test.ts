/**
 * Unit tests for the click-to-illuminate axis registry (#464, #975).
 *
 * Covers two distinct guarantees:
 *
 * 1. `formatFamilyClick` emits the family verb (bfs / pagerank / community)
 *    with its positional walk-size args and only appends the diverging
 *    kwargs, in the fixed order `reduction=` → `objective=` → `weighting=` →
 *    `prefix=` → `restart_prob=` → `epsilon=`. The default-short-form case
 *    (`bfs alice 2 5`) is the regression guard for #439 — the byte-for-byte
 *    stable click string the canvas snapshot test depends on.
 *
 * 2. Every shape this formatter can produce is parseable by the
 *    shared CLI parser in `./parser`, and round-trips to the same
 *    axes. Without this, a divergence between the picker and
 *    the parser (e.g. casing, kwarg name, value vocabulary) would
 *    silently break "click echoes a command I could have typed".
 */
import { describe, expect, test } from "bun:test";

import { parse } from "./parser";
import {
  CLI_CLICK_AXIS_DEFAULTS,
  formatFamilyClick,
  formatStoredFloat,
  parseStoredAlgorithm,
  parseStoredEpsilon,
  parseStoredK,
  parseStoredObjective,
  parseStoredPrefix,
  parseStoredRestartProb,
  parseStoredReduction,
  parseStoredStep,
  parseStoredWeighting,
  type CliClickAxes,
  validateEpsilonInput,
  validateRestartProbInput,
} from "./illuminate-axes";

describe("formatFamilyClick", () => {
  test("default axes emit the byte-stable short form (#439)", () => {
    expect(formatFamilyClick("alice", CLI_CLICK_AXIS_DEFAULTS)).toBe(
      "bfs alice 2 5",
    );
  });

  test("only step changed → bumps positional, no kwargs", () => {
    expect(
      formatFamilyClick("alice", { ...CLI_CLICK_AXIS_DEFAULTS, step: 3 }),
    ).toBe("bfs alice 3 5");
  });

  test("only k changed → bumps positional, no kwargs", () => {
    expect(
      formatFamilyClick("alice", { ...CLI_CLICK_AXIS_DEFAULTS, k: 10 }),
    ).toBe("bfs alice 2 10");
  });

  test("only reduction off-default → single kwarg", () => {
    expect(
      formatFamilyClick("alice", {
        ...CLI_CLICK_AXIS_DEFAULTS,
        reduction: "spt",
      }),
    ).toBe("bfs alice 2 5 reduction=spt");
  });

  test("algorithm=community selects the community verb with a single positional", () => {
    expect(
      formatFamilyClick("alice", {
        ...CLI_CLICK_AXIS_DEFAULTS,
        algorithm: "community",
      }),
    ).toBe("community alice 5");
  });

  test("only objective off-default → single kwarg", () => {
    expect(
      formatFamilyClick("alice", {
        ...CLI_CLICK_AXIS_DEFAULTS,
        objective: "min",
      }),
    ).toBe("bfs alice 2 5 objective=min");
  });

  test("only weighting off-default → single kwarg", () => {
    expect(
      formatFamilyClick("alice", {
        ...CLI_CLICK_AXIS_DEFAULTS,
        weighting: "tfidf",
      }),
    ).toBe("bfs alice 2 5 weighting=tfidf");
  });

  test("only prefix off-default → single kwarg appended last", () => {
    expect(
      formatFamilyClick("alice", {
        ...CLI_CLICK_AXIS_DEFAULTS,
        vertexPrefix: "svc:",
      }),
    ).toBe("bfs alice 2 5 prefix=svc:");
  });

  test("prefix value is emitted verbatim (case-sensitive, #604)", () => {
    expect(
      formatFamilyClick("alice", {
        ...CLI_CLICK_AXIS_DEFAULTS,
        vertexPrefix: "Users/Alice",
      }),
    ).toBe("bfs alice 2 5 prefix=Users/Alice");
  });

  test("empty prefix emits no kwarg even with other axes off-default", () => {
    expect(
      formatFamilyClick("alice", {
        step: 3,
        k: 10,
        algorithm: "community",
        reduction: "spt",
        objective: "min",
        weighting: "tfidf",
        vertexPrefix: "",
        restartProb: 0,
        epsilon: 0,
      }),
    ).toBe("community alice 10 reduction=spt objective=min weighting=tfidf");
  });

  test("all axes off-default → fixed token order ending in prefix=", () => {
    expect(
      formatFamilyClick("alice", {
        step: 3,
        k: 10,
        algorithm: "community",
        reduction: "spt",
        objective: "min",
        weighting: "tfidf",
        vertexPrefix: "svc:",
        restartProb: 0,
        epsilon: 0,
      }),
    ).toBe(
      "community alice 10 reduction=spt objective=min weighting=tfidf prefix=svc:",
    );
  });

  // #801: the push knobs are gated on the pagerank/community families AND a
  // non-zero value; the bfs family never emits them.
  test("algorithm=pagerank alone emits the bare positional star", () => {
    expect(
      formatFamilyClick("alice", {
        ...CLI_CLICK_AXIS_DEFAULTS,
        algorithm: "pagerank",
      }),
    ).toBe("pagerank alice 5");
  });

  test("pagerank knobs append after the positional in fixed order", () => {
    expect(
      formatFamilyClick("alice", {
        ...CLI_CLICK_AXIS_DEFAULTS,
        algorithm: "pagerank",
        restartProb: 0.25,
        epsilon: 0.001,
      }),
    ).toBe("pagerank alice 5 restart_prob=0.25 epsilon=0.001");
  });

  test("push knobs are suppressed for the bfs family", () => {
    expect(
      formatFamilyClick("alice", {
        ...CLI_CLICK_AXIS_DEFAULTS,
        reduction: "spt",
        restartProb: 0.25,
        epsilon: 0.001,
      }),
    ).toBe("bfs alice 2 5 reduction=spt");
  });

  // #942: the community family shares the pagerank α/ε knobs and the same
  // non-zero emission gate, so the click formatter must reach it too.
  test("algorithm=community alone emits the bare positional", () => {
    expect(
      formatFamilyClick("alice", {
        ...CLI_CLICK_AXIS_DEFAULTS,
        algorithm: "community",
      }),
    ).toBe("community alice 5");
  });

  test("community knobs append after the positional in fixed order", () => {
    expect(
      formatFamilyClick("alice", {
        ...CLI_CLICK_AXIS_DEFAULTS,
        algorithm: "community",
        restartProb: 0.25,
        epsilon: 0.001,
      }),
    ).toBe("community alice 5 restart_prob=0.25 epsilon=0.001");
  });

  // #961: the reduction axis is honoured for the community family (an MST /
  // SPT tree rooted at the seed) and slots in right after the positional.
  test("community + reduction emits both, reduction after the positional", () => {
    expect(
      formatFamilyClick("alice", {
        ...CLI_CLICK_AXIS_DEFAULTS,
        algorithm: "community",
        reduction: "mst",
      }),
    ).toBe("community alice 5 reduction=mst");
  });

  // #961: pagerank renders a ranked vertex star, not a tree, so the reduction
  // axis is meaningless there and the formatter suppresses it.
  test("reduction is suppressed for the pagerank family", () => {
    expect(
      formatFamilyClick("alice", {
        ...CLI_CLICK_AXIS_DEFAULTS,
        algorithm: "pagerank",
        reduction: "spt",
      }),
    ).toBe("pagerank alice 5");
  });

  test("seed containing a colon round-trips literally", () => {
    expect(formatFamilyClick("user:alice", CLI_CLICK_AXIS_DEFAULTS)).toBe(
      "bfs user:alice 2 5",
    );
  });

  test("safe keys stay human-readable while literal percent escapes stay literal", () => {
    expect(formatFamilyClick("audit:%2F", CLI_CLICK_AXIS_DEFAULTS)).toBe(
      "bfs audit:%2F 2 5",
    );
  });

  test.each([
    "audit key",
    "audit:%2F",
    'say "hi"',
    "C:\\tmp\\key",
    "tab\tand\nnewline",
    "日本語",
  ])("serialises seed and prefix losslessly: %p", (value) => {
    const result = parse(
      formatFamilyClick(value, {
        ...CLI_CLICK_AXIS_DEFAULTS,
        vertexPrefix: value,
      }),
    );
    if (!result.ok) {
      throw new Error(`formatter rejected ${JSON.stringify(value)}`);
    }
    expect(result.command.verb).toBe("bfs");
    if (result.command.verb !== "bfs") return;
    expect(result.command.seed).toBe(value);
    expect(result.command.vertexPrefix).toBe(value);
  });
});

describe("formatFamilyClick ↔ parse round-trip", () => {
  const matrix: Array<{ name: string; axes: CliClickAxes }> = [
    { name: "all-default", axes: CLI_CLICK_AXIS_DEFAULTS },
    {
      name: "only step",
      axes: { ...CLI_CLICK_AXIS_DEFAULTS, step: 4 },
    },
    {
      name: "only k",
      axes: { ...CLI_CLICK_AXIS_DEFAULTS, k: 16 },
    },
    {
      name: "only reduction=mst",
      axes: { ...CLI_CLICK_AXIS_DEFAULTS, reduction: "mst" },
    },
    {
      name: "only reduction=spt",
      axes: { ...CLI_CLICK_AXIS_DEFAULTS, reduction: "spt" },
    },
    {
      name: "only algorithm=community",
      axes: { ...CLI_CLICK_AXIS_DEFAULTS, algorithm: "community" },
    },
    {
      name: "only objective=min",
      axes: { ...CLI_CLICK_AXIS_DEFAULTS, objective: "min" },
    },
    {
      name: "only weighting=tfidf",
      axes: { ...CLI_CLICK_AXIS_DEFAULTS, weighting: "tfidf" },
    },
    {
      name: "only prefix",
      axes: { ...CLI_CLICK_AXIS_DEFAULTS, vertexPrefix: "team:" },
    },
    {
      name: "all axes off-default",
      axes: {
        step: 3,
        k: 10,
        algorithm: "community",
        reduction: "spt",
        objective: "min",
        weighting: "tfidf",
        vertexPrefix: "svc:",
        restartProb: 0.3,
        epsilon: 0.002,
      },
    },
    {
      name: "pagerank with knobs",
      axes: {
        ...CLI_CLICK_AXIS_DEFAULTS,
        algorithm: "pagerank",
        restartProb: 0.25,
        epsilon: 0.001,
      },
    },
    {
      name: "community with knobs",
      axes: {
        ...CLI_CLICK_AXIS_DEFAULTS,
        algorithm: "community",
        restartProb: 0.3,
        epsilon: 0.002,
      },
    },
    {
      name: "bfs with reduction",
      axes: { ...CLI_CLICK_AXIS_DEFAULTS, reduction: "mst", objective: "min" },
    },
  ];

  test.each(matrix)("$name parses back to the same axes", ({ axes }) => {
    const text = formatFamilyClick("user:alice", axes);
    const result = parse(text);
    if (!result.ok) {
      throw new Error(
        `formatter produced unparseable text: ${JSON.stringify({ text, usage: result.usage })}`,
      );
    }
    // Since #975 the family is the verb itself, so each axis set round-trips
    // to a different Command shape carrying the family-specific fields.
    const cmd = result.command;
    expect(cmd.verb).toBe(axes.algorithm);
    if (cmd.verb === "bfs") {
      expect(cmd.seed).toBe("user:alice");
      expect(cmd.step).toBe(axes.step);
      expect(cmd.fanOut).toBe(axes.k);
      expect(cmd.reduction).toBe(axes.reduction);
      expect(cmd.objective).toBe(axes.objective);
      expect(cmd.weighting).toBe(axes.weighting);
      expect(cmd.vertexPrefix).toBe(axes.vertexPrefix);
    } else if (cmd.verb === "pagerank") {
      expect(cmd.seed).toBe("user:alice");
      // pagerank's single positional is top_n, sourced from the shared k axis.
      expect(cmd.topN).toBe(axes.k);
      expect(cmd.weighting).toBe(axes.weighting);
      expect(cmd.vertexPrefix).toBe(axes.vertexPrefix);
      // The parsed command carries the exact float32 values sent on the wire.
      expect(cmd.restartProb).toBe(Math.fround(axes.restartProb));
      expect(cmd.epsilon).toBe(Math.fround(axes.epsilon));
    } else if (cmd.verb === "community") {
      expect(cmd.seed).toBe("user:alice");
      // community's single positional is max_size, sourced from the k axis;
      // step has no meaning for this family and is dropped by the formatter.
      expect(cmd.maxSize).toBe(axes.k);
      expect(cmd.reduction).toBe(axes.reduction);
      expect(cmd.objective).toBe(axes.objective);
      expect(cmd.weighting).toBe(axes.weighting);
      expect(cmd.vertexPrefix).toBe(axes.vertexPrefix);
      // The parsed command carries the exact float32 values sent on the wire.
      expect(cmd.restartProb).toBe(Math.fround(axes.restartProb));
      expect(cmd.epsilon).toBe(Math.fround(axes.epsilon));
    } else {
      throw new Error(`unexpected verb from click formatter: ${cmd.verb}`);
    }
  });
});

describe("parseStored* helpers", () => {
  test("step accepts the documented range and rejects everything else", () => {
    expect(parseStoredStep("1")).toBe(1);
    expect(parseStoredStep("5")).toBe(5);
    expect(parseStoredStep("0")).toBeNull();
    expect(parseStoredStep("6")).toBeNull();
    expect(parseStoredStep("two")).toBeNull();
    expect(parseStoredStep(null)).toBeNull();
  });

  test("k accepts the documented range and rejects everything else", () => {
    expect(parseStoredK("1")).toBe(1);
    expect(parseStoredK("32")).toBe(32);
    expect(parseStoredK("0")).toBeNull();
    expect(parseStoredK("33")).toBeNull();
    expect(parseStoredK("five")).toBeNull();
    expect(parseStoredK(null)).toBeNull();
  });

  test("axis enums only accept canonical lower-case CLI vocabulary", () => {
    expect(parseStoredAlgorithm("bfs")).toBe("bfs");
    expect(parseStoredAlgorithm("pagerank")).toBe("pagerank");
    expect(parseStoredAlgorithm("community")).toBe("community");
    // The reduction values are no longer part of the algorithm axis (#961).
    expect(parseStoredAlgorithm("none")).toBeNull();
    expect(parseStoredAlgorithm("mst")).toBeNull();
    expect(parseStoredAlgorithm("spt")).toBeNull();
    expect(parseStoredAlgorithm("BFS")).toBeNull();
    expect(parseStoredAlgorithm("ALGORITHM_SHORTEST_PATH_TREE")).toBeNull();
    expect(parseStoredAlgorithm(null)).toBeNull();

    expect(parseStoredReduction("none")).toBe("none");
    expect(parseStoredReduction("mst")).toBe("mst");
    expect(parseStoredReduction("spt")).toBe("spt");
    // The family values are not reductions.
    expect(parseStoredReduction("bfs")).toBeNull();
    expect(parseStoredReduction("pagerank")).toBeNull();
    expect(parseStoredReduction("community")).toBeNull();
    expect(parseStoredReduction("SPT")).toBeNull();
    expect(parseStoredReduction("REDUCTION_SHORTEST_PATH_TREE")).toBeNull();
    expect(parseStoredReduction(null)).toBeNull();

    expect(parseStoredObjective("min")).toBe("min");
    expect(parseStoredObjective("max")).toBe("max");
    expect(parseStoredObjective("OBJECTIVE_MAXIMIZE")).toBeNull();

    expect(parseStoredWeighting("raw")).toBe("raw");
    expect(parseStoredWeighting("tfidf")).toBe("tfidf");
    expect(parseStoredWeighting("bm25")).toBe("bm25");
    expect(parseStoredWeighting("WEIGHTING_TFIDF")).toBeNull();
  });

  test("prefix is free text: any non-null string is valid, null stays null", () => {
    expect(parseStoredPrefix("svc:")).toBe("svc:");
    expect(parseStoredPrefix("Users/Alice")).toBe("Users/Alice");
    // Empty is a legitimate stored value (= no filter), distinct from a
    // missing key (null), which falls back to the default.
    expect(parseStoredPrefix("")).toBe("");
    expect(parseStoredPrefix(null)).toBeNull();
  });

  test("push-knob storage uses strict, family-specific float32 domains", () => {
    expect(parseStoredRestartProb("")).toBe(0);
    expect(parseStoredRestartProb("0.25")).toBe(0.25);
    expect(parseStoredEpsilon("")).toBe(0);
    expect(parseStoredEpsilon("1e-4")).toBe(0.0001);

    // Legacy zero values, out-of-domain values, prefix parses, and values
    // that change domain after float32 rounding must never hydrate.
    for (const raw of [
      "0",
      "1",
      "1.5",
      "-0.1",
      "0.25suffix",
      "0.99999999",
      "1e-50",
      "NaN",
      "Infinity",
      " 0.25",
      " ",
    ]) {
      expect(parseStoredRestartProb(raw)).toBeNull();
    }
    for (const raw of ["0", "-0.1", "1e-50", "0.25suffix"]) {
      expect(parseStoredEpsilon(raw)).toBeNull();
    }
    expect(parseStoredRestartProb(null)).toBeNull();
    expect(parseStoredEpsilon(null)).toBeNull();
  });

  test("formatStoredFloat writes blank defaults that both strict parsers restore", () => {
    expect(formatStoredFloat(0)).toBe("");
    for (const value of [0, 0.15, 0.25, 0.0001]) {
      const stored = formatStoredFloat(value);
      expect(parseStoredRestartProb(stored)).toBe(value);
      expect(parseStoredEpsilon(stored)).toBe(value);
    }
  });
});

describe("strict live push-knob validation", () => {
  test("blank alone commits the server default, while complete valid values commit", () => {
    expect(validateRestartProbInput("")).toEqual({
      state: "default",
      value: 0,
    });
    expect(validateRestartProbInput("0.15")).toEqual({
      state: "valid",
      value: 0.15,
    });
    expect(validateEpsilonInput("1e-4")).toEqual({
      state: "valid",
      value: 0.0001,
    });
  });

  test("incomplete decimal and scientific drafts stay intact without committing", () => {
    for (const raw of [".", "0.", "+", "1e", "1e-"]) {
      expect(validateRestartProbInput(raw)).toEqual({ state: "incomplete" });
    }
  });

  test("restart_prob rejects malformed and out-of-domain float32 values", () => {
    for (const raw of [
      "0",
      "1",
      "1.5",
      "-0.1",
      " ",
      "0.25suffix",
      "0.99999999", // rounds to exactly 1 as float32
      "1e-50", // underflows to exactly 0 as float32
    ]) {
      expect(validateRestartProbInput(raw).state).toBe("invalid");
    }
  });

  test("epsilon rejects zero, malformed, and float32 underflow", () => {
    for (const raw of ["0", "-0.1", "0.25suffix", "1e-50"]) {
      expect(validateEpsilonInput(raw).state).toBe("invalid");
    }
  });
});
