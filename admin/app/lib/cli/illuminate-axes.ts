/**
 * Family-native state and command formatting for the CLI graph explorer.
 *
 * A picker selection writes a command the operator could type in the CLI, so
 * this module deliberately uses the CLI vocabulary rather than wire enums.
 * The traversal family is a discriminant: an impossible combination such as
 * PageRank plus a BFS step or tree reduction has no TypeScript representation.
 */

import type {
  AlgorithmName,
  ObjectiveName,
  ReductionName,
  WeightingName,
} from "./types";
import { quoteCliToken } from "./tokenise";
import { parseFloatStrict } from "./verbs";

export const CLI_CLICK_BFS_STEP_MIN = 1;
export const CLI_CLICK_BFS_STEP_MAX = 5;
export const CLI_CLICK_BFS_FAN_OUT_MIN = 1;
export const CLI_CLICK_BFS_FAN_OUT_MAX = 32;
export const CLI_CLICK_TOP_N_MIN = 0;
export const CLI_CLICK_TOP_N_MAX = 32;
export const CLI_CLICK_MAX_SIZE_MIN = 0;
export const CLI_CLICK_MAX_SIZE_MAX = 32;

interface SharedCliClickAxes {
  weighting: WeightingName;
  /** Empty means no frontier-prefix filter. */
  vertexPrefix: string;
}

interface TreeCliClickAxes extends SharedCliClickAxes {
  reduction: ReductionName;
  objective: ObjectiveName;
}

export interface BfsCliClickAxes extends TreeCliClickAxes {
  family: "bfs";
  step: number;
  fanOut: number;
}

export interface PagerankCliClickAxes extends SharedCliClickAxes {
  family: "pagerank";
  /** 0 returns every positive-mass vertex. */
  topN: number;
  /** 0 delegates to the server default. */
  restartProb: number;
  /** 0 delegates to the server default. */
  epsilon: number;
}

export interface CommunityCliClickAxes extends TreeCliClickAxes {
  family: "community";
  /** 0 leaves the conductance sweep to decide the community size. */
  maxSize: number;
  /** 0 delegates to the server default. */
  restartProb: number;
  /** 0 delegates to the server default. */
  epsilon: number;
}

export type CliClickAxes =
  | BfsCliClickAxes
  | PagerankCliClickAxes
  | CommunityCliClickAxes;

export interface CliClickAxesByFamily {
  bfs: BfsCliClickAxes;
  pagerank: PagerankCliClickAxes;
  community: CommunityCliClickAxes;
}

export interface CliClickPickerState {
  selectedFamily: AlgorithmName;
  families: CliClickAxesByFamily;
}

/**
 * Click defaults intentionally match the parser's family defaults: BFS 5/3,
 * PageRank top_n 10, and community max_size 0. The explicit zero values make
 * the two push families' sentinel semantics visible in generated commands.
 */
export const CLI_CLICK_AXIS_DEFAULTS: CliClickAxesByFamily = {
  bfs: {
    family: "bfs",
    step: 5,
    fanOut: 3,
    reduction: "none",
    objective: "max",
    weighting: "raw",
    vertexPrefix: "",
  },
  pagerank: {
    family: "pagerank",
    topN: 10,
    restartProb: 0,
    epsilon: 0,
    weighting: "raw",
    vertexPrefix: "",
  },
  community: {
    family: "community",
    maxSize: 0,
    restartProb: 0,
    epsilon: 0,
    reduction: "none",
    objective: "max",
    weighting: "raw",
    vertexPrefix: "",
  },
};

export function defaultCliClickPickerState(): CliClickPickerState {
  return {
    selectedFamily: "bfs",
    families: {
      bfs: { ...CLI_CLICK_AXIS_DEFAULTS.bfs },
      pagerank: { ...CLI_CLICK_AXIS_DEFAULTS.pagerank },
      community: { ...CLI_CLICK_AXIS_DEFAULTS.community },
    },
  };
}

/** Select a family without touching the other families' last-used values. */
export function selectCliClickFamily(
  state: CliClickPickerState,
  family: AlgorithmName,
): CliClickPickerState {
  return state.selectedFamily === family
    ? state
    : { ...state, selectedFamily: family };
}

/** Replace only the current family, refusing a stale cross-family update. */
export function replaceCliClickFamilyAxes(
  state: CliClickPickerState,
  axes: CliClickAxes,
): CliClickPickerState {
  if (state.selectedFamily !== axes.family) return state;
  return {
    ...state,
    families: { ...state.families, [axes.family]: axes },
  };
}

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
 * Formats a family-native click selection as a parser-accepted CLI command.
 * Positional defaults stay explicit: this makes the click preview describe
 * the exact request and keeps the zero sentinels visible for PageRank and
 * community.
 */
export function formatFamilyClick(seed: string, axes: CliClickAxes): string {
  const tokens = [axes.family, quoteCliToken(seed)];
  switch (axes.family) {
    case "bfs":
      tokens.push(String(axes.step), String(axes.fanOut));
      appendTreeTokens(tokens, axes, CLI_CLICK_AXIS_DEFAULTS.bfs);
      break;
    case "pagerank":
      tokens.push(String(axes.topN));
      appendSharedTokens(tokens, axes, CLI_CLICK_AXIS_DEFAULTS.pagerank);
      appendPushTokens(tokens, axes);
      break;
    case "community":
      tokens.push(String(axes.maxSize));
      appendTreeTokens(tokens, axes, CLI_CLICK_AXIS_DEFAULTS.community);
      appendPushTokens(tokens, axes);
      break;
  }
  return tokens.join(" ");
}

function appendTreeTokens(
  tokens: string[],
  axes: TreeCliClickAxes,
  defaults: TreeCliClickAxes,
): void {
  if (axes.reduction !== defaults.reduction) {
    tokens.push(`reduction=${axes.reduction}`);
  }
  if (axes.objective !== defaults.objective) {
    tokens.push(`objective=${axes.objective}`);
  }
  appendSharedTokens(tokens, axes, defaults);
}

function appendSharedTokens(
  tokens: string[],
  axes: SharedCliClickAxes,
  defaults: SharedCliClickAxes,
): void {
  if (axes.weighting !== defaults.weighting) {
    tokens.push(`weighting=${axes.weighting}`);
  }
  if (axes.vertexPrefix !== "") {
    tokens.push(quoteCliToken(`prefix=${axes.vertexPrefix}`));
  }
}

function appendPushTokens(
  tokens: string[],
  axes: Pick<PagerankCliClickAxes, "restartProb" | "epsilon">,
): void {
  if (axes.restartProb > 0) tokens.push(`restart_prob=${axes.restartProb}`);
  if (axes.epsilon > 0) tokens.push(`epsilon=${axes.epsilon}`);
}

/** A versioned snapshot replaces the retired flat localStorage keys. */
export const AXIS_STORAGE_KEYS = {
  state: "cli.click.families.v2",
  // Legacy flat-state keys. They are only read for one-time migration.
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

export interface LegacyAxisStorage {
  get(key: string): string | null;
}

/**
 * Reads the old flat picker state without assigning `k` a new meaning for
 * every family. Its value is migrated only to the family that was selected;
 * the other families receive their own documented defaults.
 */
export function migrateLegacyCliClickPickerState(
  storage: LegacyAxisStorage,
): CliClickPickerState {
  const selectedFamily =
    parseStoredAlgorithm(storage.get(AXIS_STORAGE_KEYS.algorithm)) ?? "bfs";
  const legacyStep =
    parseStoredStep(storage.get(AXIS_STORAGE_KEYS.step)) ??
    CLI_CLICK_AXIS_DEFAULTS.bfs.step;
  const legacyK = parseStoredK(storage.get(AXIS_STORAGE_KEYS.k));
  const reduction =
    parseStoredReduction(storage.get(AXIS_STORAGE_KEYS.reduction)) ?? "none";
  const objective =
    parseStoredObjective(storage.get(AXIS_STORAGE_KEYS.objective)) ?? "max";
  const weighting =
    parseStoredWeighting(storage.get(AXIS_STORAGE_KEYS.weighting)) ?? "raw";
  const vertexPrefix =
    parseStoredPrefix(storage.get(AXIS_STORAGE_KEYS.vertexPrefix)) ?? "";
  const restartProb =
    parseStoredRestartProb(storage.get(AXIS_STORAGE_KEYS.restartProb)) ?? 0;
  const epsilon =
    parseStoredEpsilon(storage.get(AXIS_STORAGE_KEYS.epsilon)) ?? 0;

  const state = defaultCliClickPickerState();
  state.selectedFamily = selectedFamily;
  state.families.bfs = {
    ...state.families.bfs,
    step: legacyStep,
    fanOut:
      selectedFamily === "bfs" && legacyK !== null
        ? legacyK
        : state.families.bfs.fanOut,
    reduction,
    objective,
    weighting,
    vertexPrefix,
  };
  state.families.pagerank = {
    ...state.families.pagerank,
    topN:
      selectedFamily === "pagerank" && legacyK !== null
        ? legacyK
        : state.families.pagerank.topN,
    restartProb,
    epsilon,
    weighting,
    vertexPrefix,
  };
  state.families.community = {
    ...state.families.community,
    maxSize:
      selectedFamily === "community" && legacyK !== null
        ? legacyK
        : state.families.community.maxSize,
    restartProb,
    epsilon,
    reduction,
    objective,
    weighting,
    vertexPrefix,
  };
  return state;
}

/** Returns null when the versioned snapshot is missing or malformed. */
export function parseStoredCliClickPickerState(
  raw: string | null,
): CliClickPickerState | null {
  if (raw === null) return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return null;
  }
  if (!isRecord(parsed) || parsed.version !== 2 || !isRecord(parsed.families)) {
    return null;
  }
  const selectedFamily = parseStoredAlgorithm(asString(parsed.selectedFamily));
  const bfs = parseBfsAxes(parsed.families.bfs);
  const pagerank = parsePagerankAxes(parsed.families.pagerank);
  const community = parseCommunityAxes(parsed.families.community);
  if (!selectedFamily || !bfs || !pagerank || !community) return null;
  return { selectedFamily, families: { bfs, pagerank, community } };
}

export function serialiseCliClickPickerState(
  state: CliClickPickerState,
): string {
  return JSON.stringify({ version: 2, ...state });
}

export function parseStoredStep(raw: string | null): number | null {
  return parseStoredInt(raw, CLI_CLICK_BFS_STEP_MIN, CLI_CLICK_BFS_STEP_MAX);
}

/** Parses the legacy shared k axis, whose old legal range was 1..32. */
export function parseStoredK(raw: string | null): number | null {
  return parseStoredInt(
    raw,
    CLI_CLICK_BFS_FAN_OUT_MIN,
    CLI_CLICK_BFS_FAN_OUT_MAX,
  );
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

export type PushKnobValidation =
  | { state: "default"; value: 0 }
  | { state: "valid"; value: number }
  | { state: "incomplete" }
  | { state: "invalid"; message: string };

export const RESTART_PROB_VALIDATION_MESSAGE =
  "restart_prob must be a float32 in (0, 1), or blank for the server default.";
export const EPSILON_VALIDATION_MESSAGE =
  "epsilon must be a positive float32, or blank for the server default.";

export function validateRestartProbInput(raw: string): PushKnobValidation {
  return validatePushKnob(
    raw,
    (value) => value > 0 && value < 1,
    RESTART_PROB_VALIDATION_MESSAGE,
  );
}

export function validateEpsilonInput(raw: string): PushKnobValidation {
  return validatePushKnob(
    raw,
    (value) => value > 0,
    EPSILON_VALIDATION_MESSAGE,
  );
}

export function isReadyPushKnob(
  validation: PushKnobValidation,
): validation is Extract<PushKnobValidation, { value: number }> {
  return validation.state === "default" || validation.state === "valid";
}

export function parseStoredRestartProb(raw: string | null): number | null {
  return parseStoredPushKnob(raw, validateRestartProbInput);
}

export function parseStoredEpsilon(raw: string | null): number | null {
  return parseStoredPushKnob(raw, validateEpsilonInput);
}

export function parseStoredPrefix(raw: string | null): string | null {
  return raw;
}

function parseBfsAxes(value: unknown): BfsCliClickAxes | null {
  if (!isRecord(value) || value.family !== "bfs") return null;
  const reduction = parseStoredReduction(asString(value.reduction));
  const objective = parseStoredObjective(asString(value.objective));
  const weighting = parseStoredWeighting(asString(value.weighting));
  const vertexPrefix = asString(value.vertexPrefix);
  const step = parseStoredInt(
    asString(value.step),
    CLI_CLICK_BFS_STEP_MIN,
    CLI_CLICK_BFS_STEP_MAX,
  );
  const fanOut = parseStoredInt(
    asString(value.fanOut),
    CLI_CLICK_BFS_FAN_OUT_MIN,
    CLI_CLICK_BFS_FAN_OUT_MAX,
  );
  if (
    !reduction ||
    !objective ||
    !weighting ||
    vertexPrefix === null ||
    !step ||
    !fanOut
  ) {
    return null;
  }
  return {
    family: "bfs",
    step,
    fanOut,
    reduction,
    objective,
    weighting,
    vertexPrefix,
  };
}

function parsePagerankAxes(value: unknown): PagerankCliClickAxes | null {
  if (!isRecord(value) || value.family !== "pagerank") return null;
  const weighting = parseStoredWeighting(asString(value.weighting));
  const vertexPrefix = asString(value.vertexPrefix);
  const topN = parseStoredInt(
    asString(value.topN),
    CLI_CLICK_TOP_N_MIN,
    CLI_CLICK_TOP_N_MAX,
  );
  const restartProb = parseStoredV2PushKnob(
    asString(value.restartProb),
    validateRestartProbInput,
  );
  const epsilon = parseStoredV2PushKnob(
    asString(value.epsilon),
    validateEpsilonInput,
  );
  if (
    !weighting ||
    vertexPrefix === null ||
    topN === null ||
    restartProb === null ||
    epsilon === null
  ) {
    return null;
  }
  return {
    family: "pagerank",
    topN,
    restartProb,
    epsilon,
    weighting,
    vertexPrefix,
  };
}

function parseCommunityAxes(value: unknown): CommunityCliClickAxes | null {
  if (!isRecord(value) || value.family !== "community") return null;
  const reduction = parseStoredReduction(asString(value.reduction));
  const objective = parseStoredObjective(asString(value.objective));
  const weighting = parseStoredWeighting(asString(value.weighting));
  const vertexPrefix = asString(value.vertexPrefix);
  const maxSize = parseStoredInt(
    asString(value.maxSize),
    CLI_CLICK_MAX_SIZE_MIN,
    CLI_CLICK_MAX_SIZE_MAX,
  );
  const restartProb = parseStoredV2PushKnob(
    asString(value.restartProb),
    validateRestartProbInput,
  );
  const epsilon = parseStoredV2PushKnob(
    asString(value.epsilon),
    validateEpsilonInput,
  );
  if (
    !reduction ||
    !objective ||
    !weighting ||
    vertexPrefix === null ||
    maxSize === null ||
    restartProb === null ||
    epsilon === null
  ) {
    return null;
  }
  return {
    family: "community",
    maxSize,
    restartProb,
    epsilon,
    reduction,
    objective,
    weighting,
    vertexPrefix,
  };
}

function validatePushKnob(
  raw: string,
  acceptsFloat32: (value: number) => boolean,
  message: string,
): PushKnobValidation {
  if (raw === "") return { state: "default", value: 0 };
  if (isIncompleteFloatDraft(raw)) return { state: "incomplete" };
  const value = parseFloatStrict(raw);
  if (value === null) return { state: "invalid", message };
  const wireValue = Math.fround(value);
  if (!Number.isFinite(wireValue) || !acceptsFloat32(wireValue)) {
    return { state: "invalid", message };
  }
  return { state: "valid", value };
}

function isIncompleteFloatDraft(raw: string): boolean {
  return (
    /^[+-]?$/.test(raw) ||
    /^[+-]?\.$/.test(raw) ||
    /^[+-]?\d+\.$/.test(raw) ||
    /^[+-]?(?:\d+\.?\d*|\.\d+)[eE][+-]?$/.test(raw)
  );
}

function parseStoredPushKnob(
  raw: string | null,
  validate: (value: string) => PushKnobValidation,
): number | null {
  if (raw === null) return null;
  const result = validate(raw);
  return isReadyPushKnob(result) ? result.value : null;
}

// The retired flat keys treated an explicit "0" as malformed. In the v2
// snapshot zero is an intentional, typed sentinel and is serialised as a JSON
// number, so accept it before applying the shared text-field validation.
function parseStoredV2PushKnob(
  raw: string | null,
  validate: (value: string) => PushKnobValidation,
): number | null {
  if (raw === "0") return 0;
  return parseStoredPushKnob(raw, validate);
}

function parseStoredInt(
  raw: string | null,
  lo: number,
  hi: number,
): number | null {
  if (raw === null || !/^[+-]?\d+$/.test(raw)) return null;
  const n = Number(raw);
  if (!Number.isSafeInteger(n) || n < lo || n > hi) return null;
  return n;
}

function matchOption<T extends string>(
  raw: string | null,
  options: ReadonlyArray<{ value: T }>,
): T | null {
  if (raw === null) return null;
  for (const option of options) {
    if (option.value === raw) return option.value;
  }
  return null;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

function asString(value: unknown): string | null {
  if (typeof value === "string") return value;
  if (typeof value === "number" && Number.isFinite(value)) return String(value);
  return null;
}
