/**
 * Unit tests for the click-to-illuminate axis registry (#464).
 *
 * Covers two distinct guarantees:
 *
 * 1. `formatIlluminateClick` emits the short form when every axis
 *    matches {@link CLI_CLICK_AXIS_DEFAULTS} and only appends the
 *    diverging kwargs, in the fixed order
 *    `algorithm=` → `objective=` → `weighting=`. The default-short-form
 *    case is the regression guard for #439 — the byte-for-byte stable
 *    click string the canvas snapshot test depends on.
 *
 * 2. Every shape this formatter can produce is parseable by the
 *    shared CLI parser in `./parser`, and round-trips to the same
 *    axis triple. Without this, a divergence between the picker and
 *    the parser (e.g. casing, kwarg name, value vocabulary) would
 *    silently break "click echoes a command I could have typed".
 */
import { describe, expect, test } from "bun:test";

import { parse } from "./parser";
import {
  CLI_CLICK_AXIS_DEFAULTS,
  formatIlluminateClick,
  parseStoredAlgorithm,
  parseStoredK,
  parseStoredObjective,
  parseStoredStep,
  parseStoredWeighting,
  type CliClickAxes,
} from "./illuminate-axes";

describe("formatIlluminateClick", () => {
  test("default axes emit the byte-stable short form (#439)", () => {
    expect(formatIlluminateClick("alice", CLI_CLICK_AXIS_DEFAULTS)).toBe(
      "illuminate alice 2 5",
    );
  });

  test("only step changed → bumps positional, no kwargs", () => {
    expect(
      formatIlluminateClick("alice", { ...CLI_CLICK_AXIS_DEFAULTS, step: 3 }),
    ).toBe("illuminate alice 3 5");
  });

  test("only k changed → bumps positional, no kwargs", () => {
    expect(
      formatIlluminateClick("alice", { ...CLI_CLICK_AXIS_DEFAULTS, k: 10 }),
    ).toBe("illuminate alice 2 10");
  });

  test("only algorithm off-default → single kwarg", () => {
    expect(
      formatIlluminateClick("alice", {
        ...CLI_CLICK_AXIS_DEFAULTS,
        algorithm: "spt",
      }),
    ).toBe("illuminate alice 2 5 algorithm=spt");
  });

  test("only objective off-default → single kwarg", () => {
    expect(
      formatIlluminateClick("alice", {
        ...CLI_CLICK_AXIS_DEFAULTS,
        objective: "min",
      }),
    ).toBe("illuminate alice 2 5 objective=min");
  });

  test("only weighting off-default → single kwarg", () => {
    expect(
      formatIlluminateClick("alice", {
        ...CLI_CLICK_AXIS_DEFAULTS,
        weighting: "tfidf",
      }),
    ).toBe("illuminate alice 2 5 weighting=tfidf");
  });

  test("all axes off-default → fixed token order", () => {
    expect(
      formatIlluminateClick("alice", {
        step: 3,
        k: 10,
        algorithm: "spt",
        objective: "min",
        weighting: "tfidf",
      }),
    ).toBe("illuminate alice 3 10 algorithm=spt objective=min weighting=tfidf");
  });

  test("seed containing a colon round-trips literally", () => {
    expect(formatIlluminateClick("user:alice", CLI_CLICK_AXIS_DEFAULTS)).toBe(
      "illuminate user:alice 2 5",
    );
  });
});

describe("formatIlluminateClick ↔ parse round-trip", () => {
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
      name: "only algorithm=mst",
      axes: { ...CLI_CLICK_AXIS_DEFAULTS, algorithm: "mst" },
    },
    {
      name: "only algorithm=spt",
      axes: { ...CLI_CLICK_AXIS_DEFAULTS, algorithm: "spt" },
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
      name: "all axes off-default",
      axes: {
        step: 3,
        k: 10,
        algorithm: "spt",
        objective: "min",
        weighting: "tfidf",
      },
    },
  ];

  test.each(matrix)("$name parses back to the same axes", ({ axes }) => {
    const text = formatIlluminateClick("user:alice", axes);
    const result = parse(text);
    if (!result.ok) {
      throw new Error(
        `formatter produced unparseable text: ${JSON.stringify({ text, usage: result.usage })}`,
      );
    }
    expect(result.command.verb).toBe("illuminate");
    if (result.command.verb !== "illuminate") return;
    expect(result.command.seed).toBe("user:alice");
    expect(result.command.step).toBe(axes.step);
    expect(result.command.k).toBe(axes.k);
    expect(result.command.algorithm).toBe(axes.algorithm);
    expect(result.command.objective).toBe(axes.objective);
    expect(result.command.weighting).toBe(axes.weighting);
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
    expect(parseStoredAlgorithm("none")).toBe("none");
    expect(parseStoredAlgorithm("mst")).toBe("mst");
    expect(parseStoredAlgorithm("spt")).toBe("spt");
    expect(parseStoredAlgorithm("SPT")).toBeNull();
    expect(parseStoredAlgorithm("ALGORITHM_SHORTEST_PATH_TREE")).toBeNull();
    expect(parseStoredAlgorithm(null)).toBeNull();

    expect(parseStoredObjective("min")).toBe("min");
    expect(parseStoredObjective("max")).toBe("max");
    expect(parseStoredObjective("OBJECTIVE_MAXIMIZE")).toBeNull();

    expect(parseStoredWeighting("raw")).toBe("raw");
    expect(parseStoredWeighting("tfidf")).toBe("tfidf");
    expect(parseStoredWeighting("WEIGHTING_TFIDF")).toBeNull();
  });
});
