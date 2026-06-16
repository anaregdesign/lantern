import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import type {
  SearchHit,
  SearchVerticesRequest,
  SearchVerticesResponse,
} from "./types";

export type {
  SearchHit,
  SearchVerticesRequest,
  SearchVerticesResponse,
} from "./types";

/**
 * Calls `LanternService.SearchVertices` via `lantern-sdk/web` (#627).
 *
 * Returns the server's BM25-ranked `{ key, score }` hits in descending
 * relevance order. A blank query, or a query that matches nothing,
 * resolves to an empty `hits` array — an empty success, not an error.
 *
 * When the server has the keyword index disabled (opt-out via
 * `LANTERN_SEARCH_ENABLED=false`) the SDK raises
 * `FailedPreconditionError`; this adapter re-wraps it as a
 * `LanternApiError` with code `"failed_precondition"` so the usecase
 * layer can discriminate it via `LanternApiError.isDisabled(err)` and
 * render a calm "content search is not enabled" state.
 */
export async function searchVertices(
  client: LanternClient,
  request: SearchVerticesRequest,
  init?: { signal?: AbortSignal },
): Promise<SearchVerticesResponse> {
  try {
    const hits: SearchHit[] = await client.searchVertices(
      request.query,
      { limit: request.limit ?? 0, prefix: request.prefix ?? "" },
      init?.signal,
    );
    return { hits };
  } catch (err) {
    throw LanternApiError.fromUnknown("SearchVertices", err);
  }
}
