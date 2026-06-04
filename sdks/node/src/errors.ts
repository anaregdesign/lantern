/**
 * Typed error hierarchy raised by the Lantern Node SDK.
 *
 * The SDK maps gRPC status codes to typed JS errors so callers can branch
 * on category without inspecting status messages:
 *
 *   - NotFoundError           ← status.NOT_FOUND
 *   - InvalidArgumentError    ← status.INVALID_ARGUMENT
 *   - ResourceExhaustedError  ← status.RESOURCE_EXHAUSTED
 *
 * All three extend LanternError for catch-all handling and preserve the
 * underlying ServiceError as `cause`.
 *
 * BatchError is thrown by batch helpers (putVertices, addEdges, putEdges,
 * deleteVertices, deleteEdges) on partial-write failure; its `written`
 * field reports how many items from the input were already committed
 * before the failing chunk, so callers can resume with
 * `inputs.slice(err.written)`.
 */

import { status as GrpcStatus, type ServiceError } from "@grpc/grpc-js";

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
 * addEdges — the already-applied prefix would be double-counted.
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

export function wrapRpcError(err: unknown): LanternError {
  if (err instanceof LanternError) return err;
  const se = err as Partial<ServiceError> | null;
  const code = se?.code;
  const details = se?.details ?? (err instanceof Error ? err.message : String(err));
  switch (code) {
    case GrpcStatus.NOT_FOUND:
      return new NotFoundError(details || "not found", { cause: err });
    case GrpcStatus.INVALID_ARGUMENT:
      return new InvalidArgumentError(details || "invalid argument", { cause: err });
    case GrpcStatus.RESOURCE_EXHAUSTED:
      return new ResourceExhaustedError(details || "resource exhausted", { cause: err });
    default:
      return new LanternError(code !== undefined ? `gRPC ${code}: ${details}` : String(details), {
        cause: err,
      });
  }
}
