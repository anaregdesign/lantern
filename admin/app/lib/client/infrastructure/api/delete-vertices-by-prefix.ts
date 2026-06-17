import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";

export interface DeleteVerticesByPrefixRequest {
  prefix: string;
  limit?: number;
  dryRun?: boolean;
}

/**
 * Calls `LanternService.DeleteVerticesByPrefix` via `lantern-sdk/web`.
 * Returns the number of vertices deleted (or, with `dryRun`, that would
 * be deleted), clamped to a JS-safe integer like
 * {@link countVerticesByPrefix}. Backs the destructive
 * `delete-prefix vertices <prefix> [limit=] [confirm=yes|dry_run=true]`
 * CLI grammar (#679); the safety gate lives in the parser.
 */
export async function deleteVerticesByPrefix(
  client: LanternClient,
  request: DeleteVerticesByPrefixRequest,
  init?: { signal?: AbortSignal },
): Promise<number> {
  try {
    const big = await client.deleteVerticesByPrefix(
      request.prefix,
      { limit: request.limit ?? 0, dryRun: request.dryRun ?? false },
      init?.signal,
    );
    if (big > BigInt(Number.MAX_SAFE_INTEGER)) {
      return Number.MAX_SAFE_INTEGER;
    }
    return Number(big);
  } catch (err) {
    throw LanternApiError.fromUnknown("DeleteVerticesByPrefix", err);
  }
}
