/**
 * Whole-graph backup / restore for the Node SDK (#685, parity with the Go
 * SDK's `backup.go`). `Lantern.backup` streams the server's `BackupSnapshot`
 * RPC as an async iterable of decoded {@link BackupRecord}s; `Lantern.restore`
 * replays those records through the batch `putVertices` / `putEdges` surface
 * (there is no dedicated restore RPC).
 *
 * Unlike the Go SDK — which serialises to an `io.Writer` in one of two wire
 * formats — the Node SDK yields the record stream directly, leaving
 * serialisation to the caller. {@link backupRecordToNdjson} /
 * {@link backupRecordFromNdjson} provide a ready **Node-native** NDJSON line
 * codec for the common "dump to a file, restore it later" workflow: a naive
 * `JSON.stringify(record)` is a footgun (it throws on `bigint` values and
 * mangles `Uint8Array` / `Date` / `Duration`), whereas this codec preserves
 * every value type exactly. It is a Node↔Node format — it does **not** share a
 * byte layout with the Go SDK's `FormatNDJSON` (Go frames a
 * `{type,value}` envelope and emits 64-bit ints as bare JSON numbers, which JS
 * cannot read back losslessly above 2^53), so cross-SDK NDJSON exchange is out
 * of scope. The `BackupRecord` stream itself is lossless: decoded vertices
 * carry their `kind`, so narrow numeric value types
 * (int32/uint32/uint64/float32/duration) survive a round-trip exactly.
 */

import { InvalidArgumentError } from "./errors.js";
import {
  edgeRecordToJson,
  fromEdgeJson,
  fromVertexJson,
  vertexRecordToJson,
  type Edge,
  type Vertex,
} from "./values.js";

/**
 * One frame of a backup stream: either a live vertex or a folded live edge
 * (weight summed across contributions, expiration = the furthest-future
 * contribution). Consumers route on the `kind` discriminator.
 */
export type BackupRecord =
  | { readonly kind: "vertex"; readonly vertex: Vertex }
  | { readonly kind: "edge"; readonly edge: Edge };

/** Counts of what a {@link import("./client.js").Lantern.restore} call loaded. */
export interface RestoreStats {
  vertices: number;
  edges: number;
}

/**
 * Accepted input to `restore`: any sync or async iterable of
 * {@link BackupRecord} — the output of `backup`, a decoded NDJSON file, an
 * in-memory array, etc. `for await` consumes both kinds.
 */
export type RestoreSource = Iterable<BackupRecord> | AsyncIterable<BackupRecord>;

/**
 * Default batch size for the `putVertices` / `putEdges` calls `restore`
 * issues. Mirrors the Go SDK's `defaultRestoreChunkSize`.
 */
export const DEFAULT_RESTORE_CHUNK_SIZE = 1000;

/**
 * Render a {@link BackupRecord} as a single NDJSON line (no trailing newline),
 * splicing a `"kind"` discriminator (`"vertex"` / `"edge"`) onto the flat
 * protobuf-JSON value shape. int64/uint64 magnitudes are carried as strings
 * (so values above 2^53 survive) and every narrow numeric kind is preserved,
 * so the line round-trips losslessly through {@link backupRecordFromNdjson}.
 * Prefer this over a naive `JSON.stringify(record)`, which throws on `bigint`
 * values and mangles `Uint8Array` / `Date` / `Duration`. This is a Node-native
 * layout — see the module header; it is not interchangeable with the Go SDK's
 * `FormatNDJSON`.
 */
export function backupRecordToNdjson(rec: BackupRecord): string {
  if (rec.kind === "vertex") {
    return JSON.stringify({ kind: "vertex", ...vertexRecordToJson(rec.vertex) });
  }
  return JSON.stringify({ kind: "edge", ...edgeRecordToJson(rec.edge) });
}

/**
 * Parse one NDJSON line produced by {@link backupRecordToNdjson} back into a
 * {@link BackupRecord}. Routes on the `"kind"` field; the discriminator is
 * ignored by the value decoders.
 *
 * @throws {InvalidArgumentError} when `kind` is missing or unrecognised.
 */
export function backupRecordFromNdjson(line: string): BackupRecord {
  const obj = JSON.parse(line) as Record<string, unknown>;
  switch (obj.kind) {
    case "vertex":
      return { kind: "vertex", vertex: fromVertexJson(obj) };
    case "edge":
      return { kind: "edge", edge: fromEdgeJson(obj) };
    default:
      throw new InvalidArgumentError(
        `backup: unknown or missing record kind ${JSON.stringify(obj.kind)}`,
      );
  }
}
