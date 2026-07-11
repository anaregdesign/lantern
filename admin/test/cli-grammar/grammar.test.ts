/**
 * Drift-detection test for the shared CLI/REPL grammar fixture (#411).
 *
 * Loads `admin/test/cli-grammar/verbs.json` (the same file the Go
 * test under `cli/parser/grammar_fixture_test.go` reads) and checks
 * the TS parser agrees on which inputs are valid and which are not.
 *
 * If an entry is added to the fixture that the TS parser handles
 * differently from the Go parser, both sides go red on the next CI
 * run.
 */

import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { parse } from "~/lib/cli/parser";
import type { Command } from "~/lib/cli/types";

const fixturePath = resolve(import.meta.dirname, "verbs.json");

interface FixtureCase {
  input: string;
  comment?: string;
  readonly expected?: Record<string, unknown>;
}

interface Fixture {
  valid: FixtureCase[];
  invalid: FixtureCase[];
}

const fx = JSON.parse(readFileSync(fixturePath, "utf8")) as Fixture;

function float32Bits(value: number): number {
  const bytes = new ArrayBuffer(4);
  const view = new DataView(bytes);
  view.setFloat32(0, value, false);
  return view.getUint32(0, false);
}

function normalizeFamilyCommand(command: Command): Record<string, unknown> {
  switch (command.verb) {
    case "bfs":
      return {
        family: "bfs",
        seed: command.seed,
        step: command.step,
        fan_out: command.fanOut,
        reduction: command.reduction,
        objective: command.objective,
        weighting: command.weighting,
        prefix: command.vertexPrefix,
      };
    case "pagerank":
      return {
        family: "pagerank",
        seed: command.seed,
        top_n: command.topN,
        restart_prob_f32_bits: float32Bits(command.restartProb),
        epsilon_f32_bits: float32Bits(command.epsilon),
        weighting: command.weighting,
        prefix: command.vertexPrefix,
      };
    case "community":
      return {
        family: "community",
        seed: command.seed,
        max_size: command.maxSize,
        restart_prob_f32_bits: float32Bits(command.restartProb),
        epsilon_f32_bits: float32Bits(command.epsilon),
        reduction: command.reduction,
        objective: command.objective,
        weighting: command.weighting,
        prefix: command.vertexPrefix,
      };
    default:
      throw new Error(`expected family command, got ${command.verb}`);
  }
}

describe("CLI grammar fixture (#411) — valid inputs", () => {
  for (const c of fx.valid) {
    test(c.input, () => {
      const result = parse(c.input);
      if (!result.ok) {
        throw new Error(
          `parse(${JSON.stringify(c.input)}) failed: ${result.usage} (${c.comment ?? ""})`,
        );
      }
      expect(result.ok).toBe(true);
      if (["bfs", "pagerank", "community"].includes(result.command.verb)) {
        if (c.expected === undefined) {
          throw new Error(
            `family fixture ${JSON.stringify(c.input)} is missing its expected normalized AST`,
          );
        }
        expect(normalizeFamilyCommand(result.command)).toEqual(c.expected);
      }
    });
  }
});

describe("CLI grammar fixture (#411) — invalid inputs", () => {
  for (const c of fx.invalid) {
    test(c.input || "(empty)", () => {
      const result = parse(c.input);
      if (result.ok) {
        throw new Error(
          `parse(${JSON.stringify(c.input)}) accepted invalid input (${c.comment ?? ""})`,
        );
      }
      expect(result.ok).toBe(false);
    });
  }
});
