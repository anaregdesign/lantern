import {
  FailedPreconditionError,
  InvalidArgumentError,
  LanternError,
  NotFoundError,
  ResourceExhaustedError,
  SearchErrorReason,
  SearchContinuationLimitedError,
  SearchCursorStaleError,
} from "lantern-sdk/web";

/**
 * Adapter that translates the SDK's typed error hierarchy
 * (`NotFoundError`, `InvalidArgumentError`, `ResourceExhaustedError`,
 * `LanternError` for the catch-all) into the `LanternApiError` shape
 * the admin usecase layer has historically discriminated on
 * (`error.code`, `error.grpcMessage`). Existing call sites match on
 * those two fields, so this shim keeps those branches working after
 * the migration to `lantern-sdk/web` (#409).
 *
 * Non-Lantern errors fall through unchanged so cancellation
 * (`AbortError`) and network failures retain their native shape.
 */
export class LanternApiError extends Error {
  readonly code: string;
  readonly rpc: string;
  /**
   * The raw server-supplied message before any wrapping. Mirrors the
   * legacy `grpcMessage` field the OpenAPI-derived error carried, so
   * existing usecase error-display code (which falls back to
   * `err.grpcMessage ?? err.message`) keeps working unchanged.
   */
  readonly grpcMessage: string;
  readonly searchReason: SearchErrorReason;

  private constructor(
    rpc: string,
    code: string,
    message: string,
    searchReason = SearchErrorReason.SEARCH_ERROR_REASON_UNSPECIFIED,
  ) {
    super(`${rpc} failed: ${message}`);
    this.name = "LanternApiError";
    this.rpc = rpc;
    this.code = code;
    this.grpcMessage = message;
    this.searchReason = searchReason;
  }

  static fromUnknown(rpc: string, err: unknown): Error {
    if (err instanceof LanternApiError) {
      return err;
    }
    if (err instanceof NotFoundError) {
      return new LanternApiError(rpc, "not_found", err.message);
    }
    if (err instanceof InvalidArgumentError) {
      return new LanternApiError(
        rpc,
        "invalid_argument",
        err.message,
        err.reason,
      );
    }
    if (err instanceof ResourceExhaustedError) {
      return new LanternApiError(
        rpc,
        "resource_exhausted",
        err.message,
        err.reason,
      );
    }
    if (err instanceof FailedPreconditionError) {
      return new LanternApiError(
        rpc,
        "failed_precondition",
        err.message,
        err.reason,
      );
    }
    if (err instanceof SearchCursorStaleError) {
      return new LanternApiError(rpc, "aborted", err.message, err.reason);
    }
    if (err instanceof SearchContinuationLimitedError) {
      return new LanternApiError(
        rpc,
        "resource_exhausted",
        err.message,
        err.reason,
      );
    }
    if (err instanceof LanternError) {
      return new LanternApiError(rpc, "unknown", err.message);
    }
    return err instanceof Error ? err : new Error(String(err));
  }

  /**
   * Returns true when the underlying call failed because the resource
   * does not exist. Used by `getVertex` / `getEdge` to translate the
   * SDK's `NotFoundError` into a clean `null` return rather than a
   * thrown error.
   */
  static isNotFound(err: unknown): boolean {
    return err instanceof NotFoundError;
  }

  /**
   * Returns true when the underlying call failed because the server has
   * the feature disabled (FAILED_PRECONDITION). Content search
   * (`SearchVertices`) raises this when the keyword index is turned off
   * (opt-out via `LANTERN_SEARCH_ENABLED=false`); the search usecase
   * uses it to render a calm "not enabled" state instead of an error
   * toast (#627).
   */
  static isDisabled(err: unknown): boolean {
    return err instanceof FailedPreconditionError
      ? err.reason === SearchErrorReason.SEARCH_DISABLED
      : err instanceof LanternApiError &&
          err.searchReason === SearchErrorReason.SEARCH_DISABLED;
  }

  /**
   * Synthetic factory for callers that want to surface a NotFound
   * outcome that they discovered locally (e.g. an adapter that
   * already swallowed `NotFoundError` and returned `null`, but a
   * higher-level surface — the admin `/cli` panel per #430 — needs
   * the user-visible "[not_found] ..." error chip rather than a
   * misleading `OK`).
   */
  static notFound(rpc: string, message: string): LanternApiError {
    return new LanternApiError(rpc, "not_found", message);
  }
}
