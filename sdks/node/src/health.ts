/**
 * Server readiness probe for the Node SDK (parity with the Go SDK's
 * `health.go`). Lantern mounts the gRPC-Health-v1 surface
 * (connectrpc grpchealth) on the **same** listener as `LanternService`, but
 * that service is not part of the typed Connect client. `pingHealth` therefore
 * POSTs a Connect+JSON `Health/Check` with a raw `fetch` against the same base
 * URL the client was built with — the server speaks HTTP/1.1 and the health
 * surface is auth-exempt, so this works from Node and the browser alike.
 */

import { LanternError } from "./errors.js";

/**
 * URL path the connectrpc grpchealth handler exposes. Fixed by the
 * gRPC-Health-v1 spec — never override.
 */
export const HEALTH_CHECK_PROCEDURE = "/grpc.health.v1.Health/Check";

/**
 * Whether a `HealthCheckResponse.status` string denotes SERVING. The
 * connectrpc grpchealth descriptor prefixes its enum constants
 * (`SERVING_STATUS_SERVING`); other gRPC-Health-v1 servers use the bare
 * `SERVING`. Accept both, matching the Go SDK's `servingStatusOK`.
 */
export function servingStatusOk(status: string): boolean {
  return status === "SERVING" || status === "SERVING_STATUS_SERVING";
}

/**
 * Raised by {@link pingHealth} / {@link import("./client.js").Lantern.ping}
 * when the server replies with a non-SERVING health status. The symbolic
 * status name (e.g. `NOT_SERVING`, `SERVING_STATUS_NOT_SERVING`) is preserved
 * on `.status` for logging and branching.
 */
export class HealthStatusError extends LanternError {
  readonly status: string;
  constructor(status: string) {
    super(`lantern: server health status = ${status || "(empty)"}`);
    this.name = "HealthStatusError";
    this.status = status;
  }
}

/** Knobs for {@link pingHealth}. */
export interface PingOptions {
  /** Caller abort signal; aborting rejects the ping. */
  signal?: AbortSignal;
  /** Deadline in milliseconds, combined with `signal` via `AbortSignal.any`. */
  timeoutMs?: number;
  /** Override the global `fetch` (test injection / custom pipeline). */
  fetchImpl?: typeof fetch;
}

/**
 * POST a Connect+JSON `Health/Check` to `baseUrl` and resolve iff the server
 * reports SERVING. Throws {@link HealthStatusError} on any other status and a
 * generic {@link LanternError} on transport, non-200, or JSON-decode failure.
 * `baseUrl` must be the normalised origin (`http(s)://host:port`, no trailing
 * slash) the client was built with.
 */
export async function pingHealth(baseUrl: string, opts: PingOptions = {}): Promise<void> {
  const fetchImpl = opts.fetchImpl ?? fetch;
  const signal = combineSignals(opts.signal, opts.timeoutMs);

  let resp: Response;
  try {
    resp = await fetchImpl(baseUrl + HEALTH_CHECK_PROCEDURE, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "Connect-Protocol-Version": "1",
      },
      body: "{}",
      ...(signal ? { signal } : {}),
    });
  } catch (err) {
    throw new LanternError(`lantern: ping request failed: ${stringify(err)}`, { cause: err });
  }

  if (!resp.ok) {
    throw new LanternError(`lantern: ping returned HTTP ${resp.status}`);
  }

  let body: { status?: string };
  try {
    body = (await resp.json()) as { status?: string };
  } catch (err) {
    throw new LanternError(`lantern: ping decode failed: ${stringify(err)}`, { cause: err });
  }

  const status = body.status ?? "";
  if (!servingStatusOk(status)) {
    throw new HealthStatusError(status);
  }
}

/**
 * Fold a caller signal and a timeout into a single `AbortSignal` (or
 * undefined when neither is set). Uses `AbortSignal.timeout` /
 * `AbortSignal.any`, available on Node 20+, Bun, and modern browsers.
 */
function combineSignals(signal?: AbortSignal, timeoutMs?: number): AbortSignal | undefined {
  const signals: AbortSignal[] = [];
  if (signal) signals.push(signal);
  if (timeoutMs && timeoutMs > 0) signals.push(AbortSignal.timeout(timeoutMs));
  if (signals.length === 0) return undefined;
  if (signals.length === 1) return signals[0];
  return AbortSignal.any(signals);
}

function stringify(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}
