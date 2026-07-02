/**
 * Verb-specific parse helpers split out of parser.ts so the dispatch
 * file stays narrow. Each `parseX` returns `ParseResult`; the
 * usage strings match the Go REPL's per-error usage hints verbatim.
 */

import type {
  AlgorithmName,
  ObjectiveName,
  ParseResult,
  WeightingName,
} from "./types";

const ILL_ALGORITHMS = new Set<AlgorithmName>(["none", "mst", "spt", "ppr", "community"]);
const ILL_OBJECTIVES = new Set<ObjectiveName>(["min", "max"]);
const ILL_WEIGHTINGS = new Set<WeightingName>(["raw", "tfidf", "bm25"]);

// The value-type overrides accepted by `put vertex … type=` (migrated from
// the noun-first `vertex put --value-type`). The grammar validates the type
// NAME here; the value coercion itself happens in the dispatcher (the Go
// REPL coerces at parse — a mismatch surfaces at execution on the web CLI).
const VALUE_TYPES = new Set([
  "auto",
  "string",
  "int",
  "float",
  "bool",
  "datetime",
  "duration",
  "json",
]);

export function parseGet(rest: string[]): ParseResult {
  const [obj, ...args] = rest;
  const o = obj?.toLowerCase();
  if (o === "vertex") {
    if (args.length !== 1 || args[0] === "") {
      return { ok: false, usage: "usage: get vertex <key: string>" };
    }
    return {
      ok: true,
      command: { verb: "get", objective: "vertex", key: args[0] },
    };
  }
  if (o === "edge") {
    if (args.length !== 2 || args[0] === "" || args[1] === "") {
      return {
        ok: false,
        usage: "usage: get edge <tail: string> <head: string>",
      };
    }
    return {
      ok: true,
      command: { verb: "get", objective: "edge", tail: args[0], head: args[1] },
    };
  }
  return { ok: false, usage: "usage: get { vertex | edge } ... " };
}

export function parsePut(rest: string[]): ParseResult {
  const [obj, ...args] = rest;
  const o = obj?.toLowerCase();
  if (o === "vertex") {
    const usage =
      "usage: put vertex <key: string> <value: string|int|float|bool|datetime> [<ttl_seconds: int>] [type=auto|string|int|float|bool|datetime|duration|json]";
    if (args.length < 2) {
      return { ok: false, usage };
    }
    const [key, value, ...tail] = args;
    // Omitted ttl_seconds ⇒ permanent (no decay) (#523); the optional
    // positional ttl and the type= kwarg may appear in either order.
    let ttlSeconds: number | null = null;
    let ttlSet = false;
    let valueType = "auto";
    for (const tok of tail) {
      const eq = tok.indexOf("=");
      if (eq >= 0) {
        if (tok.slice(0, eq).toLowerCase() !== "type") {
          return { ok: false, usage };
        }
        valueType = tok.slice(eq + 1).toLowerCase();
        continue;
      }
      if (ttlSet) {
        return { ok: false, usage };
      }
      const n = parseInt10(tok);
      if (n === null) {
        return { ok: false, usage };
      }
      ttlSeconds = n;
      ttlSet = true;
    }
    if (!VALUE_TYPES.has(valueType)) {
      return { ok: false, usage };
    }
    return {
      ok: true,
      command: {
        verb: "put",
        objective: "vertex",
        key,
        value,
        ttlSeconds,
        valueType,
      },
    };
  }
  if (o === "edge") {
    if (args.length < 3 || args.length > 4) {
      return {
        ok: false,
        usage:
          "usage: put edge <tail: string> <head: string> <weight: float> [<ttl_seconds: int>]",
      };
    }
    const [tail, head, weightTok, ttlTok] = args;
    const weight = parseFloatStrict(weightTok);
    if (weight === null) {
      return {
        ok: false,
        usage:
          "usage: put edge <tail: string> <head: string> <weight: float> [<ttl_seconds: int>]",
      };
    }
    // Omitted ttl_seconds ⇒ permanent (no decay); only an explicit but
    // malformed token is a usage error (#523).
    let ttlSeconds: number | null = null;
    if (ttlTok !== undefined) {
      ttlSeconds = parseInt10(ttlTok);
      if (ttlSeconds === null) {
        return {
          ok: false,
          usage:
            "usage: put edge <tail: string> <head: string> <weight: float> [<ttl_seconds: int>]",
        };
      }
    }
    return {
      ok: true,
      command: {
        verb: "put",
        objective: "edge",
        tail,
        head,
        weight,
        ttlSeconds,
      },
    };
  }
  return { ok: false, usage: "usage: put { vertex | edge } ... " };
}

export function parseDelete(rest: string[]): ParseResult {
  const [obj, ...args] = rest;
  const o = obj?.toLowerCase();
  if (o === "vertex") {
    if (args.length < 1 || args.some((a) => a === "")) {
      return {
        ok: false,
        usage: "usage: delete vertex <key: string> [<key: string> ...]",
      };
    }
    return {
      ok: true,
      command: { verb: "delete", objective: "vertex", keys: args },
    };
  }
  if (o === "edge") {
    if (
      args.length < 2 ||
      args.length % 2 !== 0 ||
      args.some((a) => a === "")
    ) {
      return {
        ok: false,
        usage:
          "usage: delete edge <tail: string> <head: string> [<tail: string> <head: string> ...]",
      };
    }
    const pairs: { tail: string; head: string }[] = [];
    for (let i = 0; i < args.length; i += 2) {
      pairs.push({ tail: args[i], head: args[i + 1] });
    }
    return {
      ok: true,
      command: { verb: "delete", objective: "edge", pairs },
    };
  }
  return { ok: false, usage: "usage: delete { vertex | edge }" };
}

export function parseAdd(rest: string[]): ParseResult {
  const [obj, ...args] = rest;
  if (obj?.toLowerCase() !== "edge") {
    return { ok: false, usage: "usage: add edge ... " };
  }
  if (args.length < 3 || args.length > 4) {
    return {
      ok: false,
      usage:
        "usage: add edge <tail: string> <head: string> <weight: float> [<ttl_seconds: int>]",
    };
  }
  const [tail, head, weightTok, ttlTok] = args;
  const weight = parseFloatStrict(weightTok);
  if (weight === null) {
    return {
      ok: false,
      usage:
        "usage: add edge <tail: string> <head: string> <weight: float> [<ttl_seconds: int>]",
    };
  }
  // Omitted ttl_seconds ⇒ permanent (no decay); only an explicit but
  // malformed token is a usage error (#523).
  let ttlSeconds: number | null = null;
  if (ttlTok !== undefined) {
    ttlSeconds = parseInt10(ttlTok);
    if (ttlSeconds === null) {
      return {
        ok: false,
        usage:
          "usage: add edge <tail: string> <head: string> <weight: float> [<ttl_seconds: int>]",
      };
    }
  }
  return {
    ok: true,
    command: {
      verb: "add",
      objective: "edge",
      tail,
      head,
      weight,
      ttlSeconds,
    },
  };
}

export function parseScan(rest: string[]): ParseResult {
  const [obj, ...args] = rest;
  const o = obj?.toLowerCase();
  if (o === "vertices") {
    const usage =
      "usage: scan vertices <prefix: string> [<limit: int>] [all=true]";
    if (args.length < 1 || args[0] === "") {
      return { ok: false, usage };
    }
    let limit = 0;
    let limitSet = false;
    let all = false;
    for (const tok of args.slice(1)) {
      const eq = tok.indexOf("=");
      if (eq < 0) {
        if (limitSet) return { ok: false, usage };
        const n = parseInt10(tok);
        if (n === null) return { ok: false, usage };
        limit = n;
        limitSet = true;
        continue;
      }
      const key = tok.slice(0, eq).toLowerCase();
      const value = tok.slice(eq + 1);
      if (key === "all") {
        const b = parseBoolStrict(value);
        if (b === null) return { ok: false, usage };
        all = b;
      } else {
        return { ok: false, usage };
      }
    }
    return {
      ok: true,
      command: {
        verb: "scan",
        objective: "vertices",
        prefix: args[0],
        limit,
        all,
      },
    };
  }
  if (o === "edges") {
    const usage =
      "usage: scan edges <tail-prefix: string> [<limit: int>] [head=<prefix>] [all=true]";
    if (args.length < 1 || args[0] === "") {
      return { ok: false, usage };
    }
    let limit = 0;
    let limitSet = false;
    let headPrefix = "";
    let all = false;
    for (const tok of args.slice(1)) {
      const eq = tok.indexOf("=");
      if (eq < 0) {
        if (limitSet) return { ok: false, usage };
        const n = parseInt10(tok);
        if (n === null) return { ok: false, usage };
        limit = n;
        limitSet = true;
        continue;
      }
      const key = tok.slice(0, eq).toLowerCase();
      const value = tok.slice(eq + 1);
      if (key === "head") {
        if (value === "") return { ok: false, usage };
        headPrefix = value;
      } else if (key === "all") {
        const b = parseBoolStrict(value);
        if (b === null) return { ok: false, usage };
        all = b;
      } else {
        return { ok: false, usage };
      }
    }
    return {
      ok: true,
      command: {
        verb: "scan",
        objective: "edges",
        tailPrefix: args[0],
        headPrefix,
        limit,
        all,
      },
    };
  }
  return { ok: false, usage: "usage: scan { vertices | edges } ... " };
}

export function parseKeys(rest: string[]): ParseResult {
  const usage = "usage: keys <prefix: string> [<limit: int>]";
  if (rest.length < 1 || rest.length > 2 || rest[0] === "") {
    return { ok: false, usage };
  }
  const limit = rest[1] === undefined ? 0 : parseInt10(rest[1]);
  if (limit === null) {
    return { ok: false, usage };
  }
  return { ok: true, command: { verb: "keys", prefix: rest[0], limit } };
}

export function parseCount(rest: string[]): ParseResult {
  const usage = "usage: count vertices <prefix: string>";
  const [obj, ...args] = rest;
  if (obj?.toLowerCase() !== "vertices") {
    return { ok: false, usage };
  }
  if (args.length !== 1 || args[0] === "") {
    return { ok: false, usage };
  }
  return {
    ok: true,
    command: { verb: "count", objective: "vertices", prefix: args[0] },
  };
}

export function parseDeletePrefix(rest: string[]): ParseResult {
  const usage =
    "usage: delete-prefix vertices <prefix: string> [limit=<int>] [confirm=yes|dry_run=true]";
  const [obj, ...args] = rest;
  if (obj?.toLowerCase() !== "vertices") {
    return { ok: false, usage };
  }
  if (args.length < 1 || args[0] === "") {
    return { ok: false, usage };
  }
  const prefix = args[0];
  let limit = 0;
  let dryRun = false;
  let confirm = false;
  for (const tok of args.slice(1)) {
    const eq = tok.indexOf("=");
    if (eq < 0) {
      return { ok: false, usage };
    }
    const key = tok.slice(0, eq).toLowerCase();
    const value = tok.slice(eq + 1);
    if (key === "limit") {
      const n = parseInt10(value);
      if (n === null) return { ok: false, usage };
      limit = n;
    } else if (key === "confirm") {
      if (value.toLowerCase() !== "yes") return { ok: false, usage };
      confirm = true;
    } else if (key === "dry_run") {
      const b = parseBoolStrict(value);
      if (b === null) return { ok: false, usage };
      dryRun = b;
    } else {
      return { ok: false, usage };
    }
  }
  // Safety gate: EXACTLY one of confirm=yes / dry_run=true is required.
  if (confirm === dryRun) {
    return { ok: false, usage };
  }
  return {
    ok: true,
    command: {
      verb: "delete-prefix",
      objective: "vertices",
      prefix,
      limit,
      dryRun,
      confirm,
    },
  };
}

export function parseIlluminate(rest: string[]): ParseResult {
  const usage =
    "usage: illuminate <key: string> <step: int> <k: int> [algorithm=none|mst|spt|ppr|community] [objective=min|max] [weighting=raw|tfidf|bm25] [prefix=<string>] [restart_prob=<float>] [epsilon=<float>]";
  if (rest.length < 3) {
    return { ok: false, usage };
  }
  const seed = rest[0];
  if (seed === "") {
    return { ok: false, usage };
  }
  const step = parseInt10(rest[1]);
  if (step === null) {
    return { ok: false, usage };
  }
  const k = parseInt10(rest[2]);
  if (k === null) {
    return { ok: false, usage };
  }
  let algorithm: AlgorithmName = "none";
  // #560: defaults to `max` so a long-form command that omits the objective
  // kwarg matches the server's MAXIMIZE default and the click picker's
  // default — keeping the strongest-neighbour behaviour byte-for-byte.
  let objective: ObjectiveName = "max";
  let weighting: WeightingName = "raw";
  // #606: prefix is a FREE-TEXT kwarg (not a closed-set axis). Empty means
  // "no filter"; an explicit empty `prefix=` is rejected, mirroring the Go
  // REPL. The value is matched against vertex keys verbatim (case-SENSITIVE).
  let vertexPrefix = "";
  // #801: Personalized PageRank knobs. Free-text floats parsed leniently
  // (1e-3 / .25 / 0 accepted; NaN/inf/garbage rejected). Both default to 0,
  // which the server resolves to its own α=0.15 / ε=1e-4; ignored unless
  // algorithm=ppr. A malformed value is a hard parse error, mirroring the Go
  // REPL's strconv.ParseFloat rejection rather than silently dropping to 0.
  let restartProb = 0;
  let epsilon = 0;
  for (let i = 3; i < rest.length; i++) {
    const tok = rest[i];
    const eq = tok.indexOf("=");
    if (eq < 0) {
      return { ok: false, usage };
    }
    // The keyword KEY is always case-insensitive. The three closed-set
    // axes (algorithm / objective / weighting) also fold their VALUE
    // because they only ever take a small fixed enum the Go REPL matches
    // case-insensitively (#437). The free-text `prefix` VALUE is matched
    // against vertex keys verbatim, so it stays case-SENSITIVE (#604).
    const key = tok.slice(0, eq).toLowerCase();
    const value = tok.slice(eq + 1);
    const lvalue = value.toLowerCase();
    if (key === "algorithm") {
      if (!ILL_ALGORITHMS.has(lvalue as AlgorithmName)) {
        return { ok: false, usage };
      }
      algorithm = lvalue as AlgorithmName;
    } else if (key === "objective") {
      if (!ILL_OBJECTIVES.has(lvalue as ObjectiveName)) {
        return { ok: false, usage };
      }
      objective = lvalue as ObjectiveName;
    } else if (key === "weighting") {
      if (!ILL_WEIGHTINGS.has(lvalue as WeightingName)) {
        return { ok: false, usage };
      }
      weighting = lvalue as WeightingName;
    } else if (key === "prefix") {
      if (value === "") {
        return { ok: false, usage };
      }
      vertexPrefix = value;
    } else if (key === "restart_prob") {
      const f = parseFloatStrict(value);
      if (f === null) {
        return { ok: false, usage };
      }
      restartProb = f;
    } else if (key === "epsilon") {
      const f = parseFloatStrict(value);
      if (f === null) {
        return { ok: false, usage };
      }
      epsilon = f;
    } else {
      return { ok: false, usage };
    }
  }
  return {
    ok: true,
    command: {
      verb: "illuminate",
      seed,
      step,
      k,
      algorithm,
      objective,
      weighting,
      vertexPrefix,
      restartProb,
      epsilon,
    },
  };
}

/**
 * Human-readable per-verb grammar reference printed by the `help`
 * verb (#436). Single source of truth for the TypeScript side; the
 * Go REPL keeps a byte-equivalent copy in `cli/parser/parser.go`
 * `HelpText`, and the shared fixture `verbs.json` exercises both
 * parsers against the bare `help` verb.
 *
 * The kwarg enums and defaults below MUST stay in lockstep with
 * `parseIlluminate`'s `ILL_ALGORITHMS` / `ILL_OBJECTIVES` /
 * `ILL_WEIGHTINGS` sets and the corresponding Go REPL parser.
 */
export const HELP_TEXT = [
  "Lantern CLI grammar:",
  "",
  "  get    vertex <key: string>",
  "  get    edge   <tail: string> <head: string>",
  "  put    vertex <key: string> <value: string|int|float|bool|datetime> [<ttl_seconds: int>] [type=auto|string|int|float|bool|datetime|duration|json]",
  "  put    edge   <tail: string> <head: string> <weight: float> [<ttl_seconds: int>]",
  "  add    edge   <tail: string> <head: string> <weight: float> [<ttl_seconds: int>]",
  "  delete vertex <key: string> [<key: string> ...]",
  "  delete edge   <tail: string> <head: string> [<tail: string> <head: string> ...]",
  "  scan   vertices <prefix: string> [<limit: int>] [all=true]",
  "  scan   edges    <tail-prefix: string> [<limit: int>] [head=<prefix>] [all=true]",
  "  count  vertices <prefix: string>",
  "  delete-prefix vertices <prefix: string> [limit=<int>] [confirm=yes|dry_run=true]",
  "  keys   <prefix: string> [<limit: int>]",
  "  illuminate <seed: string> <step: int> <k: int>",
  "             [algorithm={none|mst|spt|ppr|community}] default=none",
  "             [objective={min|max}]       default=max",
  "             [weighting={raw|tfidf|bm25}] default=raw",
  "             [prefix=<string>]           default=all keys",
  "             [restart_prob=<float>]      default=0 (ppr only; server α=0.15)",
  "             [epsilon=<float>]           default=0 (ppr only; server ε=1e-4)",
  "  help",
  "  exit",
  "",
  'Quoting: "double" with C-style escapes (\\" \\\\ \\n \\r \\t); \'single\' verbatim.',
  "Verb/objective case-insensitive; argument values preserve case.",
].join("\n");

/**
 * A single row of the structured CLI command reference rendered by the
 * `/cli` "Commands" panel (`CliCommandReference`).
 *
 * This is a **separate, human-facing view** of the grammar — deliberately
 * NOT derived from {@link HELP_TEXT}. `HELP_TEXT` is byte-locked to the Go
 * `HelpText` (the `verbs.json` fixture compares both parsers against it) and
 * its multi-line `illuminate` alignment must not be regenerated. The two are
 * instead kept honest by `verbs.test.ts`, which asserts that every `example`
 * parses and that the set of `verb`s equals the canonical verb list.
 */
export interface CliCommandDoc {
  /** The group shown as a section header in the panel. */
  readonly group: string;
  /** The verb keyword as typed (`get`, `put`, `illuminate`, …). */
  readonly verb: string;
  /** Compact signature, e.g. `get vertex <key>`. */
  readonly signature: string;
  /** One-line, plain-English description. */
  readonly summary: string;
  /** A concrete, runnable example (must parse — bound by `verbs.test.ts`). */
  readonly example: string;
}

/**
 * Structured command reference for the `/cli` "Commands" panel, ordered by
 * group (Vertices → Edges → Browse → Explore → Session). See
 * {@link CliCommandDoc} for why this is not generated from `HELP_TEXT`.
 */
export const CLI_COMMAND_REFERENCE: readonly CliCommandDoc[] = [
  {
    group: "Vertices",
    verb: "get",
    signature: "get vertex <key>",
    summary: "Read a vertex's value by key.",
    example: "get vertex alice",
  },
  {
    group: "Vertices",
    verb: "put",
    signature: "put vertex <key> <value> [ttl] [type=...]",
    summary:
      "Create or replace a vertex; value is auto-typed, or forced with type= (string/int/float/bool/datetime/duration/json). TTL seconds optional.",
    example: "put vertex alice Alice 3600",
  },
  {
    group: "Vertices",
    verb: "delete",
    signature: "delete vertex <key> [<key> ...]",
    summary: "Remove one or more vertices by key (batched when more than one).",
    example: "delete vertex alice",
  },
  {
    group: "Vertices",
    verb: "delete-prefix",
    signature: "delete-prefix vertices <prefix> [confirm=yes|dry_run=true]",
    summary:
      "Bulk-delete vertices under a key prefix (destructive). Requires confirm=yes or dry_run=true.",
    example: "delete-prefix vertices tmp/ dry_run=true",
  },
  {
    group: "Edges",
    verb: "get",
    signature: "get edge <tail> <head>",
    summary: "Read the weight of the edge between two vertices.",
    example: "get edge alice bob",
  },
  {
    group: "Edges",
    verb: "put",
    signature: "put edge <tail> <head> <weight> [ttl]",
    summary:
      "Create or replace an edge, setting its weight. TTL seconds optional.",
    example: "put edge alice bob 1.5",
  },
  {
    group: "Edges",
    verb: "add",
    signature: "add edge <tail> <head> <weight> [ttl]",
    summary:
      "Add weight onto an edge (additive); creates it if absent. TTL seconds optional.",
    example: "add edge alice bob 0.5",
  },
  {
    group: "Edges",
    verb: "delete",
    signature: "delete edge <tail> <head> [<tail> <head> ...]",
    summary: "Remove one or more edges by (tail, head) pair.",
    example: "delete edge alice bob",
  },
  {
    group: "Browse",
    verb: "scan",
    signature: "scan vertices <prefix> [limit] [all=true]",
    summary:
      "List vertices whose key starts with a prefix. all=true returns every page.",
    example: "scan vertices user: 20",
  },
  {
    group: "Browse",
    verb: "scan",
    signature: "scan edges <tail-prefix> [limit] [head=<prefix>] [all=true]",
    summary:
      "List edges by tail prefix; head=<prefix> filters the head. all=true returns every page.",
    example: "scan edges alice 20",
  },
  {
    group: "Browse",
    verb: "keys",
    signature: "keys <prefix> [limit]",
    summary:
      "List vertex keys under a prefix (keys-only, Redis-style KEYS; optional max count).",
    example: "keys user: 20",
  },
  {
    group: "Browse",
    verb: "count",
    signature: "count vertices <prefix>",
    summary: "Count vertices whose key starts with a prefix.",
    example: "count vertices user:",
  },
  {
    group: "Explore",
    verb: "illuminate",
    signature:
      "illuminate <seed> <step> <k> [algorithm=none|mst|spt|ppr|community] [objective=min|max] [weighting=raw|tfidf|bm25] [prefix=<string>] [restart_prob=<float>] [epsilon=<float>]",
    summary:
      "Walk the graph from a seed (step hops, top-k per hop) and render the subgraph. algorithm=ppr runs Personalized PageRank (tune locality with restart_prob/epsilon); the other axes reduce/weight the discovered subgraph.",
    example: "illuminate alice 2 5 algorithm=ppr restart_prob=0.25",
  },
  {
    group: "Session",
    verb: "help",
    signature: "help",
    summary: "Print the grammar reference into the terminal.",
    example: "help",
  },
  {
    group: "Session",
    verb: "exit",
    signature: "exit",
    summary:
      "No-op in the web CLI (close the tab to leave); ends the prompt in `lantern-cli repl`.",
    example: "exit",
  },
];

/**
 * Parses the `help` verb. Extra arguments are accepted silently so
 * the verb behaves like `exit` — discoverability beats strictness
 * here, since the operator typing `help` is by definition asking for
 * the grammar reference, not for a usage hint about `help` itself.
 */
export function parseHelp(_rest: string[]): ParseResult {
  return { ok: true, command: { verb: "help" } };
}

// Exact set Go's `strconv.ParseBool` accepts (mirrors the dispatcher's
// coerceValue tokens) — used by the `all=` / `dry_run=` kwargs.
const BOOL_TRUE = new Set(["1", "t", "T", "TRUE", "true", "True"]);
const BOOL_FALSE = new Set(["0", "f", "F", "FALSE", "false", "False"]);

function parseBoolStrict(s: string): boolean | null {
  if (BOOL_TRUE.has(s)) return true;
  if (BOOL_FALSE.has(s)) return false;
  return null;
}

function parseInt10(s: string): number | null {
  if (s === "" || !/^-?\d+$/.test(s)) {
    return null;
  }
  const n = Number.parseInt(s, 10);
  return Number.isFinite(n) ? n : null;
}

/**
 * Parses a Go-style float literal — exposed for unit tests (#434).
 *
 * Mirrors `strconv.ParseFloat` as it's invoked from
 * `cli/parser/parser.go` (used for edge weight + the put-vertex
 * coercion cascade), with one deliberate divergence:
 *
 * - Go's ParseFloat accepts the IEEE special tokens
 *   `NaN`, `Inf`, `+Inf`, `-Inf`, `Infinity`, `-Infinity` (any case).
 *   We reject them up-front: an edge weight of NaN is a bug magnet and
 *   nothing in lantern actually wants one.
 *
 * Otherwise we accept anything `Number.parseFloat` can finitely parse:
 *   1e3, 1.5e-3, .5, -.5, 5., 0, -1.25, +2.5
 *
 * Returns null on parse failure (so the call site can surface its own
 * "usage:" line).
 */
export function parseFloatStrict(s: string): number | null {
  if (s === "") return null;
  // Reject IEEE special tokens that ParseFloat would otherwise accept.
  if (/^[+-]?(nan|inf|infinity)$/i.test(s)) return null;
  // Structural guard so a stray "abc1" does not slip through
  // Number.parseFloat's prefix-only parse.
  if (!/^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$/.test(s)) return null;
  const n = Number.parseFloat(s);
  return Number.isFinite(n) ? n : null;
}
