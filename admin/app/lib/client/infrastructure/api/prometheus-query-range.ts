/**
 * Prometheus HTTP query adapter for the Ops Metrics section.
 *
 * This is a plain `fetch` against Prometheus's `GET /api/v1/query_range`
 * endpoint — it is NOT a Connect RPC, so it deliberately does not use
 * `LanternApiError`. Failures surface as `PrometheusError`, a flat,
 * snapshot-testable error carrying enough context for the UI to render a
 * "configure / unreachable" state. The success shape is flattened into
 * `MetricSeries[]` (one entry per returned time series), mirroring the
 * flattening done by `get-server-status.ts`.
 *
 * The `promBaseUrl` may be a same-origin path (`/api/prom`, the default
 * reverse-proxy shape) or an absolute `http(s)://…` URL; relative paths
 * resolve against the document origin at fetch time.
 */

/** A single sample: `t` is epoch seconds, `v` is the parsed float value. */
export interface MetricPoint {
  t: number;
  v: number;
}

/** One Prometheus time series: its label set plus its samples over the range. */
export interface MetricSeries {
  labels: Record<string, string>;
  points: MetricPoint[];
}

export type PrometheusErrorKind = "network" | "http" | "status" | "parse";

/**
 * Error raised by {@link queryRange}. `kind` distinguishes the failure
 * mode so the UI can phrase the degraded state appropriately:
 *
 * - `network` — the request never completed (DNS, connection refused, TLS).
 * - `http` — a non-2xx response with no usable Prometheus error body.
 * - `status` — Prometheus replied with `status: "error"` and an `error` field.
 * - `parse` — the response was not the expected JSON matrix envelope (e.g.
 *   an SPA `index.html` fallback when no Prometheus is wired up).
 *
 * `AbortError` is never wrapped in this type — {@link queryRange} rethrows
 * the original `DOMException` so callers can detect cancellation via
 * `err.name === "AbortError"`, consistent with `use-ops.ts`.
 */
export class PrometheusError extends Error {
  readonly kind: PrometheusErrorKind;
  readonly httpStatus?: number;

  private constructor(
    message: string,
    kind: PrometheusErrorKind,
    httpStatus?: number,
  ) {
    super(message);
    this.name = "PrometheusError";
    this.kind = kind;
    this.httpStatus = httpStatus;
  }

  static network(detail: string): PrometheusError {
    return new PrometheusError(
      `Could not reach Prometheus: ${detail}`,
      "network",
    );
  }

  static http(status: number, detail?: string): PrometheusError {
    const suffix = detail ? `: ${detail}` : "";
    return new PrometheusError(
      `Prometheus returned HTTP ${status}${suffix}`,
      "http",
      status,
    );
  }

  static status(detail: string): PrometheusError {
    return new PrometheusError(`Prometheus query failed: ${detail}`, "status");
  }

  static parse(detail: string): PrometheusError {
    return new PrometheusError(
      `Unexpected Prometheus response: ${detail}`,
      "parse",
    );
  }
}

export interface QueryRangeParams {
  /** A PromQL expression with all placeholders already resolved. */
  query: string;
  /** Range start, epoch seconds (inclusive). */
  start: number;
  /** Range end, epoch seconds (inclusive). */
  end: number;
  /** Resolution step, seconds. */
  step: number;
  signal?: AbortSignal;
}

interface PromMatrixResult {
  metric?: Record<string, string>;
  values?: Array<[number, string]>;
}

interface PromEnvelope {
  status?: string;
  error?: string;
  errorType?: string;
  data?: {
    resultType?: string;
    result?: PromMatrixResult[];
  };
}

/**
 * Executes a Prometheus `query_range` request and returns one
 * {@link MetricSeries} per time series in the matrix response. Throws a
 * {@link PrometheusError} on any non-success outcome (and rethrows
 * `AbortError` untouched).
 */
export async function queryRange(
  promBaseUrl: string,
  params: QueryRangeParams,
): Promise<MetricSeries[]> {
  const search = new URLSearchParams({
    query: params.query,
    start: String(Math.floor(params.start)),
    end: String(Math.floor(params.end)),
    step: `${Math.max(1, Math.floor(params.step))}s`,
  });
  const url = `${promBaseUrl}/api/v1/query_range?${search.toString()}`;

  let resp: Response;
  try {
    resp = await fetch(url, {
      method: "GET",
      headers: { Accept: "application/json" },
      signal: params.signal,
    });
  } catch (err) {
    if ((err as Error)?.name === "AbortError") {
      throw err;
    }
    throw PrometheusError.network((err as Error)?.message ?? "fetch failed");
  }

  let body: string;
  try {
    body = await resp.text();
  } catch (err) {
    if ((err as Error)?.name === "AbortError") {
      throw err;
    }
    throw PrometheusError.network("response body could not be read");
  }

  let envelope: PromEnvelope | undefined;
  try {
    envelope = JSON.parse(body) as PromEnvelope;
  } catch {
    if (!resp.ok) {
      throw PrometheusError.http(resp.status, snippet(body));
    }
    throw PrometheusError.parse("response was not valid JSON");
  }

  if (envelope?.status === "error") {
    throw PrometheusError.status(envelope.error ?? "unknown error");
  }
  if (!resp.ok) {
    throw PrometheusError.http(resp.status, envelope?.error);
  }
  if (envelope?.status !== "success") {
    throw PrometheusError.parse(
      `expected status "success", got "${envelope?.status ?? "<missing>"}"`,
    );
  }
  if (envelope.data?.resultType !== "matrix") {
    throw PrometheusError.parse(
      `expected a matrix result, got "${envelope.data?.resultType ?? "<missing>"}"`,
    );
  }

  return (envelope.data.result ?? []).map(toSeries);
}

function toSeries(result: PromMatrixResult): MetricSeries {
  return {
    labels: result.metric ?? {},
    points: (result.values ?? []).map(([t, raw]) => ({
      t,
      v: parseSampleValue(raw),
    })),
  };
}

/**
 * Parses a Prometheus sample value string. Prometheus encodes special
 * floats as `"NaN"`, `"+Inf"`, and `"-Inf"`, which `Number()` does not
 * handle, so they are mapped explicitly.
 */
export function parseSampleValue(raw: string): number {
  switch (raw) {
    case "NaN":
      return Number.NaN;
    case "+Inf":
      return Number.POSITIVE_INFINITY;
    case "-Inf":
      return Number.NEGATIVE_INFINITY;
    default:
      return Number(raw);
  }
}

function snippet(text: string): string {
  const trimmed = text.trim();
  if (trimmed.length <= 80) {
    return trimmed;
  }
  return `${trimmed.slice(0, 77)}…`;
}
