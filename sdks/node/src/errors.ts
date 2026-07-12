/**
 * Typed error hierarchy raised by the Lantern Node SDK.
 *
 * The SDK maps Connect status codes to typed JS errors so callers can branch
 * on category without inspecting status messages:
 *
 *   - NotFoundError           ← Code.NotFound (5)
 *   - InvalidArgumentError    ← Code.InvalidArgument (3)
 *   - ResourceExhaustedError  ← Code.ResourceExhausted (8)
 *   - FailedPreconditionError ← Code.FailedPrecondition (9)
 *
 * All three extend LanternError for catch-all handling and preserve the
 * underlying ConnectError as `cause`.
 *
 * BatchError is thrown by batch helpers (putVertices, addEdges, putEdges,
 * deleteVertices, deleteEdges) on partial-write failure; its `written`
 * field reports how many items from the input were already committed
 * before the failing chunk, so callers can resume with
 * `inputs.slice(err.written)`.
 */

import { ConnectError } from "@connectrpc/connect";
import { SearchErrorDetailSchema, SearchErrorReason } from "./gen/graph/v1/graph_pb.js";

export class LanternError extends Error {
  override readonly cause?: unknown;
  constructor(message: string, options?: { cause?: unknown }) {
    super(message);
    this.name = "LanternError";
    if (options?.cause !== undefined) this.cause = options.cause;
  }
}

export class NotFoundError extends LanternError {
  constructor(message: string, options?: { cause?: unknown }) {
    super(message, options);
    this.name = "NotFoundError";
  }
}

export class InvalidArgumentError extends LanternError {
  constructor(message: string, options?: { cause?: unknown }) {
    super(message, options);
    this.name = "InvalidArgumentError";
  }
}

export class ResourceExhaustedError extends LanternError {
  constructor(message: string, options?: { cause?: unknown }) {
    super(message, options);
    this.name = "ResourceExhaustedError";
  }
}

/**
 * Raised when a precondition for the call is not met — notably when
 * `searchVertices` runs against a server whose content-search index is
 * disabled (`LANTERN_SEARCH_ENABLED=false`). Callers can branch on this
 * type to render a calm "search not enabled" state instead of a hard
 * error. Maps from `Code.FailedPrecondition` (9).
 */
export class FailedPreconditionError extends LanternError {
  readonly reason: SearchErrorReason;
  constructor(message: string, options?: { cause?: unknown; reason?: SearchErrorReason }) {
    super(message, options);
    this.name = "FailedPreconditionError";
    this.reason = options?.reason ?? SearchErrorReason.SEARCH_ERROR_REASON_UNSPECIFIED;
  }
}

export class OverflowError extends LanternError {
  constructor(message: string, options?: { cause?: unknown }) {
    super(message, options);
    this.name = "OverflowError";
  }
}

/**
 * Thrown by batch helpers when a chunk fails after one or more chunks have
 * already been committed. `written` is the number of inputs from the
 * original sequence committed by chunks 0..N-1 before chunk N failed.
 * Resume safely with `inputs.slice(err.written)`.
 *
 * Full retry from index 0 is safe for idempotent operations
 * (putVertices, putEdges, deleteVertices, deleteEdges) but NOT for
 * a plain addEdges — the already-applied prefix would be double-counted.
 * Attaching contrib ids to the edges (an automatic id via
 * `ConnectOptions.idempotentAdds`, or a deterministic `EdgeInput.contribId`)
 * makes both a resumed retry (`inputs.slice(err.written)`) and a full retry
 * from index 0 safe, because the server dedups each contribution while it is
 * live (#895).
 */
export class BatchError extends LanternError {
  readonly written: number;
  constructor(written: number, cause: unknown) {
    super(`batch write failed after ${written} items committed: ${stringify(cause)}`, { cause });
    this.name = "BatchError";
    this.written = written;
  }
}

function stringify(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}

/**
 * Translates a thrown Connect error into the typed LanternError
 * hierarchy. Non-Connect errors fall through unchanged so cancellation
 * (AbortError) and network failures keep their native shape.
 *
 * Code numbers mirror the @connectrpc/connect `Code` enum (which is
 * gRPC-code-compatible by design):
 *   3 = InvalidArgument
 *   5 = NotFound
 *   8 = ResourceExhausted
 *   9 = FailedPrecondition
 */
export function wrapConnectError(err: unknown): LanternError {
  if (err instanceof LanternError) return err;
  // Read the common status fields without changing non-Connect fallthrough.
  const ce = err as { code?: number; rawMessage?: string; message?: string };
  const message = ce.rawMessage ?? ce.message ?? String(err);
  switch (ce.code) {
    case 5:
      return new NotFoundError(message, { cause: err });
    case 3:
      return new InvalidArgumentError(message, { cause: err });
    case 8:
      return new ResourceExhaustedError(message, { cause: err });
    case 9:
      return new FailedPreconditionError(message, {
        cause: err,
        reason:
          ConnectError.from(err).findDetails(SearchErrorDetailSchema)[0]?.reason ??
          SearchErrorReason.SEARCH_ERROR_REASON_UNSPECIFIED,
      });
    default:
      return new LanternError(message, { cause: err });
  }
}
