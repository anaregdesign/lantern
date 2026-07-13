import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import { sdkVertexToFlat } from "./to-flat";
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
 * Returns one bounded endpoint-sticky BM25 page. FULL_VERTEX hits are mapped
 * at this adapter boundary into the admin's flat Vertex shape, preserving the
 * server's selection-time value/TTL snapshot without a hydration RPC.
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
    const page = await client.searchVerticesPage(
      request.query,
      sdkSearchOptions(request),
      init?.signal,
    );
    const hits: SearchHit[] = page.hits.map(toAdminSearchHit);
    return {
      hits,
      nextCursor: page.nextCursor,
      effectiveLimit: page.effectiveLimit,
      truncated: page.truncated,
      continuationLimited: page.continuationLimited,
    };
  } catch (err) {
    throw LanternApiError.fromUnknown("SearchVertices", err);
  }
}

/**
 * Follow the maintained Node SDK's bounded search iterator. The iterator owns
 * cursor propagation and raises its typed continuation error after the final
 * retained hit; this adapter only maps SDK vertices into Admin's flat shape.
 */
export async function searchAllVertices(
  client: LanternClient,
  request: SearchVerticesRequest,
  init?: { signal?: AbortSignal },
): Promise<SearchHit[]> {
  try {
    const hits: SearchHit[] = [];
    for await (const hit of client.searchVerticesIter(
      request.query,
      sdkSearchOptions(request),
      init?.signal,
    )) {
      hits.push(toAdminSearchHit(hit));
    }
    return hits;
  } catch (err) {
    throw LanternApiError.fromUnknown("SearchVertices", err);
  }
}

type SdkSearchHit = Awaited<
  ReturnType<LanternClient["searchVertices"]>
>[number];

function toAdminSearchHit(hit: SdkSearchHit): SearchHit {
  return {
    key: hit.key,
    score: hit.score,
    ...(hit.vertex ? { vertex: sdkVertexToFlat(hit.vertex) } : {}),
    ...(hit.projectionStatus ? { projectionStatus: hit.projectionStatus } : {}),
  };
}

function sdkSearchOptions(request: SearchVerticesRequest) {
  return {
    limit: request.limit ?? 0,
    prefix: request.prefix ?? "",
    matchMode: request.matchMode === "server" ? undefined : request.matchMode,
    minShouldMatch:
      request.matchMode === "min-should" ? request.minShouldMatch : undefined,
    phrase: request.phrase,
    fuzziness: request.fuzziness,
    prefixTerms: request.prefixTerms,
    cursor: request.cursor,
    projection: request.projection,
  };
}
