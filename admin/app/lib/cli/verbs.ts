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

const ILL_ALGORITHMS = new Set<AlgorithmName>(["none", "mst", "spt"]);
const ILL_OBJECTIVES = new Set<ObjectiveName>(["min", "max"]);
const ILL_WEIGHTINGS = new Set<WeightingName>(["raw", "tfidf"]);

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
    if (args.length < 2 || args.length > 3) {
      return {
        ok: false,
        usage:
          "usage: put vertex <key: string> <value: string|int|float|bool|datetime> [<ttl_seconds: int>]",
      };
    }
    const [key, value, ttlTok] = args;
    // Omitted ttl_seconds ⇒ permanent (no decay); only an explicit but
    // malformed token is a usage error (#523).
    let ttlSeconds: number | null = null;
    if (ttlTok !== undefined) {
      ttlSeconds = parseInt10(ttlTok);
      if (ttlSeconds === null) {
        return {
          ok: false,
          usage:
            "usage: put vertex <key: string> <value: string|int|float|bool|datetime> [<ttl_seconds: int>]",
        };
      }
    }
    return {
      ok: true,
      command: {
        verb: "put",
        objective: "vertex",
        key,
        value,
        ttlSeconds,
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
    if (args.length !== 1 || args[0] === "") {
      return { ok: false, usage: "usage: delete vertex <key: string>" };
    }
    return {
      ok: true,
      command: { verb: "delete", objective: "vertex", key: args[0] },
    };
  }
  if (o === "edge") {
    if (args.length !== 2 || args[0] === "" || args[1] === "") {
      return {
        ok: false,
        usage: "usage: delete edge <tail: string> <head: string>",
      };
    }
    return {
      ok: true,
      command: {
        verb: "delete",
        objective: "edge",
        tail: args[0],
        head: args[1],
      },
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
    if (args.length < 1 || args.length > 2 || args[0] === "") {
      return {
        ok: false,
        usage: "usage: scan vertices <prefix: string> [<limit: int>]",
      };
    }
    const limit = args[1] === undefined ? 0 : parseInt10(args[1]);
    if (limit === null) {
      return {
        ok: false,
        usage: "usage: scan vertices <prefix: string> [<limit: int>]",
      };
    }
    return {
      ok: true,
      command: {
        verb: "scan",
        objective: "vertices",
        prefix: args[0],
        limit,
      },
    };
  }
  if (o === "edges") {
    if (args.length < 1 || args.length > 2 || args[0] === "") {
      return {
        ok: false,
        usage: "usage: scan edges <tail-prefix: string> [<limit: int>]",
      };
    }
    const limit = args[1] === undefined ? 0 : parseInt10(args[1]);
    if (limit === null) {
      return {
        ok: false,
        usage: "usage: scan edges <tail-prefix: string> [<limit: int>]",
      };
    }
    return {
      ok: true,
      command: {
        verb: "scan",
        objective: "edges",
        tailPrefix: args[0],
        limit,
      },
    };
  }
  return { ok: false, usage: "usage: scan { vertices | edges } ... " };
}

export function parseIlluminate(rest: string[]): ParseResult {
  const usage =
    "usage: illuminate <key: string> <step: int> <k: int> [algorithm=none|mst|spt] [objective=min|max] [weighting=raw|tfidf] [prefix=<string>]";
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
  "  put    vertex <key: string> <value: string|int|float|bool|datetime> [<ttl_seconds: int>]",
  "  put    edge   <tail: string> <head: string> <weight: float> [<ttl_seconds: int>]",
  "  add    edge   <tail: string> <head: string> <weight: float> [<ttl_seconds: int>]",
  "  delete vertex <key: string>",
  "  delete edge   <tail: string> <head: string>",
  "  scan   vertices <prefix: string> [<limit: int>]",
  "  scan   edges    <tail-prefix: string> [<limit: int>]",
  "  illuminate <seed: string> <step: int> <k: int>",
  "             [algorithm={none|mst|spt}]  default=none",
  "             [objective={min|max}]       default=max",
  "             [weighting={raw|tfidf}]     default=raw",
  "             [prefix=<string>]           default=all keys",
  "  help",
  "  exit",
  "",
  'Quoting: "double" with C-style escapes (\\" \\\\ \\n \\r \\t); \'single\' verbatim.',
  "Verb/objective case-insensitive; argument values preserve case.",
].join("\n");

/**
 * Parses the `help` verb. Extra arguments are accepted silently so
 * the verb behaves like `exit` — discoverability beats strictness
 * here, since the operator typing `help` is by definition asking for
 * the grammar reference, not for a usage hint about `help` itself.
 */
export function parseHelp(_rest: string[]): ParseResult {
  return { ok: true, command: { verb: "help" } };
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
