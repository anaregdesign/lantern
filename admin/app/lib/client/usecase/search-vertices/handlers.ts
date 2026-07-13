import { LanternApiError } from "~/lib/client/infrastructure/api/error";
import type { LanternClient } from "~/lib/client/infrastructure/api/lantern-client";
import { searchVertices } from "~/lib/client/infrastructure/api/search-vertices";
import type { SearchQueryOptions, SearchResultRow } from "./state";
import type { SearchVerticesAction } from "./reducer";

export interface FetchSearchResultsInput {
  client: LanternClient;
  query: string;
  limit: number;
  prefix?: string;
  options: SearchQueryOptions;
  epoch: number;
  signal?: AbortSignal;
  cursor?: Uint8Array;
  append?: boolean;
}

/**
 * Runs one FULL_VERTEX search page. Ranking and value/TTL selection share the
 * server's commit barrier, so Admin never pairs an old score with a newer
 * unrelated value through a second hydration RPC.
 *
 * As in Browse Vertices and the vertex picker, `AbortError` is swallowed:
 * cancelling an in-flight search when the query moves on is normal control
 * flow, not a failure. A `FAILED_PRECONDITION` (the server has the keyword
 * index disabled) becomes a calm `SEARCH_DISABLED`, not an error.
 */
export async function fetchSearchResults(
  input: FetchSearchResultsInput,
  dispatch: (action: SearchVerticesAction) => void,
): Promise<void> {
  dispatch({
    type: input.append ? "SEARCH_MORE_REQUESTED" : "SEARCH_REQUESTED",
    epoch: input.epoch,
  });
  try {
    const page = await searchVertices(
      input.client,
      {
        query: input.query,
        limit: input.limit,
        prefix: input.prefix,
        matchMode: input.options.matchMode,
        minShouldMatch: input.options.minShouldMatch,
        phrase: input.options.phrase,
        fuzziness: input.options.fuzziness,
        prefixTerms: input.options.prefixTerms,
        cursor: input.cursor,
        projection: "full-vertex",
      },
      { signal: input.signal },
    );
    const results: SearchResultRow[] = page.hits.map((hit) => ({
      key: hit.key,
      score: hit.score,
      vertex: hit.vertex ?? null,
    }));
    dispatch({
      type: input.append ? "SEARCH_MORE_RECEIVED" : "SEARCH_RECEIVED",
      epoch: input.epoch,
      results,
      nextCursor: page.nextCursor,
      truncated: page.truncated,
      continuationLimited: page.continuationLimited,
    });
  } catch (err) {
    if (isAbortError(err)) {
      return;
    }
    if (LanternApiError.isDisabled(err)) {
      dispatch({ type: "SEARCH_DISABLED", epoch: input.epoch });
      return;
    }
    dispatch(
      input.append
        ? {
            type: "SEARCH_MORE_FAILED",
            epoch: input.epoch,
            error: messageOf(err),
          }
        : {
            type: "SEARCH_FAILED",
            epoch: input.epoch,
            error: messageOf(err),
          },
    );
  }
}

function isAbortError(err: unknown): boolean {
  return err instanceof DOMException && err.name === "AbortError";
}

function messageOf(err: unknown): string {
  if (err instanceof LanternApiError) {
    return err.grpcMessage ?? err.message;
  }
  if (err instanceof Error) {
    return err.message;
  }
  return String(err);
}
