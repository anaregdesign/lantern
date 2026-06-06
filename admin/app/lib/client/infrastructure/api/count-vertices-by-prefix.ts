import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";

/**
 * Calls `LanternService.CountVerticesByPrefix` over Connect-Web.
 *
 * Returns the live vertex count whose key starts with the given prefix.
 * The wire format uses a `uint64` rendered as a JSON string; this
 * helper parses it back into a JS number — safe up to 2^53. Counts
 * beyond that are clamped to `Number.MAX_SAFE_INTEGER`; the UI only
 * needs an order-of-magnitude indicator.
 */
export async function countVerticesByPrefix(
  client: LanternClient,
  prefix: string,
  init?: { signal?: AbortSignal },
): Promise<number> {
  try {
    const resp = await client.countVerticesByPrefix(
      { prefix },
      { signal: init?.signal },
    );
    // resp.count is a bigint on the proto class (uint64 maps to bigint
    // in connect-es v1). Clamp at MAX_SAFE_INTEGER for the UI's order-
    // of-magnitude display.
    const big = resp.count;
    if (big > BigInt(Number.MAX_SAFE_INTEGER)) {
      return Number.MAX_SAFE_INTEGER;
    }
    return Number(big);
  } catch (err) {
    throw LanternApiError.fromUnknown("CountVerticesByPrefix", err);
  }
}
