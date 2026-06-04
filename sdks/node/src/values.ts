/**
 * Value-type bridge between JS natives and the protobuf Vertex/Edge messages.
 *
 * Native ↔ proto mapping (mirrors the Go and Python SDKs):
 *
 *   JS value                               proto oneof
 *   ─────────────────────────────────────  ─────────────────────────
 *   number (Number.isInteger, in safe)     int64
 *   number (non-integer)                   float64
 *   bigint (in [-(2^63), 2^63))            int64
 *   bigint (in [2^63, 2^64))               uint64
 *   string                                 string
 *   boolean                                bool
 *   Uint8Array                             bytes
 *   Date                                   timestamp
 *   Duration (sdk type)                    duration
 *   null                                   nil tombstone
 *
 * To pin a narrower wire type, wrap with `int32`, `uint32`, `uint64`, or
 * `float32`. Each marker range-checks at construction and throws
 * OverflowError on out-of-range input.
 */

import Long from "long";

import { OverflowError } from "./errors.js";
import { Duration as PbDuration } from "./generated/google/protobuf/duration.js";
import { type Edge as PbEdge, type Vertex as PbVertex } from "./generated/graph/v1/graph.js";

// ----------------------------------------------------------------------------
// Narrowing markers
// ----------------------------------------------------------------------------

export class Int32 {
  static readonly MIN = -2147483648;
  static readonly MAX = 2147483647;
  readonly value: number;
  constructor(value: number) {
    if (!Number.isInteger(value)) {
      throw new TypeError(`Int32 requires an integer, got ${value}`);
    }
    if (value < Int32.MIN || value > Int32.MAX) {
      throw new OverflowError(`${value} out of range for Int32 [${Int32.MIN}, ${Int32.MAX}]`);
    }
    this.value = value;
  }
}

export class Uint32 {
  static readonly MIN = 0;
  static readonly MAX = 4294967295;
  readonly value: number;
  constructor(value: number) {
    if (!Number.isInteger(value)) {
      throw new TypeError(`Uint32 requires an integer, got ${value}`);
    }
    if (value < 0 || value > Uint32.MAX) {
      throw new OverflowError(`${value} out of range for Uint32 [0, ${Uint32.MAX}]`);
    }
    this.value = value;
  }
}

export class Uint64 {
  static readonly MAX = (1n << 64n) - 1n;
  readonly value: bigint;
  constructor(value: number | bigint) {
    const bi = typeof value === "bigint" ? value : BigInt(value);
    if (bi < 0n || bi > Uint64.MAX) {
      throw new OverflowError(`${bi} out of range for Uint64 [0, ${Uint64.MAX}]`);
    }
    this.value = bi;
  }
}

export class Float32 {
  readonly value: number;
  constructor(value: number) {
    if (typeof value !== "number") {
      throw new TypeError(`Float32 requires a number, got ${typeof value}`);
    }
    this.value = value;
  }
}

/**
 * SDK-side Duration carrier. Construct via `Duration.fromMillis(ms)` or
 * `new Duration(seconds, nanos)`. Use in `putVertex` / `vertexInput` to
 * pin the `duration` proto oneof variant. Read-side returns ms as
 * `number` via Vertex.value when kind === "duration".
 */
export class Duration {
  readonly seconds: bigint;
  readonly nanos: number;
  constructor(seconds: bigint | number, nanos = 0) {
    this.seconds = typeof seconds === "bigint" ? seconds : BigInt(seconds);
    this.nanos = nanos | 0;
  }
  static fromMillis(ms: number): Duration {
    const sign = ms < 0 ? -1 : 1;
    const abs = Math.abs(ms);
    const sec = Math.trunc(abs / 1000);
    const remMs = abs - sec * 1000;
    return new Duration(BigInt(sec * sign), Math.round(remMs * 1_000_000) * sign);
  }
  toMillis(): number {
    return Number(this.seconds) * 1000 + this.nanos / 1_000_000;
  }
}

// ----------------------------------------------------------------------------
// VertexKind discriminator + SDK-side Vertex/Edge types
// ----------------------------------------------------------------------------

export type VertexKind =
  | "unset"
  | "float32"
  | "float64"
  | "int32"
  | "int64"
  | "uint32"
  | "uint64"
  | "bool"
  | "string"
  | "bytes"
  | "timestamp"
  | "duration"
  | "nil";

/**
 * Server-side post-processing strategy for Lantern.illuminate. Values match
 * the proto enum exactly; pass `Optimization.UNSPECIFIED` to disable.
 */
export const Optimization = {
  UNSPECIFIED: 0,
  MINIMUM_SPANNING_TREE: 1,
  MAXIMUM_SPANNING_TREE: 2,
  SHORTEST_PATH_TREE: 3,
  SHORTEST_PATH_TREE_INVERSE: 4,
} as const;
export type Optimization = (typeof Optimization)[keyof typeof Optimization];

export interface Vertex {
  readonly key: string;
  readonly value: VertexValue;
  readonly kind: VertexKind;
  readonly expiration: Date | null;
}

export type VertexValue = null | number | bigint | boolean | string | Uint8Array | Date | Duration;

export interface Edge {
  readonly tail: string;
  readonly head: string;
  readonly weight: number;
  readonly expiration: Date | null;
}

export interface VertexInput {
  key: string;
  value: VertexValue | Int32 | Uint32 | Uint64 | Float32;
  ttlSeconds?: number;
  expiration?: Date;
}

export interface EdgeInput {
  tail: string;
  head: string;
  weight: number;
  ttlSeconds?: number;
  expiration?: Date;
}

export interface Graph {
  vertices: Map<string, Vertex>;
  edges: Map<string, Map<string, number>>;
}

// ----------------------------------------------------------------------------
// Conversion: JS value → proto Vertex
// ----------------------------------------------------------------------------

const TWO_POW_63 = 1n << 63n;

function resolveExpiration(ttlSeconds?: number, expiration?: Date): Date | undefined {
  if (expiration !== undefined && ttlSeconds !== undefined) {
    throw new TypeError("specify either ttlSeconds or expiration, not both");
  }
  if (expiration !== undefined) return expiration;
  if (ttlSeconds !== undefined) return new Date(Date.now() + ttlSeconds * 1000);
  return undefined;
}

export function toPbVertex(
  key: string,
  value: VertexInput["value"],
  ttlSeconds?: number,
  expiration?: Date,
): PbVertex {
  const exp = resolveExpiration(ttlSeconds, expiration);
  const base: PbVertex = { key, expiration: exp };

  if (value === null) {
    return { ...base, nil: true };
  }
  if (value instanceof Int32) return { ...base, int32: value.value };
  if (value instanceof Uint32) return { ...base, uint32: value.value };
  if (value instanceof Uint64)
    return { ...base, uint64: Long.fromString(value.value.toString(), true) };
  if (value instanceof Float32) return { ...base, float32: value.value };

  switch (typeof value) {
    case "boolean":
      return { ...base, bool: value };
    case "string":
      return { ...base, string: value };
    case "number":
      if (Number.isInteger(value)) {
        if (value > Number.MAX_SAFE_INTEGER || value < Number.MIN_SAFE_INTEGER) {
          throw new OverflowError(
            `integer ${value} exceeds Number safe range; use BigInt or a narrowing marker`,
          );
        }
        return { ...base, int64: Long.fromNumber(value) };
      }
      return { ...base, float64: value };
    case "bigint": {
      if (value < 0n) {
        if (value < -TWO_POW_63) throw new OverflowError(`bigint ${value} underflows int64`);
        return { ...base, int64: Long.fromString(value.toString()) };
      }
      if (value >= TWO_POW_63) {
        if (value >= 1n << 64n) throw new OverflowError(`bigint ${value} overflows uint64`);
        return { ...base, uint64: Long.fromString(value.toString(), true) };
      }
      return { ...base, int64: Long.fromString(value.toString()) };
    }
  }
  if (value instanceof Date) return { ...base, timestamp: value };
  if (value instanceof Duration) {
    return {
      ...base,
      duration: { seconds: Long.fromString(value.seconds.toString()), nanos: value.nanos },
    };
  }
  if (value instanceof Uint8Array) return { ...base, bytes: Buffer.from(value) };

  throw new TypeError(
    `unsupported Vertex value type: ${typeof value}; supported: number, bigint, boolean, string, Uint8Array, Date, Duration, null, or Int32/Uint32/Uint64/Float32`,
  );
}

// ----------------------------------------------------------------------------
// Conversion: proto Vertex → SDK Vertex
// ----------------------------------------------------------------------------

const VARIANT_KEYS = [
  "float32",
  "float64",
  "int32",
  "int64",
  "uint32",
  "uint64",
  "bool",
  "string",
  "bytes",
  "timestamp",
  "duration",
  "nil",
] as const;

function longToNumber(v: Long | undefined): number {
  if (v === undefined) return 0;
  // Fall back to bigint via string if outside safe integer range.
  if (v.greaterThan(Long.MAX_VALUE) || v.lessThan(Long.MIN_VALUE)) return Number(v.toString());
  return v.toNumber();
}

function longToBigInt(v: Long | undefined): bigint {
  if (v === undefined) return 0n;
  return BigInt(v.toString());
}

export function fromPbVertex(pv: PbVertex): Vertex {
  let kind: VertexKind = "unset";
  let value: VertexValue = null;
  const bag = pv as unknown as Record<string, unknown>;
  for (const k of VARIANT_KEYS) {
    if (bag[k] !== undefined && bag[k] !== null) {
      kind = k;
      break;
    }
  }
  switch (kind) {
    case "unset":
    case "nil":
      value = null;
      break;
    case "float32":
      value = pv.float32 ?? 0;
      break;
    case "float64":
      value = pv.float64 ?? 0;
      break;
    case "int32":
      value = pv.int32 ?? 0;
      break;
    case "int64": {
      const n = longToNumber(pv.int64);
      // Promote to bigint if loss-of-precision was possible.
      value = Number.isSafeInteger(n) ? n : longToBigInt(pv.int64);
      break;
    }
    case "uint32":
      value = pv.uint32 ?? 0;
      break;
    case "uint64": {
      const n = longToNumber(pv.uint64);
      value = Number.isSafeInteger(n) ? n : longToBigInt(pv.uint64);
      break;
    }
    case "bool":
      value = pv.bool ?? false;
      break;
    case "string":
      value = pv.string ?? "";
      break;
    case "bytes":
      value = pv.bytes ? new Uint8Array(pv.bytes) : new Uint8Array();
      break;
    case "timestamp":
      value = pv.timestamp ?? new Date(0);
      break;
    case "duration": {
      const d = pv.duration as PbDuration | undefined;
      value = d ? new Duration(BigInt(d.seconds.toString()), d.nanos) : new Duration(0n, 0);
      break;
    }
  }
  const expiration =
    pv.expiration instanceof Date && pv.expiration.getTime() !== 0 ? pv.expiration : null;
  return { key: pv.key, value, kind, expiration };
}

export function fromPbEdge(pe: PbEdge): Edge {
  const expiration =
    pe.expiration instanceof Date && pe.expiration.getTime() !== 0 ? pe.expiration : null;
  return { tail: pe.tail, head: pe.head, weight: pe.weight, expiration };
}

/** @internal */
export function vertexInputToPb(vi: VertexInput): PbVertex {
  return toPbVertex(vi.key, vi.value, vi.ttlSeconds, vi.expiration);
}

/** @internal */
export function edgeInputToPb(ei: EdgeInput): PbEdge {
  const exp = resolveExpiration(ei.ttlSeconds, ei.expiration);
  return { tail: ei.tail, head: ei.head, weight: ei.weight, expiration: exp };
}
