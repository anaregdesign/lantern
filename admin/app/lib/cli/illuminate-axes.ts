/**
 * Click-to-illuminate axis registry (#464).
 *
 * The /cli page lets an operator click a vertex on the canvas to fire
 * an `illuminate` command for that key. Pre-#464 the click was hard-
 * coded to `illuminate <key> 2 5`, so configuring the post-#410 axes
 * (algorithm / objective / weighting) required typing the long form
 * by hand every time. This module exposes the *CLI-vocabulary* axis
 * options the picker strip renders, plus the small pure formatter
 * that turns the picker state into the exact text that will be echoed
 * into the scrollback.
 *
 * Why CLI vocabulary and not the wire `ALGORITHM_*` enums?
 * The picker writes a *command string* into the CLI input buffer; the
 * same string a user could have typed. The Go REPL parser and the
 * TypeScript parser in `./verbs.ts` both accept `none|mst|spt` /
 * `min|max` / `raw|tfidf`. Using the wire enums here would mean the
 * picker echoes something the parsers reject, breaking the "every
 * command echoed is something I could have typed" invariant the /cli
 * page is built on.
 *
 * Defaults match `parseIlluminate` in `./verbs.ts` so that an
 * untouched picker formats to the canonical short form
 * `illuminate <seed> 2 5` — the regression guard documented in #439
 * (default click must remain stable byte-for-byte).
 */

import type { AlgorithmName, ObjectiveName, WeightingName } from "./types";

/** Bounds for the step axis. Matches the Illuminate wire toolbar. */
export const CLI_CLICK_STEP_MIN = 1;
export const CLI_CLICK_STEP_MAX = 5;
/** Bounds for the k axis. */
export const CLI_CLICK_K_MIN = 1;
export const CLI_CLICK_K_MAX = 32;

export interface CliClickAxes {
  step: number;
  k: number;
  algorithm: AlgorithmName;
  objective: ObjectiveName;
  weighting: WeightingName;
  /**
   * Free-text vertex-prefix filter (#604/#617). Restricts the click-driven
   * walk frontier to keys under this prefix; the seed is always kept as the
   * anchor even when it does not match. Empty means "no filter". Matched
   * against vertex keys verbatim (case-SENSITIVE), unlike the closed-set
   * axes above.
   */
  vertexPrefix: string;
}

/**
 * Defaults are wired to the same values `parseIlluminate` falls back
 * to when a long-form command omits the axis kwargs. Any drift here
 * silently breaks the regression guard in {@link formatIlluminateClick}.
 */
export const CLI_CLICK_AXIS_DEFAULTS: CliClickAxes = {
  step: 2,
  k: 5,
  algorithm: "none",
  // #560: the server resolves an unspecified objective to MAXIMIZE, and the
  // objective now also steers the per-hop top-k pruning. Defaulting the
  // picker to `max` keeps the bare click on the strongest neighbours
  // (the pre-#560 de-facto behaviour) instead of silently inverting to
  // the weakest once the click is dispatched.
  objective: "max",
  weighting: "raw",
  // Empty = no prefix filter, so the bare click stays byte-for-byte the
  // canonical short form `illuminate <seed> 2 5` (the #439 regression guard).
  vertexPrefix: "",
};

export const CLI_ALGORITHMS: ReadonlyArray<{
  value: AlgorithmName;
  label: string;
}> = [
  { value: "none", label: "None (raw subgraph)" },
  { value: "mst", label: "Spanning tree" },
  { value: "spt", label: "Shortest-path tree" },
];

export const CLI_OBJECTIVES: ReadonlyArray<{
  value: ObjectiveName;
  label: string;
}> = [
  { value: "min", label: "Minimize (cost)" },
  { value: "max", label: "Maximize (relevance)" },
];

export const CLI_WEIGHTINGS: ReadonlyArray<{
  value: WeightingName;
  label: string;
}> = [
  { value: "raw", label: "Raw" },
  { value: "tfidf", label: "TF-IDF" },
  { value: "bm25", label: "BM25" },
];

/**
 * Format a click-to-illuminate command for {@link seed}.
 *
 * Emits the short form `illuminate <seed> <step> <k>` when every axis
 * matches {@link CLI_CLICK_AXIS_DEFAULTS}. Appends the optional kwargs
 * in fixed order (`algorithm=` → `objective=` → `weighting=` → `prefix=`)
 * only for axes that diverge from the default; the fixed order keeps
 * scrollback snapshots deterministic and matches the parser's usage string.
 *
 * The function is intentionally pure so `bun:test` can round-trip it
 * through {@link parse} without a DOM.
 */
export function formatIlluminateClick(
  seed: string,
  axes: CliClickAxes,
): string {
  const tokens: string[] = [
    "illuminate",
    seed,
    String(axes.step),
    String(axes.k),
  ];
  if (axes.algorithm !== CLI_CLICK_AXIS_DEFAULTS.algorithm) {
    tokens.push(`algorithm=${axes.algorithm}`);
  }
  if (axes.objective !== CLI_CLICK_AXIS_DEFAULTS.objective) {
    tokens.push(`objective=${axes.objective}`);
  }
  if (axes.weighting !== CLI_CLICK_AXIS_DEFAULTS.weighting) {
    tokens.push(`weighting=${axes.weighting}`);
  }
  // #617: prefix is a FREE-TEXT axis, not a closed enum. Emit `prefix=<value>`
  // only when non-empty — the parser (and the Go REPL) reject an explicit empty
  // `prefix=`, and an empty prefix means "no filter" anyway, so the bare click
  // stays the canonical short form. The value is echoed verbatim (case-
  // SENSITIVE, #604); it carries no spaces because the CLI tokeniser splits on
  // whitespace.
  if (axes.vertexPrefix !== "") {
    tokens.push(`prefix=${axes.vertexPrefix}`);
  }
  return tokens.join(" ");
}

/**
 * localStorage keys for the picker strip. Namespaced under `cli.click.*`
 * so they coexist with `cli.splitRatio` from #465 without collision.
 */
export const AXIS_STORAGE_KEYS = {
  step: "cli.click.step",
  k: "cli.click.k",
  algorithm: "cli.click.algorithm",
  objective: "cli.click.objective",
  weighting: "cli.click.weighting",
  vertexPrefix: "cli.click.prefix",
} as const;

/**
 * Parse a step value previously written to localStorage. Returns null
 * for missing / malformed / out-of-range values; the caller is expected
 * to fall back to {@link CLI_CLICK_AXIS_DEFAULTS}.step.
 */
export function parseStoredStep(raw: string | null): number | null {
  return parseStoredInt(raw, CLI_CLICK_STEP_MIN, CLI_CLICK_STEP_MAX);
}

/** Like {@link parseStoredStep} but for the k axis. */
export function parseStoredK(raw: string | null): number | null {
  return parseStoredInt(raw, CLI_CLICK_K_MIN, CLI_CLICK_K_MAX);
}

export function parseStoredAlgorithm(raw: string | null): AlgorithmName | null {
  return matchOption(raw, CLI_ALGORITHMS);
}

export function parseStoredObjective(raw: string | null): ObjectiveName | null {
  return matchOption(raw, CLI_OBJECTIVES);
}

export function parseStoredWeighting(raw: string | null): WeightingName | null {
  return matchOption(raw, CLI_WEIGHTINGS);
}

/**
 * Parse a stored vertex prefix. Prefix is free text, so every non-null
 * string is valid (including ""); only a missing key returns null, letting
 * the caller fall back to {@link CLI_CLICK_AXIS_DEFAULTS}.vertexPrefix.
 */
export function parseStoredPrefix(raw: string | null): string | null {
  return raw;
}

/** Inverse of the parse helpers; numeric axes serialise as base-10 ints. */
export function formatStoredStep(value: number): string {
  return String(value);
}

export function formatStoredK(value: number): string {
  return String(value);
}

function parseStoredInt(
  raw: string | null,
  lo: number,
  hi: number,
): number | null {
  if (raw === null) return null;
  const n = Number.parseInt(raw, 10);
  if (!Number.isInteger(n)) return null;
  if (n < lo || n > hi) return null;
  return n;
}

function matchOption<T extends string>(
  raw: string | null,
  options: ReadonlyArray<{ value: T }>,
): T | null {
  if (raw === null) return null;
  for (const opt of options) {
    if (opt.value === raw) return opt.value;
  }
  return null;
}
