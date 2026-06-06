import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";

/**
 * Calls `LanternService_CountVerticesByPrefix`
 * (GET `/v1/vertices/count/{prefix}`).
 *
 * Returns the live vertex count whose key starts with the given prefix.
 * The wire format uses a `uint64` rendered as a JSON string; this helper
 * parses it back into a JS number — safe up to 2^53. Counts beyond that
 * are clamped to `Number.MAX_SAFE_INTEGER`; the UI only needs an order-of-
 * magnitude indicator.
 */
export async function countVerticesByPrefix(
  client: LanternClient,
  prefix: string,
  init?: { signal?: AbortSignal },
): Promise<number> {
  // gRPC-gateway URL-encodes the {prefix} path segment automatically when
  // we call encodeURIComponent here. Empty prefix is allowed and returns
  // the total live count.
  const path = `/v1/vertices/count/${encodeURIComponent(prefix)}`;
  const response = await client.request(path, {
    method: "GET",
    signal: init?.signal,
  });
  if (!response.ok) {
    throw await LanternApiError.fromResponse(response, "CountVerticesByPrefix");
  }
  const body = (await response.json()) as { count?: string };
  if (typeof body.count !== "string") {
    return 0;
  }
  const parsed = Number(body.count);
  if (!Number.isFinite(parsed)) {
    return Number.MAX_SAFE_INTEGER;
  }
  return parsed > Number.MAX_SAFE_INTEGER ? Number.MAX_SAFE_INTEGER : parsed;
}
