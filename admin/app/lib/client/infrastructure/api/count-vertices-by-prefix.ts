import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";

/**
 * Calls `LanternService.CountVerticesByPrefix` via `lantern-sdk/web`.
 * The wire format uses `uint64`; the SDK surfaces it as bigint.
 * Admin's UI only needs an order-of-magnitude indicator, so this
 * adapter clamps at `Number.MAX_SAFE_INTEGER` (#409).
 */
export async function countVerticesByPrefix(
  client: LanternClient,
  prefix: string,
  init?: { signal?: AbortSignal },
): Promise<number> {
  try {
    const big = await client.countVerticesByPrefix(prefix, init?.signal);
    if (big > BigInt(Number.MAX_SAFE_INTEGER)) {
      return Number.MAX_SAFE_INTEGER;
    }
    return Number(big);
  } catch (err) {
    throw LanternApiError.fromUnknown("CountVerticesByPrefix", err);
  }
}
