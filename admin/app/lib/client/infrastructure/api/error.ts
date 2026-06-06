/**
 * Typed wrapper around non-2xx Lantern HTTP responses. Use
 * `LanternApiError.fromResponse(response, "ScanVertices")` from API adapters
 * so callers can render a consistent error UI without having to inspect the
 * raw `Response` themselves.
 */
export class LanternApiError extends Error {
  public readonly status: number;
  public readonly operation: string;
  public readonly grpcCode?: number;
  public readonly grpcMessage?: string;

  constructor(
    operation: string,
    status: number,
    message: string,
    grpcCode?: number,
    grpcMessage?: string,
  ) {
    super(message);
    this.name = "LanternApiError";
    this.operation = operation;
    this.status = status;
    this.grpcCode = grpcCode;
    this.grpcMessage = grpcMessage;
  }

  /**
   * Build a `LanternApiError` from a non-2xx Lantern response. The body is
   * assumed to be the `rpcStatus` envelope grpc-gateway emits by default,
   * but the helper tolerates plain text and empty bodies.
   */
  static async fromResponse(
    response: Response,
    operation: string,
  ): Promise<LanternApiError> {
    let bodyText = "";
    try {
      bodyText = await response.text();
    } catch {
      // ignore — body may already be drained
    }
    let grpcCode: number | undefined;
    let grpcMessage: string | undefined;
    if (bodyText) {
      try {
        const parsed = JSON.parse(bodyText) as {
          code?: number;
          message?: string;
        };
        grpcCode = parsed.code;
        grpcMessage = parsed.message;
      } catch {
        // not JSON; fall through
      }
    }
    const message =
      grpcMessage ??
      bodyText ??
      `${operation} failed with HTTP ${response.status}`;
    return new LanternApiError(
      operation,
      response.status,
      message,
      grpcCode,
      grpcMessage,
    );
  }
}
