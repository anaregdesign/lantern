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
 *     decay matches what the user typed (#429). Mirrors Go REPL
 *     `cli/service/service.go` `c.client.PutVertex(ctx, key, value, ttl)`.
 *   - `get vertex` / `get edge` raise `LanternApiError.notFound` when the
 *     adapter returned `null` so the scrollback renders a red `[not_found]`
 *     error chip rather than a misleading `OK` (#430).
 *   - `scan vertices` no longer issues an extra `countVerticesByPrefix`
 *     RPC the Go REPL does not make and `scan edges` does not make either
 *     (#432).
 */

import type { LanternClient } from "~/lib/client/infrastructure/api/lantern-client";
import { addEdge } from "~/lib/client/infrastructure/api/add-edge";
import { deleteEdge } from "~/lib/client/infrastructure/api/delete-edge";
import { deleteVertex } from "~/lib/client/infrastructure/api/delete-vertex";
import { LanternApiError } from "~/lib/client/infrastructure/api/error";
import { getEdge } from "~/lib/client/infrastructure/api/get-edge";
import { getVertex } from "~/lib/client/infrastructure/api/get-vertex";
import {
  illuminate,
  type Algorithm as ApiAlgorithm,
  type Objective as ApiObjective,
  type Weighting as ApiWeighting,
} from "~/lib/client/infrastructure/api/illuminate";
import { putEdge } from "~/lib/client/infrastructure/api/put-edge";
import { putVertex } from "~/lib/client/infrastructure/api/put-vertex";
import { scanEdges } from "~/lib/client/infrastructure/api/scan-edges";
import { scanVertices } from "~/lib/client/infrastructure/api/scan-vertices";
import type { Vertex } from "~/lib/client/infrastructure/api/types";
import type { Command } from "./types";

export interface DispatchInput {
  client: LanternClient;
  command: Command;
  signal?: AbortSignal;
}

const ALGORITHM_TO_API: Record<string, ApiAlgorithm> = {
  none: "ALGORITHM_UNSPECIFIED",
  mst: "ALGORITHM_MINIMUM_SPANNING_TREE",
  spt: "ALGORITHM_SHORTEST_PATH_TREE",
};
const OBJECTIVE_TO_API: Record<string, ApiObjective> = {
  min: "OBJECTIVE_MINIMIZE",
  max: "OBJECTIVE_MAXIMIZE",
};
const WEIGHTING_TO_API: Record<string, ApiWeighting> = {
  raw: "WEIGHTING_RAW",
  tfidf: "WEIGHTING_TFIDF",
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
        const vertex: Vertex = {
          key: command.key,
          ...coerceValue(command.value),
          expiration: ttlSecondsToExpiration(command.ttlSeconds),
        };
        await putVertex(client, command.key, { vertex }, { signal });
        return { ok: true };
      }
      await putEdge(
        client,
        command.tail,
        command.head,
        {
          edge: {
            tail: command.tail,
            head: command.head,
            weight: command.weight,
            expiration: ttlSecondsToExpiration(command.ttlSeconds),
          },
        },
        { signal },
      );
      return { ok: true };
    case "delete":
      if (command.objective === "vertex") {
        return deleteVertex(client, command.key, { signal });
      }
      return deleteEdge(client, command.tail, command.head, { signal });
    case "add":
      await addEdge(
        client,
        command.tail,
        command.head,
        {
          edge: {
            tail: command.tail,
            head: command.head,
            weight: command.weight,
            expiration: ttlSecondsToExpiration(command.ttlSeconds),
          },
        },
        { signal },
      );
      return { ok: true };
    case "scan":
      if (command.objective === "vertices") {
        return scanVertices(
          client,
          { prefix: command.prefix, limit: command.limit },
          { signal },
        );
      }
      return scanEdges(
        client,
        { tailPrefix: command.tailPrefix, limit: command.limit },
        { signal },
      );
    case "illuminate":
      return illuminate(
        client,
        {
          seed: command.seed,
          step: command.step,
          k: command.k,
          algorithm: ALGORITHM_TO_API[command.algorithm],
          objective: OBJECTIVE_TO_API[command.objective],
          weighting: WEIGHTING_TO_API[command.weighting],
        },
        { signal },
      );
  }
}

/**
 * Returns true when the command would mutate server state. Used by
 * the React panel to gate destructive verbs behind a confirmation
 * chip (#411).
 */
export function isDestructive(command: Command): boolean {
  return (
    command.verb === "delete" ||
    command.verb === "put" ||
    command.verb === "add"
  );
}

// ----------------------------------------------------------------------------
// Internals
// ----------------------------------------------------------------------------

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
): Pick<Vertex, "string" | "int64" | "float64" | "bool" | "timestamp"> {
  if (INT_RE.test(raw)) {
    return { int64: raw };
  }
  if (FLOAT_RE.test(raw)) {
    return { float64: Number.parseFloat(raw) };
  }
  if (TRUE_TOKENS.has(raw)) {
    return { bool: true };
  }
  if (FALSE_TOKENS.has(raw)) {
    return { bool: false };
  }
  if (RFC3339_RE.test(raw) && !Number.isNaN(Date.parse(raw))) {
    return { timestamp: raw };
  }
  return { string: raw };
}

/**
 * Convert a parser-supplied TTL (in whole seconds; the verbs default
 * to one year) to the ISO-8601 absolute expiration the wire format
 * carries. Mirrors `cli/service/service.go`'s `time.Now().Add(ttl)`
 * server-side computation.
 *
 * Exported for unit testing.
 */
export function ttlSecondsToExpiration(ttlSeconds: number): string {
  return new Date(Date.now() + ttlSeconds * 1000).toISOString();
}
