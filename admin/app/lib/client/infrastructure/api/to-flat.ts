/**
 * Translate SDK rich-shape values into and out of the flat-JSON shape
 * the admin SPA's UI layer consumes. The flat shape mirrors the
 * protobuf JSON spec (oneof fields appear flat on the message:
 * `{ key, string }` / `{ key, int64 }` / ...) and is the shape that
 * value-codec.ts + Sigma render + the browse table all expect.
 * Keeping it intact lets the migration land without touching any UI
 * code (#409).
 */

import {
  Duration,
  Float32,
  Int32,
  Uint32,
  Uint64,
  type Edge as SdkEdge,
  type EdgeInput as SdkEdgeInput,
  type Vertex as SdkVertex,
  type VertexInput as SdkVertexInput,
} from "lantern-sdk/web";

import type { Edge, Vertex } from "./types";

/**
 * Convert one SDK rich Vertex into admin's flat-JSON Vertex. The
 * `kind` field on the SDK side discriminates the proto oneof; we
 * project it onto the corresponding flat-JSON field using the same
 * encoding rules the protobuf JSON spec uses (ISO-8601 for
 * Timestamp, Go-duration string for Duration, base64 for bytes,
 * stringified for int64/uint64).
 */
export function sdkVertexToFlat(v: SdkVertex): Vertex {
  const out: Vertex = { key: v.key };
  if (v.expiration) {
    out.expiration = v.expiration.toISOString();
  }
  switch (v.kind) {
    case "unset":
      break;
    case "nil":
      out.nil = true;
      break;
    case "string":
      out.string = v.value as string;
      break;
    case "bool":
      out.bool = v.value as boolean;
      break;
    case "bytes":
      out.bytes = bytesToBase64(v.value as Uint8Array);
      break;
    case "timestamp":
      out.timestamp = (v.value as Date).toISOString();
      break;
    case "duration":
      out.duration = (v.value as Duration).toString();
      break;
    case "float32":
      out.float32 = v.value as number;
      break;
    case "float64":
      out.float64 = v.value as number;
      break;
    case "int32":
      out.int32 = v.value as number;
      break;
    case "int64":
      out.int64 = String(v.value);
      break;
    case "uint32":
      out.uint32 = v.value as number;
      break;
    case "uint64":
      out.uint64 = String(v.value);
      break;
  }
  return out;
}

/**
 * Convert one SDK rich Edge into admin's flat-JSON Edge.
 */
export function sdkEdgeToFlat(e: SdkEdge): Edge {
  const out: Edge = {
    tail: e.tail,
    head: e.head,
    weight: e.weight,
  };
  if (e.expiration) {
    out.expiration = e.expiration.toISOString();
  }
  return out;
}

/**
 * Translate admin's flat-JSON Vertex into the SDK's `VertexInput`
 * shape. The flat input carries at most one populated oneof field;
 * this helper picks the right SDK typed wrapper for each one so the
 * wire round-trip is loss-free for fixed-width integers (int32 /
 * uint32 / uint64 / float32) which the SDK otherwise would infer
 * from the JS value.
 *
 * No oneof field set (or `nil: true`) maps to `null` — the proto nil
 * tombstone — so a key-only PutVertex still upserts a marker row.
 */
export function flatVertexToSdkInput(v: Vertex): SdkVertexInput {
  if (!v.key) {
    throw new Error("flatVertexToSdkInput: vertex.key is required");
  }
  const input: SdkVertexInput = {
    key: v.key,
    value: flatVertexValue(v),
  };
  if (v.expiration) {
    input.expiration = new Date(v.expiration);
  }
  return input;
}

function flatVertexValue(v: Vertex): SdkVertexInput["value"] {
  if (v.string !== undefined) return v.string;
  if (v.bool !== undefined) return v.bool;
  if (v.bytes !== undefined) return base64ToBytes(v.bytes);
  if (v.timestamp !== undefined) return new Date(v.timestamp);
  if (v.duration !== undefined) return parseGoDuration(v.duration);
  if (v.float32 !== undefined) return new Float32(v.float32);
  if (v.float64 !== undefined) return v.float64;
  if (v.int32 !== undefined) return new Int32(v.int32);
  if (v.int64 !== undefined) return BigInt(v.int64);
  if (v.uint32 !== undefined) return new Uint32(v.uint32);
  if (v.uint64 !== undefined) return new Uint64(BigInt(v.uint64));
  return null;
}

/**
 * Translate admin's flat-JSON Edge into the SDK's `EdgeInput`.
 */
export function flatEdgeToSdkInput(e: Edge): SdkEdgeInput {
  if (!e.tail || !e.head) {
    throw new Error("flatEdgeToSdkInput: tail and head are required");
  }
  const input: SdkEdgeInput = {
    tail: e.tail,
    head: e.head,
    weight: e.weight ?? 0,
  };
  if (e.expiration) {
    input.expiration = new Date(e.expiration);
  }
  return input;
}

// ----------------------------------------------------------------------------
// Internals
// ----------------------------------------------------------------------------

function bytesToBase64(bytes: Uint8Array): string {
  let s = "";
  for (let i = 0; i < bytes.length; i++) {
    s += String.fromCharCode(bytes[i]);
  }
  return btoa(s);
}

function base64ToBytes(b64: string): Uint8Array {
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) {
    out[i] = bin.charCodeAt(i);
  }
  return out;
}

/**
 * Decode a Go-style duration string ("1.5s", "2m30s", "750ms",
 * "-3h") into the SDK's `Duration` carrier. The flat-JSON
 * `duration: "1.5s"` field round-trips verbatim via this helper.
 */
function parseGoDuration(s: string): Duration {
  const m = /^(-?)(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)$/.exec(s);
  if (m) {
    const sign = m[1] === "-" ? -1 : 1;
    const n = parseFloat(m[2]);
    const unit = m[3];
    return nsToDuration(unitToNs(unit) * n * sign);
  }
  return parseGoDurationSum(s);
}

function parseGoDurationSum(s: string): Duration {
  let total = 0;
  let sign = 1;
  let rest = s;
  if (rest.startsWith("-")) {
    sign = -1;
    rest = rest.slice(1);
  }
  const re = /(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)/g;
  for (let match = re.exec(rest); match; match = re.exec(rest)) {
    total += unitToNs(match[2]) * parseFloat(match[1]);
  }
  return nsToDuration(total * sign);
}

function unitToNs(unit: string): number {
  switch (unit) {
    case "ns":
      return 1;
    case "us":
    case "µs":
      return 1_000;
    case "ms":
      return 1_000_000;
    case "s":
      return 1_000_000_000;
    case "m":
      return 60 * 1_000_000_000;
    case "h":
      return 3600 * 1_000_000_000;
  }
  return 0;
}

function nsToDuration(ns: number): Duration {
  const sec = Math.trunc(ns / 1_000_000_000);
  const rem = Math.trunc(ns - sec * 1_000_000_000);
  return new Duration(BigInt(sec), rem);
}
