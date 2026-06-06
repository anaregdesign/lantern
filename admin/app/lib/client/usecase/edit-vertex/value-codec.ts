import type { Vertex } from "~/lib/client/infrastructure/api/get-vertex";
import type { PutVertexBody } from "~/lib/client/infrastructure/api/put-vertex";

/**
 * One label per `v1Vertex` value oneof variant. The order chosen here is
 * also the order shown in the kind selector dropdown. Numeric variants
 * come first so the most common cases land at the top.
 */
export const VERTEX_VALUE_KINDS = [
  "float64",
  "float32",
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
export type VertexValueKind = (typeof VERTEX_VALUE_KINDS)[number];

export type BytesEncoding = "hex" | "base64";

/**
 * Form-state inputs for the editor. Each variant gets a slot so the form
 * preserves what the user typed when they flip the kind selector back and
 * forth. The fields are strings (or simple primitives) so the React inputs
 * remain controlled until the user explicitly saves.
 */
export interface VertexInputs {
  float64: string;
  float32: string;
  int32: string;
  int64: string;
  uint32: string;
  uint64: string;
  bool: boolean;
  string: string;
  bytesEncoding: BytesEncoding;
  bytesInput: string;
  /** Local ISO `YYYY-MM-DDTHH:mm` (no zone) for the datetime-local input. */
  timestamp: string;
  /** Go duration syntax: `5m`, `1h30m`. */
  duration: string;
}

export const INITIAL_VERTEX_INPUTS: VertexInputs = {
  float64: "",
  float32: "",
  int32: "",
  int64: "",
  uint32: "",
  uint64: "",
  bool: false,
  string: "",
  bytesEncoding: "hex",
  bytesInput: "",
  timestamp: "",
  duration: "",
};

/**
 * Picks the kind currently populated on a vertex. Returns `"nil"` when
 * either the `nil` flag is set or no variant is present.
 */
export function kindOfVertex(vertex: Vertex): VertexValueKind {
  if (vertex.float64 !== undefined) return "float64";
  if (vertex.float32 !== undefined) return "float32";
  if (vertex.int32 !== undefined) return "int32";
  if (vertex.int64 !== undefined) return "int64";
  if (vertex.uint32 !== undefined) return "uint32";
  if (vertex.uint64 !== undefined) return "uint64";
  if (vertex.bool !== undefined) return "bool";
  if (vertex.string !== undefined) return "string";
  if (vertex.bytes !== undefined) return "bytes";
  if (vertex.timestamp !== undefined) return "timestamp";
  if (vertex.duration !== undefined) return "duration";
  return "nil";
}

/**
 * Seeds the form inputs from the loaded vertex so the Edit form mirrors
 * what the user is about to overwrite. Unset slots fall back to the
 * initial blank values.
 */
export function inputsFromVertex(vertex: Vertex): VertexInputs {
  return {
    ...INITIAL_VERTEX_INPUTS,
    float64:
      vertex.float64 !== undefined
        ? String(vertex.float64)
        : INITIAL_VERTEX_INPUTS.float64,
    float32:
      vertex.float32 !== undefined
        ? String(vertex.float32)
        : INITIAL_VERTEX_INPUTS.float32,
    int32:
      vertex.int32 !== undefined
        ? String(vertex.int32)
        : INITIAL_VERTEX_INPUTS.int32,
    int64: vertex.int64 ?? INITIAL_VERTEX_INPUTS.int64,
    uint32:
      vertex.uint32 !== undefined
        ? String(vertex.uint32)
        : INITIAL_VERTEX_INPUTS.uint32,
    uint64: vertex.uint64 ?? INITIAL_VERTEX_INPUTS.uint64,
    bool: vertex.bool ?? INITIAL_VERTEX_INPUTS.bool,
    string: vertex.string ?? INITIAL_VERTEX_INPUTS.string,
    bytesEncoding: INITIAL_VERTEX_INPUTS.bytesEncoding,
    bytesInput:
      vertex.bytes !== undefined
        ? `0x${base64ToHex(vertex.bytes)}`
        : INITIAL_VERTEX_INPUTS.bytesInput,
    timestamp:
      vertex.timestamp !== undefined
        ? isoToLocalInput(vertex.timestamp)
        : INITIAL_VERTEX_INPUTS.timestamp,
    duration: vertex.duration ?? INITIAL_VERTEX_INPUTS.duration,
  };
}

/** TTL choice: a relative duration to convert to absolute `expiration`. */
export type TtlMode = "preset5m" | "preset1h" | "preset24h" | "none" | "custom";

export interface TtlInput {
  mode: TtlMode;
  /** Used when `mode === "custom"`. Go duration syntax. */
  custom: string;
}

export const INITIAL_TTL_INPUT: TtlInput = {
  mode: "preset1h",
  custom: "",
};

const PRESET_MS: Record<Exclude<TtlMode, "none" | "custom">, number> = {
  preset5m: 5 * 60_000,
  preset1h: 60 * 60_000,
  preset24h: 24 * 60 * 60_000,
};

const SECOND_NS = 1_000_000_000n;

/**
 * Validates and parses a Go-syntax duration like `5m`, `1h30m`, `750ms`.
 * Supports the same unit set the Go stdlib does (`ns`, `us`/`µs`, `ms`,
 * `s`, `m`, `h`), case-sensitive units, optional leading sign, and
 * fractional magnitudes. Returns the duration in milliseconds.
 */
export function parseGoDuration(input: string): {
  ms: number | null;
  error: string | null;
} {
  const raw = input.trim();
  if (raw === "") {
    return { ms: null, error: "Duration is required." };
  }
  if (raw === "0") {
    return { ms: 0, error: null };
  }
  let i = 0;
  let neg = false;
  if (raw[0] === "+" || raw[0] === "-") {
    neg = raw[0] === "-";
    i++;
  }
  if (i === raw.length) {
    return { ms: null, error: "Invalid duration." };
  }
  let totalNs = 0n;
  const unitMap: Record<string, bigint> = {
    ns: 1n,
    us: 1_000n,
    µs: 1_000n,
    μs: 1_000n,
    ms: 1_000_000n,
    s: SECOND_NS,
    m: 60n * SECOND_NS,
    h: 3600n * SECOND_NS,
  };
  while (i < raw.length) {
    const numStart = i;
    while (i < raw.length && /[0-9.]/.test(raw[i]!)) {
      i++;
    }
    if (i === numStart) {
      return { ms: null, error: "Invalid duration." };
    }
    const numStr = raw.slice(numStart, i);
    if ((numStr.match(/\./g) ?? []).length > 1) {
      return { ms: null, error: "Invalid duration." };
    }
    const num = Number(numStr);
    if (!Number.isFinite(num)) {
      return { ms: null, error: "Invalid duration." };
    }
    const unitStart = i;
    while (i < raw.length && /[a-zA-Zµμ]/.test(raw[i]!)) {
      i++;
    }
    if (i === unitStart) {
      return { ms: null, error: "Missing unit (s, ms, m, h…)." };
    }
    const unit = raw.slice(unitStart, i);
    const unitNs = unitMap[unit];
    if (unitNs === undefined) {
      return { ms: null, error: `Unknown unit "${unit}".` };
    }
    // Scale: floor((num as float * unitNs) so fractional 1.5s works.
    const scaled = BigInt(Math.round(num * Number(unitNs)));
    totalNs += scaled;
  }
  if (neg) {
    totalNs = -totalNs;
  }
  const ms = Number(totalNs / 1_000_000n);
  return { ms, error: null };
}

/** Converts the TTL input into an absolute ISO expiration string. */
export function ttlToExpiration(
  ttl: TtlInput,
  now: number,
): { iso: string | undefined; error: string | null } {
  if (ttl.mode === "none") {
    return { iso: undefined, error: null };
  }
  if (ttl.mode === "custom") {
    const { ms, error } = parseGoDuration(ttl.custom);
    if (error || ms === null) {
      return { iso: undefined, error: error ?? "Invalid duration." };
    }
    if (ms <= 0) {
      return { iso: undefined, error: "TTL must be positive." };
    }
    return { iso: new Date(now + ms).toISOString(), error: null };
  }
  const ms = PRESET_MS[ttl.mode];
  return { iso: new Date(now + ms).toISOString(), error: null };
}

const INT32_MIN = -2_147_483_648;
const INT32_MAX = 2_147_483_647;
const UINT32_MAX = 4_294_967_295;
const INT64_MIN = -(2n ** 63n);
const INT64_MAX = 2n ** 63n - 1n;
const UINT64_MAX = 2n ** 64n - 1n;

export interface BuildBodyResult {
  body: PutVertexBody | null;
  /** Single field-scoped error message (kind selector / TTL / value). */
  error: string | null;
}

/**
 * Validates the typed value inputs for `kind`, encodes the result onto
 * the flat protobuf-JSON body shape the Connect handler accepts, and
 * stamps the absolute expiration. Returns either a ready-to-PUT body or
 * a single human-readable error message.
 */
export function buildPutVertexBody(
  kind: VertexValueKind,
  inputs: VertexInputs,
  ttl: TtlInput,
  now: number = Date.now(),
): BuildBodyResult {
  const { iso, error: ttlErr } = ttlToExpiration(ttl, now);
  if (ttlErr) {
    return { body: null, error: ttlErr };
  }
  const vertex: PutVertexBody["vertex"] = {};
  if (iso !== undefined) {
    vertex.expiration = iso;
  }
  switch (kind) {
    case "float64": {
      const num = Number(inputs.float64);
      if (inputs.float64.trim() === "" || !Number.isFinite(num)) {
        return { body: null, error: "float64 must be a finite number." };
      }
      vertex.float64 = num;
      break;
    }
    case "float32": {
      const num = Number(inputs.float32);
      if (inputs.float32.trim() === "" || !Number.isFinite(num)) {
        return { body: null, error: "float32 must be a finite number." };
      }
      vertex.float32 = num;
      break;
    }
    case "int32": {
      const raw = inputs.int32.trim();
      if (!/^-?\d+$/.test(raw)) {
        return { body: null, error: "int32 must be a whole number." };
      }
      const num = Number(raw);
      if (num < INT32_MIN || num > INT32_MAX) {
        return {
          body: null,
          error: `int32 must be between ${INT32_MIN} and ${INT32_MAX}.`,
        };
      }
      vertex.int32 = num;
      break;
    }
    case "int64": {
      const raw = inputs.int64.trim();
      if (!/^-?\d+$/.test(raw)) {
        return { body: null, error: "int64 must be a whole number." };
      }
      let big: bigint;
      try {
        big = BigInt(raw);
      } catch {
        return { body: null, error: "int64 is not a valid integer." };
      }
      if (big < INT64_MIN || big > INT64_MAX) {
        return { body: null, error: "int64 out of range." };
      }
      vertex.int64 = big.toString();
      break;
    }
    case "uint32": {
      const raw = inputs.uint32.trim();
      if (!/^\d+$/.test(raw)) {
        return {
          body: null,
          error: "uint32 must be a non-negative whole number.",
        };
      }
      const num = Number(raw);
      if (num > UINT32_MAX) {
        return { body: null, error: `uint32 must be at most ${UINT32_MAX}.` };
      }
      vertex.uint32 = num;
      break;
    }
    case "uint64": {
      const raw = inputs.uint64.trim();
      if (!/^\d+$/.test(raw)) {
        return {
          body: null,
          error: "uint64 must be a non-negative whole number.",
        };
      }
      let big: bigint;
      try {
        big = BigInt(raw);
      } catch {
        return { body: null, error: "uint64 is not a valid integer." };
      }
      if (big > UINT64_MAX) {
        return { body: null, error: "uint64 out of range." };
      }
      vertex.uint64 = big.toString();
      break;
    }
    case "bool": {
      vertex.bool = inputs.bool;
      break;
    }
    case "string": {
      vertex.string = inputs.string;
      break;
    }
    case "bytes": {
      const { b64, error } = parseBytesInput(
        inputs.bytesInput,
        inputs.bytesEncoding,
      );
      if (error || b64 === null) {
        return { body: null, error: error ?? "Invalid bytes." };
      }
      vertex.bytes = b64;
      break;
    }
    case "timestamp": {
      const raw = inputs.timestamp.trim();
      if (raw === "") {
        return { body: null, error: "timestamp is required." };
      }
      const ms = Date.parse(raw);
      if (!Number.isFinite(ms)) {
        return { body: null, error: "timestamp is not a valid date." };
      }
      vertex.timestamp = new Date(ms).toISOString();
      break;
    }
    case "duration": {
      const { error } = parseGoDuration(inputs.duration);
      if (error) {
        return { body: null, error };
      }
      vertex.duration = inputs.duration.trim();
      break;
    }
    case "nil": {
      vertex.nil = true;
      break;
    }
  }
  return { body: { vertex }, error: null };
}

interface BytesParseResult {
  b64: string | null;
  error: string | null;
}

/**
 * Accepts the user's bytes input under the active encoding and returns
 * the base64 string the wire expects. Hex inputs may carry a `0x` prefix
 * and may contain whitespace.
 */
export function parseBytesInput(
  raw: string,
  encoding: BytesEncoding,
): BytesParseResult {
  const trimmed = raw.trim();
  if (trimmed === "") {
    return { b64: "", error: null };
  }
  if (encoding === "hex") {
    let hex =
      trimmed.startsWith("0x") || trimmed.startsWith("0X")
        ? trimmed.slice(2)
        : trimmed;
    hex = hex.replace(/\s+/g, "");
    if (hex.length % 2 !== 0) {
      return { b64: null, error: "Hex must have an even number of digits." };
    }
    if (!/^[0-9a-fA-F]*$/.test(hex)) {
      return { b64: null, error: "Hex contains non-hex characters." };
    }
    const bytes = new Uint8Array(hex.length / 2);
    for (let i = 0; i < bytes.length; i++) {
      bytes[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16);
    }
    return { b64: bytesToBase64(bytes), error: null };
  }
  if (!/^[A-Za-z0-9+/=\s]*$/.test(trimmed)) {
    return { b64: null, error: "Invalid base64." };
  }
  const compact = trimmed.replace(/\s+/g, "");
  try {
    // Round-trip to confirm decodability.
    if (typeof atob !== "function") {
      return { b64: compact, error: null };
    }
    atob(compact);
  } catch {
    return { b64: null, error: "Invalid base64." };
  }
  return { b64: compact, error: null };
}

function bytesToBase64(bytes: Uint8Array): string {
  if (typeof btoa !== "function") {
    return "";
  }
  let binary = "";
  for (let i = 0; i < bytes.length; i++) {
    binary += String.fromCharCode(bytes[i]!);
  }
  return btoa(binary);
}

export function base64ToHex(b64: string): string {
  if (typeof atob !== "function") {
    return b64;
  }
  try {
    const binary = atob(b64);
    let hex = "";
    for (let i = 0; i < binary.length; i++) {
      hex += binary.charCodeAt(i).toString(16).padStart(2, "0");
    }
    return hex;
  } catch {
    return b64;
  }
}

/**
 * Converts an ISO `2026-01-02T03:04:05Z` string to the `YYYY-MM-DDTHH:mm`
 * shape the HTML `datetime-local` input expects. Returns the raw value
 * unchanged on parse failure so the user can still see (and fix) it.
 */
export function isoToLocalInput(iso: string): string {
  const ms = Date.parse(iso);
  if (!Number.isFinite(ms)) {
    return iso;
  }
  const d = new Date(ms);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}
