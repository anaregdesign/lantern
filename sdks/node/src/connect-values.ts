/**
 * Vertex / Edge value bridge for the additive LanternConnect client
 * (#340). Bridges the protobuf-es v2 JSON shape (oneof fields flat on
 * the message: `{ key: "k", string: "v" }`) to the existing SDK
 * Vertex / Edge / Graph value-object shapes the legacy `Lantern`
 * class already returns.
 *
 * Why a parallel bridge file (rather than extending values.ts): the
 * legacy values.ts depends on the ts-proto-generated PbVertex / PbEdge
 * classes. Touching it would force every legacy call site to update
 * imports. Keeping the new bridge separate lets both transports
 * coexist until #347 / #342 retire the legacy path wholesale.
 *
 * Re-exports the SAME `Vertex`, `Edge`, `Graph`, `VertexInput`,
 * `EdgeInput`, `VertexValue`, `VertexKind` types from values.ts so
 * downstream consumers do NOT have to know which transport they were
 * built against — `const v: Vertex` works the same way.
 */

import { Duration, type Int32, type Uint32, type Uint64, type Float32 } from "./values.js";
import {
  type Edge,
  type Vertex,
  type VertexInput,
  type VertexKind,
  type VertexValue,
} from "./values.js";

export type {
  Edge,
  EdgeInput,
  Graph,
  Vertex,
  VertexInput,
  VertexKind,
  VertexValue,
} from "./values.js";

const TWO_POW_63 = 1n << 63n;

/**
 * Build the protobuf JSON shape for a VertexInput so it can feed
 * `fromJson(VertexSchema, ...)` without ever touching the proto class
 * shape. The oneof field name lives flat on the JSON object per the
 * proto3 JSON mapping spec (so `value: "hello"` becomes
 * `string: "hello"`), which is the exact representation the server's
 * Connect+JSON codec emits and accepts.
 */
export function toPbVertexJson(input: VertexInput): Record<string, unknown> {
  const out: Record<string, unknown> = { key: input.key };
  const exp = resolveExpiration(input.ttlSeconds, input.expiration);
  if (exp) out.expiration = exp.toISOString();
  encodeValue(out, input.value);
  return out;
}

function encodeValue(out: Record<string, unknown>, value: VertexInput["value"]): void {
  if (value === null) {
    // The proto `nil` field is a `bool` that is always true when
    // present — it is the existence-only marker for a vertex carrying
    // no payload. See proto/graph/v1/graph.proto.
    out.nil = true;
    return;
  }
  if (typeof value === "string") {
    out.string = value;
    return;
  }
  if (typeof value === "boolean") {
    out.bool = value;
    return;
  }
  if (value instanceof Uint8Array) {
    out.bytes = bytesToBase64(value);
    return;
  }
  if (value instanceof Date) {
    out.timestamp = value.toISOString();
    return;
  }
  if (value instanceof Duration) {
    out.duration = value.toString();
    return;
  }
  // Marker classes: Int32, Uint32, Uint64, Float32. Duck-type the
  // `.value` field so this file does not need to import the marker
  // class identifiers (avoids a circular import with values.ts).
  if (isMarker(value, "Int32")) {
    out.int32 = (value as Int32).value;
    return;
  }
  if (isMarker(value, "Uint32")) {
    out.uint32 = (value as Uint32).value;
    return;
  }
  if (isMarker(value, "Uint64")) {
    out.uint64 = (value as Uint64).value.toString();
    return;
  }
  if (isMarker(value, "Float32")) {
    out.float32 = (value as Float32).value;
    return;
  }
  if (typeof value === "number") {
    if (Number.isInteger(value)) {
      out.int64 = String(value);
    } else {
      out.float64 = value;
    }
    return;
  }
  if (typeof value === "bigint") {
    if (value >= 0n && value >= TWO_POW_63) {
      out.uint64 = value.toString();
    } else {
      out.int64 = value.toString();
    }
    return;
  }
  throw new TypeError(`unsupported VertexInput.value: ${typeof value}`);
}

function isMarker(value: unknown, name: string): boolean {
  return (
    typeof value === "object" &&
    value !== null &&
    (value as { constructor?: { name?: string } }).constructor?.name === name
  );
}

/**
 * Decode a Vertex from its protobuf JSON form. The input matches what
 * `toJson(VertexSchema, msg)` produces (one of the oneof fields
 * appears flat on the object: `{ key, string }` / `{ key, int64 }`
 * / ...).
 */
export function fromPbVertexJson(json: Record<string, unknown>): Vertex {
  const key = String(json.key ?? "");
  const expiration = parseExpiration(json.expiration);
  const { value, kind } = decodeValue(json);
  return { key, value, kind, expiration };
}

function decodeValue(json: Record<string, unknown>): { value: VertexValue; kind: VertexKind } {
  if (json.float64 !== undefined) return { value: Number(json.float64), kind: "float64" };
  if (json.float32 !== undefined) return { value: Number(json.float32), kind: "float32" };
  if (json.int32 !== undefined) return { value: Number(json.int32), kind: "int32" };
  if (json.int64 !== undefined) return { value: BigInt(String(json.int64)), kind: "int64" };
  if (json.uint32 !== undefined) return { value: Number(json.uint32), kind: "uint32" };
  if (json.uint64 !== undefined) return { value: BigInt(String(json.uint64)), kind: "uint64" };
  if (json.bool !== undefined) return { value: Boolean(json.bool), kind: "bool" };
  if (json.string !== undefined) return { value: String(json.string), kind: "string" };
  if (json.bytes !== undefined) {
    return { value: base64ToBytes(String(json.bytes)), kind: "bytes" };
  }
  if (json.timestamp !== undefined) {
    return { value: new Date(String(json.timestamp)), kind: "timestamp" };
  }
  if (json.duration !== undefined) {
    // Duration parsing lives on the legacy Duration class as a private
    // helper. We re-implement minimal Go-duration parsing here to avoid
    // exporting an internal hook. Supported forms: "10s", "1m30s",
    // "1.5h", "2.5ms". Falls back to a zero Duration on parse failure
    // so the bridge never throws on otherwise-valid messages.
    return { value: parseGoDuration(String(json.duration)), kind: "duration" };
  }
  if (json.nil !== undefined) return { value: null, kind: "nil" };
  return { value: null, kind: "unset" };
}

export function fromPbEdgeJson(json: Record<string, unknown>): Edge {
  return {
    tail: String(json.tail ?? ""),
    head: String(json.head ?? ""),
    weight: Number(json.weight ?? 0),
    expiration: parseExpiration(json.expiration),
  };
}

function parseExpiration(raw: unknown): Date | null {
  if (typeof raw !== "string" || raw === "") return null;
  const ms = Date.parse(raw);
  return Number.isFinite(ms) ? new Date(ms) : null;
}

function resolveExpiration(ttlSeconds?: number, expiration?: Date): Date | undefined {
  if (expiration !== undefined && ttlSeconds !== undefined) {
    throw new TypeError("specify either ttlSeconds or expiration, not both");
  }
  if (expiration !== undefined) return expiration;
  if (ttlSeconds !== undefined) return new Date(Date.now() + ttlSeconds * 1000);
  return undefined;
}

function bytesToBase64(bytes: Uint8Array): string {
  return Buffer.from(bytes).toString("base64");
}

function base64ToBytes(b64: string): Uint8Array {
  return new Uint8Array(Buffer.from(b64, "base64"));
}

/**
 * Minimal Go-duration parser ("10s", "1m30s", "1.5h", "2.5ms") that
 * returns a Duration. Used only by the decode path; the legacy
 * Duration class owns the encode path via `toString()`.
 */
function parseGoDuration(s: string): Duration {
  const trimmed = s.trim();
  if (!trimmed) return new Duration(0n, 0);
  const re = /([0-9]*\.?[0-9]+)(ns|us|µs|ms|s|m|h)/g;
  const unitMultiplierNs: Record<string, bigint> = {
    ns: 1n,
    us: 1_000n,
    µs: 1_000n,
    ms: 1_000_000n,
    s: 1_000_000_000n,
    m: 60n * 1_000_000_000n,
    h: 3600n * 1_000_000_000n,
  };
  let totalNs = 0n;
  let match: RegExpExecArray | null;
  while ((match = re.exec(trimmed)) !== null) {
    const n = Number(match[1]);
    const mult = unitMultiplierNs[match[2] ?? ""] ?? 0n;
    if (Number.isFinite(n)) {
      totalNs += BigInt(Math.round(n * Number(mult)));
    }
  }
  const seconds = totalNs / 1_000_000_000n;
  const nanos = Number(totalNs % 1_000_000_000n);
  return new Duration(seconds, nanos);
}
