/**
 * Tab-completion engine for the admin Web CLI (#515).
 *
 * The /cli prompt is a real inline terminal line, and a terminal without
 * Tab completion feels broken. `completeCommandLine` is the pure core
 * that powers it: given the current input buffer and a best-effort set of
 * known vertex keys, it returns the candidates for the *active token*
 * (the token the cursor is sitting in — i.e. the last whitespace-
 * delimited chunk, or an empty token when the buffer ends in whitespace).
 *
 * It is deliberately decoupled from React: `CliPage` owns the keyboard
 * wiring (preventDefault, applying the single/longest-common-prefix
 * completion, rendering the candidate hint), while this module owns the
 * grammar knowledge so it can be unit-tested without a DOM. The verb /
 * objective / illuminate-axis vocabularies are the same ones the parser
 * (`./verbs.ts`, `./types.ts`) and the click-to-illuminate picker
 * (`./illuminate-axes.ts`) accept, so a completed line is always a line
 * the parser would also accept.
 */

import {
  CLI_ALGORITHMS,
  CLI_OBJECTIVES,
  CLI_WEIGHTINGS,
} from "./illuminate-axes";

/**
 * Every verb the parser dispatches (see the `Command` union in
 * `./types.ts`). `help` / `exit` are completion targets too — they are
 * valid lines a user can type at the prompt.
 */
const VERBS: readonly string[] = [
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
];

/** The illuminate option kwargs, in the parser's fixed order. */
const ILLUMINATE_OPTION_KEYS: readonly string[] = [
  "algorithm",
  "objective",
  "weighting",
  // #606: free-text vertex-prefix filter. Surfaced as a completion key so
  // operators discover it, but it has no enum values to complete after `=`.
  "prefix",
  // #801: Personalized PageRank knobs. Free-form floats (no enum values to
  // complete after `=`), surfaced as keys so operators discover them.
  "restart_prob",
  "epsilon",
];

/** Cap on how many key candidates we surface, so the hint stays compact. */
const MAX_KEY_CANDIDATES = 50;

/**
 * The result of a completion request. `candidates` are the full
 * replacement tokens for the active token; `start` is the index in the
 * input where the active token begins (so the caller replaces
 * `input.slice(start)`); `token` is the active token's current text.
 */
export interface Completion {
  candidates: string[];
  start: number;
  token: string;
}

/**
 * Compute completion candidates for the active token of {@link input}.
 *
 * Slot semantics (0-based, whitespace-delimited):
 *   - slot 0 → verb
 *   - slot 1 → the verb's objective (`vertex`/`edge`/`vertices`/`edges`)
 *     for verbs that take one, or the seed key for `illuminate`
 *   - key slots → completed from {@link knownKeys}
 *   - `illuminate` slots ≥ 4 → option kwargs (`algorithm=` / `objective=`
 *     / `weighting=` / `prefix=` / `restart_prob=` / `epsilon=`, then the
 *     closed-set enum values once `=` is typed)
 */
export function completeCommandLine(
  input: string,
  knownKeys: readonly string[],
): Completion {
  const trailingWs = /\s$/.test(input);
  const tokens = input.split(/\s+/).filter((t) => t.length > 0);
  const slotIndex = trailingWs ? tokens.length : Math.max(0, tokens.length - 1);
  const token = trailingWs ? "" : (tokens[tokens.length - 1] ?? "");
  const start = input.length - token.length;
  const verb = (tokens[0] ?? "").toLowerCase();
  const objective = (tokens[1] ?? "").toLowerCase();

  const none: Completion = { candidates: [], start, token };

  // slot 0 — the verb.
  if (slotIndex === 0) {
    return { candidates: filterByPrefix(VERBS, token), start, token };
  }

  // illuminate option kwargs come before the generic slot handling so a
  // long illuminate line (slot ≥ 4) is routed to the axis vocabulary.
  if (verb === "illuminate" && slotIndex >= 4) {
    return {
      candidates: completeIlluminateOption(tokens, token),
      start,
      token,
    };
  }

  // slot 1 — objective for verbs that take one, else the illuminate seed.
  if (slotIndex === 1) {
    const objectives = objectivesFor(verb);
    if (objectives) {
      return { candidates: filterByPrefix(objectives, token), start, token };
    }
    if (verb === "illuminate" || verb === "keys") {
      return { candidates: completeKeys(knownKeys, token), start, token };
    }
    return none;
  }

  // Remaining key-bearing slots (tail/head endpoints, scan prefixes).
  if (isKeySlot(verb, objective, slotIndex)) {
    return { candidates: completeKeys(knownKeys, token), start, token };
  }

  return none;
}

/**
 * The longest common prefix of {@link values}, char-for-char. Used by the
 * caller to advance the buffer as far as it unambiguously can when more
 * than one candidate matches (bash-style partial completion).
 */
export function longestCommonPrefix(values: readonly string[]): string {
  if (values.length === 0) return "";
  let prefix = values[0];
  for (let i = 1; i < values.length; i++) {
    const s = values[i];
    let j = 0;
    while (j < prefix.length && j < s.length && prefix[j] === s[j]) j++;
    prefix = prefix.slice(0, j);
    if (prefix === "") break;
  }
  return prefix;
}

/** The objective words a verb accepts, or null if it takes none. */
function objectivesFor(verb: string): readonly string[] | null {
  switch (verb) {
    case "get":
    case "put":
    case "delete":
      return ["vertex", "edge"];
    case "add":
      return ["edge"];
    case "scan":
      return ["vertices", "edges"];
    case "count":
    case "delete-prefix":
      return ["vertices"];
    default:
      return null;
  }
}

/**
 * Whether the given slot holds a vertex key (so it should complete from
 * `knownKeys`). Covers both endpoints of edge verbs and the prefix slot
 * of `scan` (completing a prefix to a full known key is a useful jump).
 */
function isKeySlot(
  verb: string,
  objective: string,
  slotIndex: number,
): boolean {
  switch (verb) {
    case "get":
    case "delete":
    case "put":
      if (objective === "vertex") return slotIndex === 2;
      if (objective === "edge") return slotIndex === 2 || slotIndex === 3;
      return false;
    case "add": // add edge <tail> <head> ...
      return slotIndex === 2 || slotIndex === 3;
    case "scan": // scan { vertices | edges } <prefix> ...
    case "count": // count vertices <prefix>
    case "delete-prefix": // delete-prefix vertices <prefix> ...
      return slotIndex === 2;
    default:
      return false;
  }
}

/** Candidate vertex keys whose name starts with {@link token} (case-insensitive). */
function completeKeys(knownKeys: readonly string[], token: string): string[] {
  const lower = token.toLowerCase();
  const out: string[] = [];
  for (const key of knownKeys) {
    if (key.toLowerCase().startsWith(lower)) {
      out.push(key);
      if (out.length >= MAX_KEY_CANDIDATES) break;
    }
  }
  return out;
}

/**
 * Complete an illuminate option token. With no `=` yet, suggests the
 * option keys not already present on the line (`algorithm=` etc., which
 * keep the trailing `=` so the caller knows not to append a space). Once
 * `=` is typed, suggests the matching enum values as full `key=value`
 * tokens.
 */
function completeIlluminateOption(
  tokens: readonly string[],
  token: string,
): string[] {
  const eq = token.indexOf("=");
  if (eq === -1) {
    const used = new Set(
      tokens.slice(4).map((t) => t.split("=")[0].toLowerCase()),
    );
    const keys = ILLUMINATE_OPTION_KEYS.filter((k) => !used.has(k)).map(
      (k) => `${k}=`,
    );
    return filterByPrefix(keys, token);
  }
  const kw = token.slice(0, eq).toLowerCase();
  const valuePrefix = token.slice(eq + 1).toLowerCase();
  const values = optionValues(kw);
  if (!values) return [];
  return values
    .filter((v) => v.startsWith(valuePrefix))
    .map((v) => `${kw}=${v}`);
}

/** The enum values for an illuminate option keyword, or null if unknown. */
function optionValues(keyword: string): readonly string[] | null {
  switch (keyword) {
    case "algorithm":
      return CLI_ALGORITHMS.map((a) => a.value);
    case "objective":
      return CLI_OBJECTIVES.map((o) => o.value);
    case "weighting":
      return CLI_WEIGHTINGS.map((w) => w.value);
    default:
      return null;
  }
}

function filterByPrefix(
  candidates: readonly string[],
  token: string,
): string[] {
  const lower = token.toLowerCase();
  return candidates.filter((c) => c.toLowerCase().startsWith(lower));
}
