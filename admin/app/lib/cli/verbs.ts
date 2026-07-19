/**
 * Verb-specific parse helpers split out of parser.ts so the dispatch
 * file stays narrow. Each `parseX` returns `ParseResult`; the
 * usage strings match the Go REPL's per-error usage hints verbatim.
 */

import type {
  Command,
  HelpTopic,
  ObjectiveName,
  ParseResult,
  ReductionName,
  SearchMatchMode,
  SearchOutputFormat,
  SearchProjection,
  WeightingName,
} from "./types";

const ILL_REDUCTIONS = new Set<ReductionName>(["none", "mst", "spt"]);
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

const ADD_EDGE_USAGE =
  "usage: add edge <tail: string> <head: string> <weight: float> [<ttl_seconds: int>]";
const ADD_DECAYING_EDGE_USAGE =
  "usage: add decaying-edge <tail: string> <head: string> <initial_weight: float> <ratio: float> <steps: int> <interval_seconds: int>";

export function parseAdd(rest: string[]): ParseResult {
  const [obj, ...args] = rest;
  const o = obj?.toLowerCase();
  if (o === "edge") {
    return parseAddEdge(args);
  }
  if (o === "decaying-edge") {
    return parseAddDecayingEdge(args);
  }
  return { ok: false, usage: "usage: add { edge | decaying-edge } ... " };
}

function parseAddEdge(args: string[]): ParseResult {
  if (args.length < 3 || args.length > 4) {
    return { ok: false, usage: ADD_EDGE_USAGE };
  }
  const [tail, head, weightTok, ttlTok] = args;
  const weight = parseFloatStrict(weightTok);
  if (weight === null) {
    return { ok: false, usage: ADD_EDGE_USAGE };
  }
  // Omitted ttl_seconds ⇒ permanent (no decay); only an explicit but
  // malformed token is a usage error (#523).
  let ttlSeconds: number | null = null;
  if (ttlTok !== undefined) {
    ttlSeconds = parseInt10(ttlTok);
    if (ttlSeconds === null) {
      return { ok: false, usage: ADD_EDGE_USAGE };
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

// `add decaying-edge <tail> <head> <initial_weight> <ratio> <steps>
// <interval_seconds>` (#953). Mirrors the Go parser's `AddDecayingEdgeParam`:
// exactly six operands, validated for *type* only. Numeric-range checks
// (ratio in (0,1), steps in [1, MAX_DECAY_STEPS], interval > 0) are deferred
// to the SDK's `DecayOptions` contract, so a grammatically well-formed but
// out-of-range command parses here and fails at execution with the SDK's
// error, exactly as the Go CLI defers to `client.DecayOpts`.
function parseAddDecayingEdge(args: string[]): ParseResult {
  if (args.length !== 6) {
    return { ok: false, usage: ADD_DECAYING_EDGE_USAGE };
  }
  const [tail, head, initialTok, ratioTok, stepsTok, intervalTok] = args;
  const initialWeight = parseFloatStrict(initialTok);
  const ratio = parseFloatStrict(ratioTok);
  const steps = parseInt10(stepsTok);
  const intervalSeconds = parseInt10(intervalTok);
  if (
    initialWeight === null ||
    ratio === null ||
    steps === null ||
    intervalSeconds === null
  ) {
    return { ok: false, usage: ADD_DECAYING_EDGE_USAGE };
  }
  return {
    ok: true,
    command: {
      verb: "add",
      objective: "decaying-edge",
      tail,
      head,
      initialWeight,
      ratio,
      steps,
      intervalSeconds,
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

/** Parse the cross-language `search <query> key=value...` grammar (#1068). */
export function parseSearch(rest: string[]): ParseResult {
  if (rest.length === 0) {
    return { ok: false, usage: searchUsage() };
  }
  const command: Extract<Command, { verb: "search" }> = {
    verb: "search",
    query: rest[0],
    limit: 0,
    prefix: "",
    mode: "server",
    minShould: 0,
    phrase: false,
    fuzziness: 0,
    prefixTerms: false,
    cursor: "",
    all: false,
    projection: "key-score",
    format: "",
  };
  const seen = new Set<string>();
  for (const token of rest.slice(1)) {
    const eq = token.indexOf("=");
    if (eq <= 0) {
      return { ok: false, usage: searchUsage() };
    }
    const key = token.slice(0, eq).toLowerCase();
    const value = token.slice(eq + 1);
    if (seen.has(key)) {
      return { ok: false, usage: searchUsage() };
    }
    seen.add(key);
    switch (key) {
      case "limit": {
        const n = parseFamilyUint32(value);
        if (n === null) return { ok: false, usage: searchUsage() };
        command.limit = n;
        break;
      }
      case "prefix":
        command.prefix = value;
        break;
      case "mode": {
        const mode = value.toLowerCase();
        if (
          !(["server", "any", "all", "min-should"] as const).includes(
            mode as SearchMatchMode,
          )
        ) {
          return { ok: false, usage: searchUsage() };
        }
        command.mode = mode as SearchMatchMode;
        break;
      }
      case "min_should": {
        const n = parseFamilyUint32(value);
        if (n === null) return { ok: false, usage: searchUsage() };
        command.minShould = n;
        break;
      }
      case "phrase": {
        const bool = parseBoolStrict(value);
        if (bool === null) return { ok: false, usage: searchUsage() };
        command.phrase = bool;
        break;
      }
      case "fuzziness": {
        const n = parseFamilyUint32(value);
        if (n === null || n > 2) return { ok: false, usage: searchUsage() };
        command.fuzziness = n as 0 | 1 | 2;
        break;
      }
      case "prefix_terms": {
        const bool = parseBoolStrict(value);
        if (bool === null) return { ok: false, usage: searchUsage() };
        command.prefixTerms = bool;
        break;
      }
      case "cursor":
        command.cursor = value;
        break;
      case "all": {
        const bool = parseBoolStrict(value);
        if (bool === null) return { ok: false, usage: searchUsage() };
        command.all = bool;
        break;
      }
      case "projection": {
        const projection = value.toLowerCase();
        if (
          !(["key-score", "full-vertex"] as const).includes(
            projection as SearchProjection,
          )
        ) {
          return { ok: false, usage: searchUsage() };
        }
        command.projection = projection as SearchProjection;
        break;
      }
      case "format": {
        const format = value.toLowerCase();
        if (
          !(["json", "ndjson", "tsv"] as const).includes(
            format as Exclude<SearchOutputFormat, "">,
          )
        ) {
          return { ok: false, usage: searchUsage() };
        }
        command.format = format as Exclude<SearchOutputFormat, "">;
        break;
      }
      default:
        return { ok: false, usage: searchUsage() };
    }
  }
  if (command.minShould !== 0 && command.mode !== "min-should") {
    return { ok: false, usage: searchUsage() };
  }
  if (
    command.phrase &&
    (command.mode !== "server" ||
      command.minShould !== 0 ||
      command.fuzziness !== 0 ||
      command.prefixTerms)
  ) {
    return { ok: false, usage: searchUsage() };
  }
  if (command.all && command.format === "json") {
    return { ok: false, usage: searchUsage() };
  }
  return { ok: true, command };
}

function searchUsage(): string {
  return "usage: search <query: string> [limit=<uint32>] [prefix=<string>] [mode=server|any|all|min-should] [min_should=<uint32>] [phrase=<bool>] [fuzziness=0|1|2] [prefix_terms=<bool>] [cursor=<base64url>] [all=<bool>] [projection=key-score|full-vertex] [format=json|ndjson|tsv]";
}

export function parseBfs(rest: string[]): ParseResult {
  const usage =
    "usage: bfs <seed: string> [step: int] [fan_out: int] [reduction=none|mst|spt] [objective=min|max] [weighting=raw|tfidf|bm25] [prefix=<string>]";
  if (rest.length < 1 || rest[0] === "") {
    return { ok: false, usage };
  }
  const seed = rest[0];
  // Only <seed> is required (#975); step/fan_out are optional positional ints
  // (defaults 5/3) or step=/fan_out= kwargs. A bare integer fills the next
  // positional slot (step then fan_out). objective steers BOTH the per-hop
  // top-k pruning and the reduction direction (#560).
  let step = 5;
  let fanOut = 3;
  let reduction: ReductionName = "none";
  let objective: ObjectiveName = "max";
  let weighting: WeightingName = "raw";
  let vertexPrefix = "";
  let pos = 0;
  for (let i = 1; i < rest.length; i++) {
    const tok = rest[i];
    const eq = tok.indexOf("=");
    if (eq >= 0) {
      const key = tok.slice(0, eq).toLowerCase();
      const value = tok.slice(eq + 1);
      const lvalue = value.toLowerCase();
      if (key === "step") {
        const n = parsePositiveFamilyInt(value);
        if (n === null) return { ok: false, usage };
        step = n;
      } else if (key === "fan_out") {
        const n = parsePositiveFamilyInt(value);
        if (n === null) return { ok: false, usage };
        fanOut = n;
      } else if (key === "reduction") {
        if (!ILL_REDUCTIONS.has(lvalue as ReductionName))
          return { ok: false, usage };
        reduction = lvalue as ReductionName;
      } else if (key === "objective") {
        if (!ILL_OBJECTIVES.has(lvalue as ObjectiveName))
          return { ok: false, usage };
        objective = lvalue as ObjectiveName;
      } else if (key === "weighting") {
        if (!ILL_WEIGHTINGS.has(lvalue as WeightingName))
          return { ok: false, usage };
        weighting = lvalue as WeightingName;
      } else if (key === "prefix") {
        if (value === "") return { ok: false, usage };
        vertexPrefix = value;
      } else {
        return { ok: false, usage };
      }
      continue;
    }
    if (pos === 0) {
      const n = parsePositiveFamilyInt(tok);
      if (n === null) return { ok: false, usage };
      step = n;
    } else if (pos === 1) {
      const n = parsePositiveFamilyInt(tok);
      if (n === null) return { ok: false, usage };
      fanOut = n;
    } else {
      return { ok: false, usage };
    }
    pos++;
  }
  return {
    ok: true,
    command: {
      verb: "bfs",
      seed,
      step,
      fanOut,
      reduction,
      objective,
      weighting,
      vertexPrefix,
    },
  };
}

export function parsePagerank(rest: string[]): ParseResult {
  const usage =
    "usage: pagerank <seed: string> [top_n: int] [restart_prob=<float>] [epsilon=<float>] [weighting=raw|tfidf|bm25] [prefix=<string>]";
  if (rest.length < 1 || rest[0] === "") {
    return { ok: false, usage };
  }
  const seed = rest[0];
  // Only <seed> is required (#975); top_n is an optional positional int
  // (default 10; 0 = every positive-mass vertex) or top_n= kwarg. Personalized
  // PageRank returns a relevance star (already a tree), so there is no
  // reduction/objective knob. restart_prob (α) / epsilon (ε) default to 0,
  // which the server resolves to α=0.15 / ε=1e-4.
  let topN = 10;
  let restartProb = 0;
  let epsilon = 0;
  let weighting: WeightingName = "raw";
  let vertexPrefix = "";
  let pos = 0;
  for (let i = 1; i < rest.length; i++) {
    const tok = rest[i];
    const eq = tok.indexOf("=");
    if (eq >= 0) {
      const key = tok.slice(0, eq).toLowerCase();
      const value = tok.slice(eq + 1);
      const lvalue = value.toLowerCase();
      if (key === "top_n") {
        const n = parseNonNegativeFamilyInt(value);
        if (n === null) return { ok: false, usage };
        topN = n;
      } else if (key === "restart_prob") {
        const f = parseFamilyRestartProb(value);
        if (f === null) return { ok: false, usage };
        restartProb = f;
      } else if (key === "epsilon") {
        const f = parsePositiveFamilyFloat(value);
        if (f === null) return { ok: false, usage };
        epsilon = f;
      } else if (key === "weighting") {
        if (!ILL_WEIGHTINGS.has(lvalue as WeightingName))
          return { ok: false, usage };
        weighting = lvalue as WeightingName;
      } else if (key === "prefix") {
        if (value === "") return { ok: false, usage };
        vertexPrefix = value;
      } else {
        return { ok: false, usage };
      }
      continue;
    }
    if (pos === 0) {
      const n = parseNonNegativeFamilyInt(tok);
      if (n === null) return { ok: false, usage };
      topN = n;
    } else {
      return { ok: false, usage };
    }
    pos++;
  }
  return {
    ok: true,
    command: {
      verb: "pagerank",
      seed,
      topN,
      restartProb,
      epsilon,
      weighting,
      vertexPrefix,
    },
  };
}

export function parseCommunity(rest: string[]): ParseResult {
  const usage =
    "usage: community <seed: string> [max_size: int] [restart_prob=<float>] [epsilon=<float>] [reduction=none|mst|spt] [objective=min|max] [weighting=raw|tfidf|bm25] [prefix=<string>]";
  if (rest.length < 1 || rest[0] === "") {
    return { ok: false, usage };
  }
  const seed = rest[0];
  // Only <seed> is required (#975); max_size is an optional positional int
  // (default 0 = the conductance sweep decides) or max_size= kwarg.
  // restart_prob/epsilon share PPR's defaults; reduction/objective render an
  // optional tree view of the community (#845).
  let maxSize = 0;
  let restartProb = 0;
  let epsilon = 0;
  let reduction: ReductionName = "none";
  let objective: ObjectiveName = "max";
  let weighting: WeightingName = "raw";
  let vertexPrefix = "";
  let pos = 0;
  for (let i = 1; i < rest.length; i++) {
    const tok = rest[i];
    const eq = tok.indexOf("=");
    if (eq >= 0) {
      const key = tok.slice(0, eq).toLowerCase();
      const value = tok.slice(eq + 1);
      const lvalue = value.toLowerCase();
      if (key === "max_size") {
        const n = parseNonNegativeFamilyInt(value);
        if (n === null) return { ok: false, usage };
        maxSize = n;
      } else if (key === "restart_prob") {
        const f = parseFamilyRestartProb(value);
        if (f === null) return { ok: false, usage };
        restartProb = f;
      } else if (key === "epsilon") {
        const f = parsePositiveFamilyFloat(value);
        if (f === null) return { ok: false, usage };
        epsilon = f;
      } else if (key === "reduction") {
        if (!ILL_REDUCTIONS.has(lvalue as ReductionName))
          return { ok: false, usage };
        reduction = lvalue as ReductionName;
      } else if (key === "objective") {
        if (!ILL_OBJECTIVES.has(lvalue as ObjectiveName))
          return { ok: false, usage };
        objective = lvalue as ObjectiveName;
      } else if (key === "weighting") {
        if (!ILL_WEIGHTINGS.has(lvalue as WeightingName))
          return { ok: false, usage };
        weighting = lvalue as WeightingName;
      } else if (key === "prefix") {
        if (value === "") return { ok: false, usage };
        vertexPrefix = value;
      } else {
        return { ok: false, usage };
      }
      continue;
    }
    if (pos === 0) {
      const n = parseNonNegativeFamilyInt(tok);
      if (n === null) return { ok: false, usage };
      maxSize = n;
    } else {
      return { ok: false, usage };
    }
    pos++;
  }
  return {
    ok: true,
    command: {
      verb: "community",
      seed,
      maxSize,
      restartProb,
      epsilon,
      reduction,
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
 * `parseIlluminate`'s `ILL_ALGORITHMS` / `ILL_REDUCTIONS` / `ILL_OBJECTIVES`
 * / `ILL_WEIGHTINGS` sets and the corresponding Go REPL parser.
 */
export const HELP_TEXT = [
  "Lantern CLI grammar:",
  "",
  "  get    vertex <key: string>",
  "  get    edge   <tail: string> <head: string>",
  "  put    vertex <key: string> <value: string|int|float|bool|datetime> [<ttl_seconds: int>] [type=auto|string|int|float|bool|datetime|duration|json]",
  "  put    edge   <tail: string> <head: string> <weight: float> [<ttl_seconds: int>]",
  "  add    edge   <tail: string> <head: string> <weight: float> [<ttl_seconds: int>]",
  "  add    decaying-edge <tail: string> <head: string> <initial_weight: float> <ratio: float> <steps: int> <interval_seconds: int>",
  "  delete vertex <key: string> [<key: string> ...]",
  "  delete edge   <tail: string> <head: string> [<tail: string> <head: string> ...]",
  "  scan   vertices <prefix: string> [<limit: int>] [all=true]",
  "  scan   edges    <tail-prefix: string> [<limit: int>] [head=<prefix>] [all=true]",
  "  count  vertices <prefix: string>",
  "  delete-prefix vertices <prefix: string> [limit=<int>] [confirm=yes|dry_run=true]",
  "  keys   <prefix: string> [<limit: int>]",
  "  search <query: string> [limit=<uint32>] [prefix=<string>] [mode=server|any|all|min-should] [min_should=<uint32>] [phrase=<bool>] [fuzziness=0|1|2] [prefix_terms=<bool>] [cursor=<base64url>] [all=<bool>] [projection=key-score|full-vertex] [format=json|ndjson|tsv]",
  "  bfs        <seed: string> [step: int] [fan_out: int]",
  "             [reduction={none|mst|spt}]  default=none",
  "             [objective={min|max}]       default=max",
  "             [weighting={raw|tfidf|bm25}] default=raw",
  "             [prefix=<string>]           default=all keys",
  "             defaults: step=5 fan_out=3",
  "  pagerank   <seed: string> [top_n: int]",
  "             [restart_prob=<float>]      default=0 (server α=0.15)",
  "             [epsilon=<float>]           default=0 (server ε=1e-4)",
  "             [weighting={raw|tfidf|bm25}] default=raw",
  "             [prefix=<string>]           default=all keys",
  "             defaults: top_n=10",
  "  community  <seed: string> [max_size: int]",
  "             [restart_prob=<float>]      default=0 (server α=0.15)",
  "             [epsilon=<float>]           default=0 (server ε=1e-4)",
  "             [reduction={none|mst|spt}]  default=none",
  "             [objective={min|max}]       default=max",
  "             [weighting={raw|tfidf|bm25}] default=raw",
  "             [prefix=<string>]           default=all keys",
  "             defaults: max_size=0 (sweep decides)",
  "  help [search|bfs|pagerank|community]",
  "  exit",
  "",
  "Search contract: https://github.com/anaregdesign/lantern/blob/main/docs/search.md",
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
  /** Focused help fields for commands whose parameters need a full reference. */
  readonly scopedHelp?: {
    readonly defaults: readonly string[];
    readonly domains: readonly string[];
    readonly meaning: string;
    readonly examples: readonly string[];
  };
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
    verb: "add",
    signature:
      "add decaying-edge <tail> <head> <initial_weight> <ratio> <steps> <interval_seconds>",
    summary:
      "Add a geometric decay staircase: the edge starts at initial_weight and decays by ratio each step, one contribution per interval_seconds. Client-side only; range checks apply at execution.",
    example: "add decaying-edge alice bob 16 0.5 5 1",
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
    group: "Browse",
    verb: "search",
    signature:
      "search <query> [limit=<uint32>] [prefix=<string>] [mode=server|any|all|min-should] [min_should=<uint32>] [phrase=<bool>] [fuzziness=0|1|2] [prefix_terms=<bool>] [cursor=<base64url>] [all=<bool>] [projection=key-score|full-vertex] [format=json|ndjson|tsv]",
    summary:
      "BM25 content search with cursor paging. One page is lossless JSON; all=true follows the bounded search session. Canonical contract: https://github.com/anaregdesign/lantern/blob/main/docs/search.md",
    example: 'search "rolling update" mode=all limit=20',
    scopedHelp: {
      defaults: [
        "limit=0 (endpoint default)",
        "prefix=all keys",
        "mode=server (endpoint default)",
        "min_should=0 (endpoint default when mode=min-should)",
        "phrase=false",
        "fuzziness=0 (exact terms)",
        "prefix_terms=false",
        "cursor=first page",
        "all=false",
        "projection=key-score",
        "format=json (all=true defaults to ndjson)",
      ],
      domains: [
        "query: required full-text input analyzed and ranked by BM25 relevance",
        "limit: maximum hits per page; 0 uses the endpoint default and the endpoint applies its cap",
        "prefix: candidate vertex-key namespace; empty searches every key",
        "mode: server defers to endpoint config; any=OR; all=AND; min-should requires N distinct words",
        "min_should: positive word threshold used only with mode=min-should",
        "phrase: adjacent ordered words within one key, value, or JSON string field",
        "fuzziness: maximum dictionary-term edit distance 0|1|2",
        "prefix_terms: include dictionary terms extending a query word (lan matches lantern)",
        "cursor: opaque unpadded URL-safe base64 continuation from the same request and endpoint",
        "all: lazily follow the bounded endpoint-sticky cursor session",
        "projection: key-score or the exact selection-time full-vertex value/TTL snapshot",
        "format: lossless page JSON, per-hit NDJSON, or quoted TSV",
        "Compatibility: phrase=true requires mode=server, fuzziness=0, prefix_terms=false, and endpoint positional postings; all=true requires ndjson or tsv",
      ],
      meaning:
        "Search live vertex keys and values by relevance without treating the derived index as a source of truth. See https://github.com/anaregdesign/lantern/blob/main/docs/search.md.",
      examples: [
        'search "rolling update" mode=all limit=20',
        'search "release notes" phrase=true',
        "search serach fuzziness=1",
        "search espresso limit=20 all=true format=ndjson",
      ],
    },
  },
  {
    group: "Explore",
    verb: "bfs",
    signature:
      "bfs <seed> [step] [fan_out] [reduction=none|mst|spt] [objective=min|max] [weighting=raw|tfidf|bm25] [prefix=<string>]",
    summary:
      "Greedy per-hop top-k breadth-first walk from a seed (step hops, fan_out neighbours per hop; defaults 5/3). reduction=mst|spt renders the neighbourhood as a spanning / shortest-path tree (#961); objective steers the pruning and reduction direction.",
    example: "bfs alice 2 5 reduction=mst",
    scopedHelp: {
      defaults: [
        "step=5",
        "fan_out=3",
        "reduction=none",
        "objective=max",
        "weighting=raw",
        "prefix=all keys",
      ],
      domains: [
        "reduction: none|mst|spt",
        "objective: min|max",
        "weighting: raw|tfidf|bm25",
      ],
      meaning:
        "Greedy per-hop top-k breadth-first walk. objective controls both frontier pruning and any directed-arborescence / shortest-path reduction.",
      examples: ["bfs alice 2 5", "bfs alice 3 20 reduction=mst objective=min"],
    },
  },
  {
    group: "Explore",
    verb: "pagerank",
    signature:
      "pagerank <seed> [top_n] [restart_prob=<float>] [epsilon=<float>] [weighting=raw|tfidf|bm25] [prefix=<string>]",
    summary:
      "Personalized PageRank relevance star from a seed (top_n by mass, default 10). restart_prob (α) and epsilon (ε) tune locality; both default to the server's 0.15 / 1e-4.",
    example: "pagerank alice 15 restart_prob=0.25",
    scopedHelp: {
      defaults: [
        "top_n=10",
        "restart_prob=0 (server α=0.15)",
        "epsilon=0 (server ε=1e-4)",
        "weighting=raw",
        "prefix=all keys",
      ],
      domains: [
        "restart_prob: 0 or (0,1)",
        "epsilon: 0 or positive",
        "weighting: raw|tfidf|bm25",
      ],
      meaning:
        "Seed-anchored Personalized PageRank relevance star. It has no reduction or objective knob.",
      examples: [
        "pagerank alice",
        "pagerank alice 15 restart_prob=0.25 epsilon=0.001",
      ],
    },
  },
  {
    group: "Explore",
    verb: "community",
    signature:
      "community <seed> [max_size] [restart_prob=<float>] [epsilon=<float>] [reduction=none|mst|spt] [objective=min|max] [weighting=raw|tfidf|bm25] [prefix=<string>]",
    summary:
      "Conductance-optimal local community around a seed (#845; max_size upper bound, default 0 = the sweep decides). restart_prob/epsilon tune locality; reduction renders a tree view.",
    example: "community alice 20 reduction=mst",
    scopedHelp: {
      defaults: [
        "max_size=0 (sweep decides)",
        "restart_prob=0 (server α=0.15)",
        "epsilon=0 (server ε=1e-4)",
        "reduction=none",
        "objective=max",
        "weighting=raw",
        "prefix=all keys",
      ],
      domains: [
        "max_size: non-negative",
        "reduction: none|mst|spt",
        "objective: min|max",
        "weighting: raw|tfidf|bm25",
      ],
      meaning:
        "PageRank-Nibble conductance community returned as an induced subgraph; an optional reduction renders a rooted directed arborescence or shortest-path tree.",
      examples: [
        "community alice",
        "community alice 20 reduction=mst objective=min",
      ],
    },
  },
  {
    group: "Session",
    verb: "help",
    signature: "help [search|bfs|pagerank|community]",
    summary:
      "Print the grammar overview or a focused search/traversal reference into the terminal.",
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

/** Focused topics derive from the same docs that render the drawer. */
export const HELP_TOPICS = CLI_COMMAND_REFERENCE.filter(
  (doc) => doc.scopedHelp !== undefined,
).map((doc) => doc.verb) as readonly Exclude<HelpTopic, "">[];

/** Returns the overview or a command-only reference rendered from the drawer registry. */
export function helpTextFor(topic: HelpTopic): string {
  if (topic === "") return HELP_TEXT;
  const doc = CLI_COMMAND_REFERENCE.find(
    (candidate) => candidate.verb === topic,
  );
  if (doc?.scopedHelp === undefined) return HELP_TEXT;
  const help = doc.scopedHelp;
  return [
    topic,
    "",
    "Signature",
    `  ${doc.signature}`,
    "",
    "Defaults",
    ...help.defaults.map((value) => `  ${value}`),
    "",
    "Domains",
    ...help.domains.map((value) => `  ${value}`),
    "",
    "Meaning",
    `  ${help.meaning}`,
    "",
    "Examples",
    ...help.examples.map((value) => `  ${value}`),
  ].join("\n");
}

/** Parses the optional focused command help topic. */
export function parseHelp(rest: string[]): ParseResult {
  if (rest.length === 0)
    return { ok: true, command: { verb: "help", topic: "" } };
  if (rest.length !== 1) {
    return {
      ok: false,
      usage: "usage: help [search|bfs|pagerank|community]",
    };
  }
  const topic = rest[0].toLowerCase();
  if (!HELP_TOPICS.includes(topic as Exclude<HelpTopic, "">)) {
    return {
      ok: false,
      usage: `unknown help topic ${JSON.stringify(rest[0])} (try: ${HELP_TOPICS.join(", ")})`,
    };
  }
  return { ok: true, command: { verb: "help", topic: topic as HelpTopic } };
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

const MAX_UINT32 = 0xffff_ffff;

function parseFamilyUint32(s: string): number | null {
  if (!/^\d+$/.test(s)) return null;
  const n = Number(s);
  return Number.isSafeInteger(n) && n <= MAX_UINT32 ? n : null;
}

function parsePositiveFamilyInt(s: string): number | null {
  const n = parseFamilyUint32(s);
  return n !== null && n > 0 ? n : null;
}

function parseNonNegativeFamilyInt(s: string): number | null {
  const n = parseFamilyUint32(s);
  return n !== null && n >= 0 ? n : null;
}

function parseFamilyRestartProb(s: string): number | null {
  const n = parseFloatStrict(s);
  if (n === null) return null;
  const f = Math.fround(n);
  return Number.isFinite(f) && f > 0 && f < 1 ? f : null;
}

function parsePositiveFamilyFloat(s: string): number | null {
  const n = parseFloatStrict(s);
  if (n === null) return null;
  const f = Math.fround(n);
  return Number.isFinite(f) && f > 0 ? f : null;
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
