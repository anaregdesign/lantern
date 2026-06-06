/**
 * Value-type bridge between JS natives and the Connect protobuf JSON
 * shape of the Lantern Vertex / Edge messages.
 *
 * Native ↔ proto mapping (mirrors the Go SDK):
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
 * To pin a narrower wire type, wrap with `Int32`, `Uint32`, `Uint64`, or
 * `Float32`. Each marker range-checks at construction and throws
 * OverflowError on out-of-range input.
 *
 * Wire format: this module produces and consumes the protobuf JSON
 * representation (oneof fields appear flat on the message: `{ key,
 * string }` / `{ key, int64 }` / ...). The Connect client feeds the
 * output of `toVertexJson` through `fromJson(VertexSchema, ...)` and
 * decodes responses with `toJson(VertexSchema, msg)` before handing
 * them to `fromVertexJson`.
 */

import { OverflowError } from "./errors.js";

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
 * pin the `duration` proto oneof variant. Read-side returns the same
 * Duration via Vertex.value when kind === "duration".
 *
 * The on-the-wire shape is a Go-duration string ("1.5s", "2m30s"), which
 * `toString()` produces and `parseGoDuration` consumes.
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
  /**
   * Render as a Go-style duration string (e.g. "1.5s", "750ms"). The
   * Connect/JSON codec accepts this form for the google.protobuf.Duration
   * mapping.
   */
  toString(): string {
    const totalNs = this.seconds * 1_000_000_000n + BigInt(this.nanos);
    if (totalNs === 0n) return "0s";
    const sign = totalNs < 0n ? "-" : "";
    const absNs = totalNs < 0n ? -totalNs : totalNs;
    const secs = absNs / 1_000_000_000n;
    const remNs = absNs % 1_000_000_000n;
    if (remNs === 0n) return `${sign}${secs}s`;
    // Trim trailing zeros from the fractional second component.
    const frac = remNs.toString().padStart(9, "0").replace(/0+$/, "");
    return `${sign}${secs}.${frac}s`;
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
// JSON bridge: SDK ↔ protobuf JSON
// ----------------------------------------------------------------------------

const TWO_POW_63 = 1n << 63n;

/**
 * Build the protobuf JSON shape for a VertexInput so it can feed
 * `fromJson(VertexSchema, ...)`. The oneof field name lives flat on
 * the JSON object per the proto3 JSON mapping spec (so `value: "hello"`
 * becomes `string: "hello"`), which is the exact representation the
 * Connect+JSON codec emits and accepts.
 */
export function toVertexJson(input: VertexInput): Record<string, unknown> {
  const out: Record<string, unknown> = { key: input.key };
  const exp = resolveExpiration(input.ttlSeconds, input.expiration);
  if (exp) out.expiration = exp.toISOString();
  encodeValue(out, input.value);
  return out;
}

/**
 * Build the protobuf JSON shape for an EdgeInput.
 */
export function toEdgeJson(input: EdgeInput): Record<string, unknown> {
  const out: Record<string, unknown> = {
    tail: input.tail,
    head: input.head,
    weight: input.weight,
  };
  const exp = resolveExpiration(input.ttlSeconds, input.expiration);
  if (exp) out.expiration = exp.toISOString();
  return out;
}

/**
 * Decode a Vertex from its protobuf JSON form. The input matches what
 * `toJson(VertexSchema, msg)` produces (one of the oneof fields appears
 * flat on the object: `{ key, string }` / `{ key, int64 }` / ...).
 */
export function fromVertexJson(json: Record<string, unknown>): Vertex {
  const key = String(json.key ?? "");
  const expiration = parseExpiration(json.expiration);
  const { value, kind } = decodeValue(json);
  return { key, value, kind, expiration };
}

export function fromEdgeJson(json: Record<string, unknown>): Edge {
  return {
    tail: String(json.tail ?? ""),
    head: String(json.head ?? ""),
    weight: Number(json.weight ?? 0),
    expiration: parseExpiration(json.expiration),
  };
}

// ----------------------------------------------------------------------------
// Internals
// ----------------------------------------------------------------------------

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
  if (value instanceof Int32) {
    out.int32 = value.value;
    return;
  }
  if (value instanceof Uint32) {
    out.uint32 = value.value;
    return;
  }
  if (value instanceof Uint64) {
    out.uint64 = value.value.toString();
    return;
  }
  if (value instanceof Float32) {
    out.float32 = value.value;
    return;
  }
  if (typeof value === "number") {
    if (Number.isInteger(value)) {
      if (value > Number.MAX_SAFE_INTEGER || value < Number.MIN_SAFE_INTEGER) {
        throw new OverflowError(
          `integer ${value} exceeds Number safe range; use BigInt or a narrowing marker`,
        );
      }
      out.int64 = String(value);
    } else {
      out.float64 = value;
    }
    return;
  }
  if (typeof value === "bigint") {
    if (value < 0n) {
      if (value < -TWO_POW_63) {
        throw new OverflowError(`bigint ${value} underflows int64`);
      }
      out.int64 = value.toString();
      return;
    }
    if (value >= TWO_POW_63) {
      if (value >= 1n << 64n) {
        throw new OverflowError(`bigint ${value} overflows uint64`);
      }
      out.uint64 = value.toString();
      return;
    }
    out.int64 = value.toString();
    return;
  }
  throw new TypeError(
    `unsupported Vertex value type: ${typeof value}; supported: number, bigint, boolean, string, Uint8Array, Date, Duration, null, or Int32/Uint32/Uint64/Float32`,
  );
}

function decodeValue(json: Record<string, unknown>): { value: VertexValue; kind: VertexKind } {
  if (json.float64 !== undefined) return { value: Number(json.float64), kind: "float64" };
  if (json.float32 !== undefined) return { value: Number(json.float32), kind: "float32" };
  if (json.int32 !== undefined) return { value: Number(json.int32), kind: "int32" };
  if (json.int64 !== undefined) {
    const raw = String(json.int64);
    const bi = BigInt(raw);
    // Promote to plain number if it fits safely — keeps consumer code
    // that round-trips small ints idiomatic.
    const n = Number(bi);
    return {
      value: Number.isSafeInteger(n) && BigInt(n) === bi ? n : bi,
      kind: "int64",
    };
  }
  if (json.uint32 !== undefined) return { value: Number(json.uint32), kind: "uint32" };
  if (json.uint64 !== undefined) {
    const bi = BigInt(String(json.uint64));
    const n = Number(bi);
    return {
      value: Number.isSafeInteger(n) && BigInt(n) === bi ? n : bi,
      kind: "uint64",
    };
  }
  if (json.bool !== undefined) return { value: Boolean(json.bool), kind: "bool" };
  if (json.string !== undefined) return { value: String(json.string), kind: "string" };
  if (json.bytes !== undefined) {
    return { value: base64ToBytes(String(json.bytes)), kind: "bytes" };
  }
  if (json.timestamp !== undefined) {
    return { value: new Date(String(json.timestamp)), kind: "timestamp" };
  }
  if (json.duration !== undefined) {
    return { value: parseGoDuration(String(json.duration)), kind: "duration" };
  }
  if (json.nil !== undefined) return { value: null, kind: "nil" };
  return { value: null, kind: "unset" };
}

function parseExpiration(raw: unknown): Date | null {
  if (typeof raw !== "string" || raw === "") return null;
  const ms = Date.parse(raw);
  if (!Number.isFinite(ms) || ms === 0) return null;
  return new Date(ms);
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
 * Minimal Go-duration parser ("10s", "1m30s", "1.5h", "2.5ms") returning
 * a Duration. Used only by the decode path; the encode path goes
 * through `Duration.toString()`.
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
