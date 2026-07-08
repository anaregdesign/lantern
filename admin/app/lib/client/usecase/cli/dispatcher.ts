/**
 * Verb dispatcher (#411). Each parsed `Command` from
 * `lib/cli/parser.ts` is routed to the matching SDK call (or pair
 * of calls) on a pre-built `LanternClient`. The dispatcher returns
 * a JSON-serialisable result so the scrollback panel can pretty-print
 * any verb's output via `JSON.stringify`.
 *
 * Errors propagate as `LanternApiError` (the same shape every other
 * usecase consumes) so the scrollback can render `err.code`,
 * `err.grpcMessage`, or fall back to `err.message` consistently.
 *
 * Parity with `cli/parser/parser.go` `Value()` and `cli/service/service.go`:
 *   - `put vertex` runs `coerceValue` over the raw token, mirroring the
 *     Go cascade (int → float → bool → RFC3339 → string) so the admin
 *     CLI and the Go REPL store the same vertex kind for the same input
 *     (#428).
 *   - `put vertex` / `put edge` / `add edge` carry the parser-supplied
 *     `ttlSeconds` through as `expiration = now + ttl` so server-side
 *     decay matches what the user typed (#429); an omitted `ttl_seconds`
 *     (null) sends no expiration and is stored permanently (#523).
 *     Mirrors Go REPL `cli/service/service.go`
 *     `c.client.PutVertex(ctx, key, value, ttl)`.
 *   - `get vertex` / `get edge` raise `LanternApiError.notFound` when the
 *     adapter returned `null` so the scrollback renders a red `[not_found]`
 *     error chip rather than a misleading `OK` (#430).
 *   - `scan vertices` no longer issues an extra `countVerticesByPrefix`
 *     RPC the Go REPL does not make and `scan edges` does not make either
 *     (#432).
 */

import type { LanternClient } from "~/lib/client/infrastructure/api/lantern-client";
import { addDecayingEdge } from "~/lib/client/infrastructure/api/add-decaying-edge";
import { addEdge } from "~/lib/client/infrastructure/api/add-edge";
import { countVerticesByPrefix } from "~/lib/client/infrastructure/api/count-vertices-by-prefix";
import { deleteEdge } from "~/lib/client/infrastructure/api/delete-edge";
import { deleteEdges } from "~/lib/client/infrastructure/api/delete-edges";
import { deleteVertex } from "~/lib/client/infrastructure/api/delete-vertex";
import { deleteVertices } from "~/lib/client/infrastructure/api/delete-vertices";
import { deleteVerticesByPrefix } from "~/lib/client/infrastructure/api/delete-vertices-by-prefix";
import { LanternApiError } from "~/lib/client/infrastructure/api/error";
import { getEdge } from "~/lib/client/infrastructure/api/get-edge";
import { getVertex } from "~/lib/client/infrastructure/api/get-vertex";
import {
  illuminate,
  type Objective as ApiObjective,
  type Reduction as ApiReduction,
  type Weighting as ApiWeighting,
} from "~/lib/client/infrastructure/api/illuminate";
import { putEdge } from "~/lib/client/infrastructure/api/put-edge";
import { putVertex } from "~/lib/client/infrastructure/api/put-vertex";
import { scanEdges } from "~/lib/client/infrastructure/api/scan-edges";
import { scanVertexKeys } from "~/lib/client/infrastructure/api/scan-vertex-keys";
import { scanVertices } from "~/lib/client/infrastructure/api/scan-vertices";
import type { Edge, Vertex } from "~/lib/client/infrastructure/api/types";
import type {
  Command,
  ObjectiveName,
  ReductionName,
  WeightingName,
} from "~/lib/cli/types";

export interface DispatchInput {
  client: LanternClient;
  command: Command;
  signal?: AbortSignal;
}

// Translate the CLI's friendly axis vocabulary to the wire enum the
// illuminate adapter consumes. Keyed by the CLOSED axis unions from
// `~/lib/cli/types` (not `string`) so that adding a token to `ReductionName`
// / `ObjectiveName` / `WeightingName` without a matching entry here is a `tsc`
// error, not a silent `undefined` that degrades to a default. Since #975 the
// traversal FAMILY is the verb itself (bfs / pagerank / community), so the
// family wire enum is chosen directly in each dispatch case rather than mapped
// from an axis.
const REDUCTION_TO_API: Record<ReductionName, ApiReduction> = {
  none: "REDUCTION_UNSPECIFIED",
  mst: "REDUCTION_MINIMUM_SPANNING_TREE",
  spt: "REDUCTION_SHORTEST_PATH_TREE",
};
const OBJECTIVE_TO_API: Record<ObjectiveName, ApiObjective> = {
  min: "OBJECTIVE_MINIMIZE",
  max: "OBJECTIVE_MAXIMIZE",
};
const WEIGHTING_TO_API: Record<WeightingName, ApiWeighting> = {
  raw: "WEIGHTING_RAW",
  tfidf: "WEIGHTING_TFIDF",
  bm25: "WEIGHTING_BM25",
};

/**
 * Dispatch one parsed REPL command. Returns whatever JSON-serialisable
 * shape the underlying RPC produced; the caller pretty-prints it
 * into the scrollback panel.
 *
 * `command.verb === "exit"` is handled by the caller (the React
 * component closes the prompt rather than dispatching).
 */
export async function dispatch(input: DispatchInput): Promise<unknown> {
  const { client, command, signal } = input;
  switch (command.verb) {
    case "exit":
      return null;
    case "help":
      // Help is intrinsically caller-handled (no RPC) — the React
      // panel intercepts it before reaching the dispatcher and
      // renders `HELP_TEXT` from `verbs.ts` into the scrollback.
      // The case lives here only so the switch stays exhaustive.
      return null;
    case "get":
      if (command.objective === "vertex") {
        const v = await getVertex(client, command.key, { signal });
        if (v === null) {
          throw LanternApiError.notFound(
            "GetVertex",
            `vertex "${command.key}" not found`,
          );
        }
        return v;
      }
      {
        const e = await getEdge(client, command.tail, command.head, {
          signal,
        });
        if (e === null) {
          throw LanternApiError.notFound(
            "GetEdge",
            `edge "${command.tail}" -> "${command.head}" not found`,
          );
        }
        return e;
      }
    case "put":
      if (command.objective === "vertex") {
        // Compute the expiration ONCE so the echo reports exactly the
        // instant sent to the server (a second `ttlSecondsToExpiration`
        // call would drift by the elapsed `Date.now()` delta).
        const expiration = ttlSecondsToExpiration(command.ttlSeconds);
        const vertex: Vertex = {
          key: command.key,
          ...coerceValue(command.value, command.valueType),
          expiration,
        };
        await putVertex(client, command.key, { vertex }, { signal });
        return writeEcho({ key: command.key }, command.ttlSeconds, expiration);
      }
      {
        const expiration = ttlSecondsToExpiration(command.ttlSeconds);
        await putEdge(
          client,
          command.tail,
          command.head,
          {
            edge: {
              tail: command.tail,
              head: command.head,
              weight: command.weight,
              expiration,
            },
          },
          { signal },
        );
        return writeEcho(
          { tail: command.tail, head: command.head, weight: command.weight },
          command.ttlSeconds,
          expiration,
        );
      }
    case "delete":
      if (command.objective === "vertex") {
        if (command.keys.length === 1) {
          return deleteVertex(client, command.keys[0], { signal });
        }
        return {
          deleted: await deleteVertices(client, command.keys, { signal }),
        };
      }
      if (command.pairs.length === 1) {
        return deleteEdge(
          client,
          command.pairs[0].tail,
          command.pairs[0].head,
          {
            signal,
          },
        );
      }
      return { deleted: await deleteEdges(client, command.pairs, { signal }) };
    case "add": {
      if (command.objective === "decaying-edge") {
        // The staircase has staggered per-step TTLs, so there is no single
        // applied TTL; echo the full decay horizon (steps × interval) as the
        // effective expiry — when the edge decays to zero — mirroring the Go
        // REPL's `add decaying-edge` echo.
        const horizonSeconds = command.steps * command.intervalSeconds;
        const expiration = ttlSecondsToExpiration(horizonSeconds);
        const { effectiveWeight } = await addDecayingEdge(
          client,
          command.tail,
          command.head,
          {
            initialWeight: command.initialWeight,
            ratio: command.ratio,
            steps: command.steps,
            intervalSeconds: command.intervalSeconds,
          },
          { signal },
        );
        return writeEcho(
          {
            tail: command.tail,
            head: command.head,
            initialWeight: command.initialWeight,
            ratio: command.ratio,
            steps: command.steps,
            total: effectiveWeight,
          },
          horizonSeconds,
          expiration,
        );
      }
      const expiration = ttlSecondsToExpiration(command.ttlSeconds);
      await addEdge(
        client,
        command.tail,
        command.head,
        {
          edge: {
            tail: command.tail,
            head: command.head,
            weight: command.weight,
            expiration,
          },
        },
        { signal },
      );
      return writeEcho(
        { tail: command.tail, head: command.head, weight: command.weight },
        command.ttlSeconds,
        expiration,
      );
    }
    case "scan":
      if (command.objective === "vertices") {
        if (command.all) {
          return scanAllVertices(client, command.prefix, command.limit, signal);
        }
        return scanVertices(
          client,
          { prefix: command.prefix, limit: command.limit },
          { signal },
        );
      }
      if (command.all) {
        return scanAllEdges(
          client,
          command.tailPrefix,
          command.headPrefix,
          command.limit,
          signal,
        );
      }
      return scanEdges(
        client,
        {
          tailPrefix: command.tailPrefix,
          headPrefix: command.headPrefix,
          limit: command.limit,
        },
        { signal },
      );
    case "count":
      return {
        count: await countVerticesByPrefix(client, command.prefix, { signal }),
      };
    case "delete-prefix": {
      const n = await deleteVerticesByPrefix(
        client,
        {
          prefix: command.prefix,
          limit: command.limit,
          dryRun: command.dryRun,
        },
        { signal },
      );
      return command.dryRun ? { wouldDelete: n } : { deleted: n };
    }
    case "keys":
      return scanVertexKeys(
        client,
        { prefix: command.prefix, limit: command.limit },
        { signal },
      );
    case "bfs":
      return illuminate(
        client,
        {
          seed: command.seed,
          step: command.step,
          k: command.fanOut,
          family: "FAMILY_BFS",
          reduction: REDUCTION_TO_API[command.reduction],
          objective: OBJECTIVE_TO_API[command.objective],
          weighting: WEIGHTING_TO_API[command.weighting],
          vertexPrefix: command.vertexPrefix,
        },
        { signal },
      );
    case "pagerank":
      return illuminate(
        client,
        {
          seed: command.seed,
          k: command.topN,
          family: "FAMILY_PERSONALIZED_PAGERANK",
          weighting: WEIGHTING_TO_API[command.weighting],
          vertexPrefix: command.vertexPrefix,
          restartProb: command.restartProb,
          epsilon: command.epsilon,
        },
        { signal },
      );
    case "community":
      return illuminate(
        client,
        {
          seed: command.seed,
          k: command.maxSize,
          family: "FAMILY_LOCAL_COMMUNITY",
          reduction: REDUCTION_TO_API[command.reduction],
          objective: OBJECTIVE_TO_API[command.objective],
          weighting: WEIGHTING_TO_API[command.weighting],
          vertexPrefix: command.vertexPrefix,
          restartProb: command.restartProb,
          epsilon: command.epsilon,
        },
        { signal },
      );
  }
}

// ----------------------------------------------------------------------------
// Internals
// ----------------------------------------------------------------------------

// Page through every `scan vertices` result so `all=true` renders the
// full set (mirrors the Go REPL's ScanVerticesAll loop). The infra
// adapter carries the opaque cursor as a base64 string.
async function scanAllVertices(
  client: LanternClient,
  prefix: string,
  limit: number,
  signal?: AbortSignal,
): Promise<{ vertices: Vertex[] }> {
  const vertices: Vertex[] = [];
  let cursor: string | undefined;
  do {
    const page = await scanVertices(
      client,
      { prefix, limit, cursor },
      { signal },
    );
    vertices.push(...(page.vertices ?? []));
    cursor = page.nextCursor || undefined;
  } while (cursor);
  return { vertices };
}

// Page through every `scan edges` result for `all=true`, honouring the
// optional head-prefix filter (mirrors the Go REPL's ScanEdgesAll loop).
async function scanAllEdges(
  client: LanternClient,
  tailPrefix: string,
  headPrefix: string,
  limit: number,
  signal?: AbortSignal,
): Promise<{ edges: Edge[] }> {
  const edges: Edge[] = [];
  let cursor: string | undefined;
  do {
    const page = await scanEdges(
      client,
      { tailPrefix, headPrefix, limit, cursor },
      { signal },
    );
    edges.push(...(page.edges ?? []));
    cursor = page.nextCursor || undefined;
  } while (cursor);
  return { edges };
}

const INT_RE = /^[+-]?\d+$/;
// Mirrors Go's `strconv.ParseFloat` decimal grammar without the
// "inf" / "nan" tokens (the Go REPL technically accepts them but the
// admin surface rejects them so the cascade falls through to bool /
// time / string instead of silently storing a degenerate float).
// See #434 for the alignment work tracked on the parser side.
const FLOAT_RE = /^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$/;
// Strict RFC3339 (mirrors Go's `time.Parse(time.RFC3339, ...)`):
// date "T" time, optional fractional seconds, `Z` or `±HH:MM` offset.
const RFC3339_RE =
  /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/;
// Exact set Go's `strconv.ParseBool` accepts.
const TRUE_TOKENS = new Set(["1", "t", "T", "TRUE", "true", "True"]);
const FALSE_TOKENS = new Set(["0", "f", "F", "FALSE", "false", "False"]);

/**
 * Coerce a single raw CLI token to the matching `Vertex` value field,
 * mirroring `cli/parser/parser.go` `Value()`:
 *
 *   int → float → bool → RFC3339 → string
 *
 * Integers are sent on `int64` (matching the Go switch where
 * `strconv.Atoi` → Go `int` → pb `Int64`). The string-encoded `int64`
 * field preserves precision past JS' 2^53 safe-integer limit so values
 * like `9223372036854775000` round-trip cleanly through the wire.
 *
 * Exported for unit testing.
 */
export function coerceValue(
  raw: string,
  valueType: string = "auto",
): Pick<
  Vertex,
  "string" | "int64" | "float64" | "bool" | "timestamp" | "duration"
> {
  switch (valueType) {
    case "":
    case "auto":
      if (INT_RE.test(raw)) return { int64: raw };
      if (FLOAT_RE.test(raw)) return { float64: Number.parseFloat(raw) };
      if (TRUE_TOKENS.has(raw)) return { bool: true };
      if (FALSE_TOKENS.has(raw)) return { bool: false };
      if (RFC3339_RE.test(raw) && !Number.isNaN(Date.parse(raw)))
        return { timestamp: raw };
      return { string: raw };
    case "string":
      return { string: raw };
    case "int":
      if (!INT_RE.test(raw)) throw valueTypeError("int", raw);
      return { int64: raw };
    case "float":
      if (!FLOAT_RE.test(raw)) throw valueTypeError("float", raw);
      return { float64: Number.parseFloat(raw) };
    case "bool":
      if (TRUE_TOKENS.has(raw)) return { bool: true };
      if (FALSE_TOKENS.has(raw)) return { bool: false };
      throw valueTypeError("bool", raw);
    case "datetime":
      if (!RFC3339_RE.test(raw) || Number.isNaN(Date.parse(raw)))
        throw valueTypeError("datetime", raw);
      return { timestamp: raw };
    case "duration":
      return { duration: raw };
    case "json":
      return coerceJsonValue(raw);
    default:
      throw valueTypeError(valueType, raw);
  }
}

// A value that does not coerce to its forced `type=`. The Go REPL rejects
// this at parse; on the web CLI the parser validates only the type NAME, so
// the mismatch surfaces here at dispatch.
function valueTypeError(typ: string, raw: string): Error {
  return new Error(
    `type=${typ}: cannot parse ${JSON.stringify(raw)} as ${typ}`,
  );
}

// `type=json`: parse the token; objects / arrays / null re-encode as a
// compact JSON string (the wire has no nested value variant), mirroring the
// Go `parseValue`. JSON numbers are float64 (Go's encoding/json convention).
function coerceJsonValue(
  raw: string,
): Pick<Vertex, "string" | "int64" | "float64" | "bool"> {
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    throw new Error(`type=json: invalid JSON ${JSON.stringify(raw)}`);
  }
  if (parsed === null || typeof parsed === "object") {
    return { string: JSON.stringify(parsed) };
  }
  if (typeof parsed === "boolean") return { bool: parsed };
  if (typeof parsed === "number") return { float64: parsed };
  return { string: String(parsed) };
}

/**
 * Convert a parser-supplied TTL (in whole seconds) to the ISO-8601
 * absolute expiration the wire format carries. A `null` TTL means the
 * verb omitted `ttl_seconds`, which maps to `undefined` so no
 * expiration is sent and the server stores the value permanently
 * (decay is opt-in; see #523). Mirrors `cli/service/service.go`'s
 * `time.Now().Add(ttl)` server-side computation for positive TTLs.
 *
 * Exported for unit testing.
 */
export function ttlSecondsToExpiration(
  ttlSeconds: number | null,
): string | undefined {
  if (ttlSeconds === null) return undefined;
  return new Date(Date.now() + ttlSeconds * 1000).toISOString();
}

/**
 * Build the success echo for a mutating write (#653).
 *
 * The dispatcher used to return a bare `{ ok: true }`, which hid the
 * applied TTL: `put vertex a a 1` looked permanent in the scrollback
 * yet silently decayed in one second, so a follow-up `get` returned
 * nothing and read as data loss. Surfacing the applied TTL and the
 * absolute expiry the server decays against makes the write
 * self-explanatory. `ttlSeconds === null` (the verb omitted
 * `ttl_seconds`) renders as `ttlSeconds: null, expiresAt: null` —
 * i.e. permanent, no decay (#523).
 *
 * Exported for unit testing.
 */
export function writeEcho(
  identity: Record<string, string | number>,
  ttlSeconds: number | null,
  expiration: string | undefined,
): Record<string, unknown> {
  return { ...identity, ttlSeconds, expiresAt: expiration ?? null };
}
