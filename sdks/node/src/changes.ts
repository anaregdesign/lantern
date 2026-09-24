/** Value-free, deployment-scoped change frames from identity-only Subscribe. */

import { toBinary } from "@bufbuild/protobuf";

import { InvalidArgumentError, LanternError } from "./errors.js";
import {
  IdentityOperation as PbIdentityOperation,
  SubscribeResponseSchema,
  type SubscribeResponse,
} from "./gen/graph/v1/replication_pb.js";

const MAX_UINT64 = (1n << 64n) - 1n;
const MAX_UINT32 = 0xffff_ffff;
const MAX_FRAME_BYTES = 1 << 20;
const MAX_CHUNK_ITEMS = 1024;
const ORIGIN_PATTERN = /^(?!0{32}$)[0-9a-f]{32}$/;

function validOrigin(origin: string): boolean {
  return ORIGIN_PATTERN.test(origin);
}

function originFromBytes(bytes: Uint8Array): string {
  if (bytes.length !== 16 || bytes.every((byte) => byte === 0)) {
    throw new LanternError("identity frame has invalid origin");
  }
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
}

function checkedCursorEntries(
  sequences: Readonly<Record<string, bigint>>,
  kind: "next" | "last",
): [string, bigint][] {
  return Object.entries(sequences).map(([origin, sequence]) => {
    if (!validOrigin(origin) || typeof sequence !== "bigint") {
      throw new InvalidArgumentError(`invalid identity ${kind} cursor entry`);
    }
    if (sequence < (kind === "next" ? 1n : 0n) || sequence > MAX_UINT64) {
      throw new InvalidArgumentError(`identity ${kind} cursor sequence is outside uint64`);
    }
    return [origin, sequence];
  });
}

/** Portable per-origin cursor. Values are the NEXT expected sequence. */
export class IdentityNextCursor {
  readonly nextSequences: Readonly<Record<string, bigint>>;

  constructor(nextSequences: Readonly<Record<string, bigint>>) {
    this.nextSequences = Object.freeze(
      Object.fromEntries(checkedCursorEntries(nextSequences, "next")),
    );
    Object.freeze(this);
  }

  /** Convert a durable LAST-applied vector, rejecting uint64 exhaustion. */
  static fromLastApplied(lastSequences: Readonly<Record<string, bigint>>): IdentityNextCursor {
    const entries = checkedCursorEntries(lastSequences, "last").map(([origin, last]) => {
      if (last === MAX_UINT64) {
        throw new InvalidArgumentError("last-applied identity sequence cannot advance");
      }
      return [origin, last + 1n] as const;
    });
    return new IdentityNextCursor(Object.fromEntries(entries));
  }
}

/** One Edge identity; weights and contribution IDs are intentionally absent. */
export interface IdentityEdgeKey {
  readonly tail: string;
  readonly head: string;
}

/** Operation category of the original committed mutation. */
export type IdentityOperation = "putVertex" | "deleteVertex" | "addEdge" | "putEdge" | "deleteEdge";

/** Nanosecond-precision causal coordinate of the original mutation. */
export interface IdentityHlc {
  readonly wallNanoseconds: bigint;
  readonly logical: number;
  readonly nodeId: string;
}

/** Atomic bootstrap cutoff. Values are LAST committed sequences. */
export interface IdentityCheckpointFrame {
  readonly kind: "checkpoint";
  readonly lastSequences: Readonly<Record<string, bigint>>;
}

/** One bounded fragment of exact graph identities from a mutation. */
export interface IdentityChunkFrame {
  readonly kind: "chunk";
  readonly origin: string;
  readonly sequence: bigint;
  readonly hlc: IdentityHlc;
  readonly operation: IdentityOperation;
  readonly chunkIndex: number;
  readonly firstItemIndex: number;
  /** Advance a durable cursor only after this final chunk is applied. */
  readonly isLast: boolean;
  readonly vertexKeys: readonly string[];
  readonly edgeKeys: readonly IdentityEdgeKey[];
}

export type IdentityFrame = IdentityCheckpointFrame | IdentityChunkFrame;

/** Options for one long-lived identity stream. */
export interface IdentitySubscribeOptions {
  /** An atomic checkpoint followed by live chunks; requires an empty cursor. */
  readonly bootstrap?: boolean;
  /** Per-origin NEXT expected sequences for resume. */
  readonly cursor?: IdentityNextCursor;
  /** Explicit stream deadline. The client's default unary timeout is ignored. */
  readonly timeoutMs?: number;
}

function operationFromWire(operation: PbIdentityOperation): IdentityOperation {
  switch (operation) {
    case PbIdentityOperation.PUT_VERTEX:
      return "putVertex";
    case PbIdentityOperation.DELETE_VERTEX:
      return "deleteVertex";
    case PbIdentityOperation.ADD_EDGE:
      return "addEdge";
    case PbIdentityOperation.PUT_EDGE:
      return "putEdge";
    case PbIdentityOperation.DELETE_EDGE:
      return "deleteEdge";
    default:
      throw new LanternError("identity chunk has unknown operation");
  }
}

/** Decode one value-free frame. Exported from this internal module for tests. */
export function decodeIdentityFrame(raw: SubscribeResponse): IdentityFrame {
  let frameBytes: number;
  try {
    frameBytes = toBinary(SubscribeResponseSchema, raw).length;
  } catch (cause) {
    throw new LanternError("invalid identity frame encoding", { cause });
  }
  if (frameBytes > MAX_FRAME_BYTES) {
    throw new LanternError("identity frame exceeds size limit");
  }
  switch (raw.event.case) {
    case "checkpoint": {
      let lastSequences: Record<string, bigint>;
      try {
        lastSequences = Object.fromEntries(
          checkedCursorEntries(raw.event.value.lastSeqPerOrigin, "last"),
        );
      } catch (cause) {
        throw new LanternError("identity checkpoint has invalid cursor", { cause });
      }
      return Object.freeze({ kind: "checkpoint", lastSequences: Object.freeze(lastSequences) });
    }
    case "identityChunk": {
      const chunk = raw.event.value;
      const origin = originFromBytes(chunk.origin);
      const hlc = chunk.hlc;
      if (typeof chunk.seq !== "bigint" || chunk.seq < 1n || chunk.seq > MAX_UINT64 || !hlc) {
        throw new LanternError("identity chunk is missing sequence or HLC");
      }
      const hlcOrigin = originFromBytes(hlc.nodeId);
      if (hlcOrigin !== origin) {
        throw new LanternError("identity chunk HLC origin mismatch");
      }
      const count = chunk.vertexKeys.length + chunk.edgeKeys.length;
      if (count > MAX_CHUNK_ITEMS || (!chunk.isLast && count === 0)) {
        throw new LanternError("identity chunk has invalid item count");
      }
      if (
        !Number.isInteger(chunk.chunkIndex) ||
        chunk.chunkIndex < 0 ||
        chunk.chunkIndex > MAX_UINT32 ||
        !Number.isInteger(chunk.firstItemIndex) ||
        chunk.firstItemIndex < 0 ||
        chunk.firstItemIndex > MAX_UINT32 ||
        chunk.firstItemIndex + count > MAX_UINT32 ||
        (chunk.chunkIndex === 0 && chunk.firstItemIndex !== 0)
      ) {
        throw new LanternError("identity chunk has invalid item index");
      }
      const operation = operationFromWire(chunk.operation);
      if (
        chunk.vertexKeys.some((key) => key.length === 0) ||
        chunk.edgeKeys.some((key) => key.tail.length === 0 || key.head.length === 0)
      ) {
        throw new LanternError("identity chunk has an empty graph identity");
      }
      if (
        operation === "putVertex" || operation === "deleteVertex"
          ? chunk.edgeKeys.length !== 0
          : chunk.vertexKeys.length !== 0
      ) {
        throw new LanternError("identity chunk mixes operation and key family");
      }
      const vertexKeys = Object.freeze([...chunk.vertexKeys]);
      const edgeKeys = Object.freeze(
        chunk.edgeKeys.map((key) => Object.freeze({ tail: key.tail, head: key.head })),
      );
      return Object.freeze({
        kind: "chunk",
        origin,
        sequence: chunk.seq,
        hlc: Object.freeze({
          wallNanoseconds: hlc.wallNs,
          logical: hlc.logical,
          nodeId: hlcOrigin,
        }),
        operation,
        chunkIndex: chunk.chunkIndex,
        firstItemIndex: chunk.firstItemIndex,
        isLast: chunk.isLast,
        vertexKeys,
        edgeKeys,
      });
    }
    case "mutation":
    case undefined:
      throw new LanternError("unexpected identity stream frame");
  }
}
