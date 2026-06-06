import { Code, ConnectError } from "@connectrpc/connect";

/**
 * Translates a thrown Connect error into the LanternApiError shape the
 * usecase layer already discriminates on. Existing call sites match on
 * `error.code` (the HTTP-derived string equivalent of the gRPC code)
 * and `error.message`, so this shim keeps those branches working
 * after the transport switch.
 *
 * Non-Connect errors fall through unchanged so cancellation
 * (AbortError) and network failures retain their native shape.
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

  private constructor(rpc: string, code: string, message: string) {
    super(`${rpc} failed: ${message}`);
    this.name = "LanternApiError";
    this.rpc = rpc;
    this.code = code;
    this.grpcMessage = message;
  }

  static fromUnknown(rpc: string, err: unknown): Error {
    if (err instanceof LanternApiError) {
      return err;
    }
    if (err instanceof ConnectError) {
      return new LanternApiError(rpc, codeLabel(err.code), err.rawMessage);
    }
    return err instanceof Error ? err : new Error(String(err));
  }

  /**
   * Returns true when the underlying call failed because the resource
   * does not exist. Used by getVertex / getEdge to translate the
   * Connect CodeNotFound into a clean `null` return rather than a
   * thrown error.
   */
  static isNotFound(err: unknown): boolean {
    return err instanceof ConnectError && err.code === Code.NotFound;
  }
}

// Lower-case dotted label mirroring the gRPC code names the legacy
// LanternApiError carried (e.g. NOT_FOUND → "not_found"). Matches what
// downstream consumers display in the error toast.
function codeLabel(c: Code): string {
  return Code[c]
    .replace(/([A-Z])/g, "_$1")
    .toLowerCase()
    .replace(/^_/, "");
}
