import type { components, operations } from "./lantern-api.gen";
import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";

export type Vertex = components["schemas"]["v1Vertex"];
export type Edge = components["schemas"]["v1Edge"];
export type Graph = components["schemas"]["v1Graph"];
export type IlluminateResponse = components["schemas"]["v1IlluminateResponse"];

/**
 * Query knobs accepted by `LanternService_Illuminate`. Mirrors the
 * generated `operations[…].parameters.query` shape but is the contract the
 * usecase layer talks to (no nested `?` chains).
 */
export type Optimization = NonNullable<
  NonNullable<
    operations["LanternService_Illuminate"]["parameters"]["query"]
  >["optimization"]
>;

export interface IlluminateRequest {
  /** Seed vertex key. Required; must be non-empty (caller decodes URL first). */
  seed: string;
  /** Max hops from the seed (server-side default if omitted). */
  step?: number;
  /** Max neighbours expanded per hop. */
  k?: number;
  /** Reweight edges using TF-IDF before tree selection. */
  tfidf?: boolean;
  /** Tree-selection strategy applied to the returned subgraph. */
  optimization?: Optimization;
}

/**
 * Calls `LanternService_Illuminate` (GET `/v1/illuminate/{seed}`).
 *
 * The seed segment is URL-encoded here so callers can pass raw keys
 * (including `:`, `/`, spaces, …). Empty optional knobs are omitted from
 * the query string so the server's defaults apply.
 */
export async function illuminate(
  client: LanternClient,
  request: IlluminateRequest,
  init?: { signal?: AbortSignal },
): Promise<IlluminateResponse> {
  if (request.seed === "") {
    throw new Error("illuminate: seed must be non-empty");
  }
  const params = new URLSearchParams();
  if (request.step !== undefined) {
    params.set("step", String(request.step));
  }
  if (request.k !== undefined) {
    params.set("k", String(request.k));
  }
  if (request.tfidf !== undefined) {
    params.set("tfidf", String(request.tfidf));
  }
  if (request.optimization !== undefined) {
    params.set("optimization", request.optimization);
  }
  const query = params.toString();
  const path =
    `/v1/illuminate/${encodeURIComponent(request.seed)}` +
    (query === "" ? "" : `?${query}`);
  const response = await client.request(path, {
    method: "GET",
    signal: init?.signal,
  });
  if (!response.ok) {
    throw await LanternApiError.fromResponse(response, "Illuminate");
  }
  return (await response.json()) as IlluminateResponse;
}
