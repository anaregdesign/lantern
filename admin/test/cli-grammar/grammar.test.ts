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

const fixturePath = resolve(import.meta.dirname, "verbs.json");

interface FixtureCase {
  input: string;
  comment?: string;
}

interface Fixture {
  valid: FixtureCase[];
  invalid: FixtureCase[];
}

const fx = JSON.parse(readFileSync(fixturePath, "utf8")) as Fixture;

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
