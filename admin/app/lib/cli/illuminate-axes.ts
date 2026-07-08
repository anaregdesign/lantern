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
 * Why CLI vocabulary and not the wire enums?
 * The picker writes a *command string* into the CLI input buffer; the
 * same string a user could have typed. The Go REPL parser and the
 * TypeScript parser in `./verbs.ts` both accept the family axis
 * `bfs|ppr|community`, the orthogonal reduction axis `none|mst|spt`
 * (#961), plus `min|max` / `raw|tfidf|bm25`. Using the wire enums here
 * would mean the picker echoes something the parsers reject, breaking the
 * "every command echoed is something I could have typed" invariant the
 * /cli page is built on.
 *
 * Defaults match `parseIlluminate` in `./verbs.ts` so that an
 * untouched picker formats to the canonical short form
 * `illuminate <seed> 2 5` — the regression guard documented in #439
 * (default click must remain stable byte-for-byte).
 */

import type {
  AlgorithmName,
  ObjectiveName,
  ReductionName,
  WeightingName,
} from "./types";

/** Bounds for the step axis. Matches the Illuminate wire toolbar. */
export const CLI_CLICK_STEP_MIN = 1;
export const CLI_CLICK_STEP_MAX = 5;
/** Bounds for the k axis. */
export const CLI_CLICK_K_MIN = 1;
export const CLI_CLICK_K_MAX = 32;

export interface CliClickAxes {
  step: number;
  k: number;
  /**
   * Traversal FAMILY (#961): `bfs` (default), `ppr`, or `community`.
   * Orthogonal to {@link reduction}.
   */
  algorithm: AlgorithmName;
  /**
   * Post-traversal tree REDUCTION (#961): `none` (default), `mst`, or `spt`.
   * Honoured for the `bfs` and `community` families and ignored for `ppr`;
   * {@link formatFamilyClick} only echoes it for a family that uses it.
   */
  reduction: ReductionName;
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
  /**
   * Push-family locality knobs (#801/#942), meaningful when
   * `algorithm === "ppr"` or `algorithm === "community"`. `restartProb` is
   * the restart/teleport-to-seed probability α in (0,1); `epsilon` is the
   * forward-push residual threshold ε > 0. 0 means "leave to the server
   * default" (α=0.15 / ε=1e-4) and is the value that keeps the bare click
   * byte-for-byte the canonical short form — {@link formatFamilyClick}
   * only emits these for a non-zero ppr/community walk.
   */
  restartProb: number;
  epsilon: number;
}

/**
 * Defaults are wired to the same values `parseIlluminate` falls back
 * to when a long-form command omits the axis kwargs. Any drift here
 * silently breaks the regression guard in {@link formatIlluminateClick}.
 */
export const CLI_CLICK_AXIS_DEFAULTS: CliClickAxes = {
  step: 2,
  k: 5,
  algorithm: "bfs",
  // #961: no tree reduction by default — the bare click renders the raw
  // discovered subgraph. Kept in lockstep with `parseIlluminate`.
  reduction: "none",
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
  // 0 = "use the server default" for both push knobs; the formatter omits
  // them unless algorithm=ppr|community AND the value is non-zero, so the
  // bare click is unaffected (#439 / #801 / #942).
  restartProb: 0,
  epsilon: 0,
};

export const CLI_ALGORITHMS: ReadonlyArray<{
  value: AlgorithmName;
  label: string;
}> = [
  { value: "bfs", label: "BFS (per-hop top-k)" },
  { value: "pagerank", label: "Personalized PageRank" },
  { value: "community", label: "Local community" },
];

export const CLI_REDUCTIONS: ReadonlyArray<{
  value: ReductionName;
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
 * Format a click-to-illuminate command for {@link seed} as a family verb
 * (#975). The verb is `axes.algorithm` (bfs / pagerank / community): bfs emits
 * positional `<step> <fan_out>`, while pagerank / community emit a single
 * positional count (top_n / max_size), all sourced from `axes.k` (the shared
 * "count" axis). Optional kwargs are appended in fixed order only when they
 * diverge from {@link CLI_CLICK_AXIS_DEFAULTS}: `reduction=` / `objective=`
 * (bfs & community only — pagerank's relevance star is already a tree),
 * `weighting=`, `prefix=`, then the α/ε push knobs (pagerank / community only,
 * and only when non-zero). The fixed order keeps scrollback snapshots
 * deterministic and every echoed line is one the parser accepts.
 *
 * The function is intentionally pure so `bun:test` can round-trip it through
 * {@link parse} without a DOM.
 */
export function formatFamilyClick(seed: string, axes: CliClickAxes): string {
  const verb = axes.algorithm;
  const tokens: string[] = [verb, seed];
  // Positional walk-size args: bfs takes step + fan_out; pagerank / community
  // take a single count (top_n / max_size). `axes.k` is the shared count axis.
  if (verb === "bfs") {
    tokens.push(String(axes.step), String(axes.k));
  } else {
    tokens.push(String(axes.k));
  }
  // reduction / objective apply to bfs and community (pagerank returns a
  // relevance star with no tree view). Emit only when non-default so a bare
  // click stays byte-stable (#439) and we never echo a knob pagerank ignores.
  if (verb !== "pagerank") {
    if (axes.reduction !== CLI_CLICK_AXIS_DEFAULTS.reduction) {
      tokens.push(`reduction=${axes.reduction}`);
    }
    if (axes.objective !== CLI_CLICK_AXIS_DEFAULTS.objective) {
      tokens.push(`objective=${axes.objective}`);
    }
  }
  if (axes.weighting !== CLI_CLICK_AXIS_DEFAULTS.weighting) {
    tokens.push(`weighting=${axes.weighting}`);
  }
  // #617: prefix is a FREE-TEXT axis. Emit `prefix=<value>` only when non-empty
  // — the parser rejects an explicit empty `prefix=`, and an empty prefix means
  // "no filter" anyway, so the bare click stays the canonical short form. The
  // value is echoed verbatim (case-SENSITIVE, #604).
  if (axes.vertexPrefix !== "") {
    tokens.push(`prefix=${axes.vertexPrefix}`);
  }
  // #801/#942: the α/ε knobs are meaningful for the two push-based families
  // (pagerank and community — both carry the same `restart_prob`/`epsilon`
  // locality knobs) and only when the operator has overridden the server
  // default (0). Gating on both keeps the bare click byte-stable (#439) and
  // never emits a knob the server ignores for the bfs family.
  if (verb === "pagerank" || verb === "community") {
    if (axes.restartProb > 0) {
      tokens.push(`restart_prob=${axes.restartProb}`);
    }
    if (axes.epsilon > 0) {
      tokens.push(`epsilon=${axes.epsilon}`);
    }
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
  reduction: "cli.click.reduction",
  objective: "cli.click.objective",
  weighting: "cli.click.weighting",
  vertexPrefix: "cli.click.prefix",
  restartProb: "cli.click.restart_prob",
  epsilon: "cli.click.epsilon",
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

export function parseStoredReduction(raw: string | null): ReductionName | null {
  return matchOption(raw, CLI_REDUCTIONS);
}

export function parseStoredObjective(raw: string | null): ObjectiveName | null {
  return matchOption(raw, CLI_OBJECTIVES);
}

export function parseStoredWeighting(raw: string | null): WeightingName | null {
  return matchOption(raw, CLI_WEIGHTINGS);
}

/**
 * Parse a stored PPR knob (restart_prob / epsilon). Accepts any finite,
 * non-negative number — 0 means "use the server default". Returns null for
 * missing / malformed / negative values so the caller falls back to the
 * documented default (0). Kept lenient (the server is the final authority on
 * the (0,1) / >0 bounds) but never resurrects a NaN or a negative knob (#801).
 */
export function parseStoredFloat(raw: string | null): number | null {
  if (raw === null) return null;
  const n = Number.parseFloat(raw);
  if (!Number.isFinite(n)) return null;
  if (n < 0) return null;
  return n;
}

/**
 * Interpret a raw push-knob (restart_prob / epsilon) input string into the
 * number to COMMIT to the axis during live editing (#801).
 *
 * The CliAxisPicker knob fields keep the operator's raw keystrokes as the
 * displayed value — so a half-typed "0." or "1e-" survives instead of being
 * round-tripped through the numeric axis (where 0 = "server default" renders
 * blank) and erased mid-edit — and feed every keystroke through this helper to
 * decide what, if anything, to persist:
 *   - blank / whitespace → 0, i.e. "leave the knob at the server default";
 *   - a finite, non-negative parse (incl. "0", "0.", "0.15", "1e-4") → that
 *     number, committed so the live preview updates;
 *   - anything else (negative, NaN, "abc", a lone "-") → null, meaning "keep the
 *     previously committed value" so a stray keystroke never destroys a good knob.
 *
 * This is the live-edit sibling of {@link parseStoredFloat} (which guards
 * localStorage hydration). Both stay lenient about the (0,1) / >0 bounds — the
 * server remains the final authority — but neither ever resurrects a NaN or a
 * negative knob.
 */
export function interpretPushKnobInput(raw: string): number | null {
  if (raw.trim() === "") return 0;
  const n = Number.parseFloat(raw);
  if (!Number.isFinite(n) || n < 0) return null;
  return n;
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

/** Serialise a PPR knob for localStorage; base-10, mirrors {@link parseStoredFloat}. */
export function formatStoredFloat(value: number): string {
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
