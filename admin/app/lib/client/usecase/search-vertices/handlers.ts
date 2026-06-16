import { LanternApiError } from "~/lib/client/infrastructure/api/error";
import { getVertices } from "~/lib/client/infrastructure/api/get-vertices";
import type { LanternClient } from "~/lib/client/infrastructure/api/lantern-client";
import { searchVertices } from "~/lib/client/infrastructure/api/search-vertices";
import type { SearchResultRow } from "./state";
import type { SearchVerticesAction } from "./reducer";

export interface FetchSearchResultsInput {
  client: LanternClient;
  query: string;
  limit: number;
  prefix?: string;
  epoch: number;
  signal?: AbortSignal;
}

/**
 * Runs the two-step content search: BM25 keyword search for ranked
 * `{ key, score }` hits, then a single batch `GetVertices` to hydrate the
 * keys into full vertices. The ranked hit order is authoritative — the
 * hydration result is partitioned into found/missing and re-projected
 * against the hit list, so a hit whose vertex expired between the two
 * calls still renders its rank slot (with a `null` vertex) rather than
 * silently dropping out of the ranking.
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
  dispatch({ type: "SEARCH_REQUESTED", epoch: input.epoch });
  try {
    const { hits } = await searchVertices(
      input.client,
      { query: input.query, limit: input.limit, prefix: input.prefix },
      { signal: input.signal },
    );
    if (hits.length === 0) {
      // Empty match set is an empty success, not an error.
      dispatch({ type: "SEARCH_RECEIVED", epoch: input.epoch, results: [] });
      return;
    }
    const keys = hits.map((hit) => hit.key);
    const { found } = await getVertices(input.client, keys, {
      signal: input.signal,
    });
    const byKey = new Map(found.map((vertex) => [vertex.key ?? "", vertex]));
    const results: SearchResultRow[] = hits.map((hit) => ({
      key: hit.key,
      score: hit.score,
      vertex: byKey.get(hit.key) ?? null,
    }));
    dispatch({ type: "SEARCH_RECEIVED", epoch: input.epoch, results });
  } catch (err) {
    if (isAbortError(err)) {
      return;
    }
    if (LanternApiError.isDisabled(err)) {
      dispatch({ type: "SEARCH_DISABLED", epoch: input.epoch });
      return;
    }
    dispatch({
      type: "SEARCH_FAILED",
      epoch: input.epoch,
      error: messageOf(err),
    });
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
