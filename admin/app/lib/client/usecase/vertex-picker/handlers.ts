import { countVerticesByPrefix } from "~/lib/client/infrastructure/api/count-vertices-by-prefix";
import { LanternApiError } from "~/lib/client/infrastructure/api/error";
import type { LanternClient } from "~/lib/client/infrastructure/api/lantern-client";
import { scanVertices } from "~/lib/client/infrastructure/api/scan-vertices";
import type { VertexPickerAction } from "./reducer";

export interface FetchSuggestionsInput {
  client: LanternClient;
  prefix: string;
  limit: number;
  epoch: number;
  signal?: AbortSignal;
}

/**
 * Scans for the first `limit` vertex keys matching `prefix` and dispatches
 * the resulting suggestions. As in Browse Vertices, `AbortError` is
 * swallowed: cancelling an in-flight scan when the prefix moves on is
 * normal control flow, not a failure.
 */
export async function fetchSuggestions(
  input: FetchSuggestionsInput,
  dispatch: (action: VertexPickerAction) => void,
): Promise<void> {
  dispatch({ type: "SCAN_REQUESTED", epoch: input.epoch });
  try {
    const response = await scanVertices(
      input.client,
      { prefix: input.prefix, limit: input.limit },
      { signal: input.signal },
    );
    const suggestions = (response.vertices ?? [])
      .map((vertex) => vertex.key ?? "")
      .filter((key) => key.length > 0);
    dispatch({ type: "SCAN_RECEIVED", epoch: input.epoch, suggestions });
  } catch (err) {
    if (isAbortError(err)) {
      return;
    }
    dispatch({
      type: "SCAN_FAILED",
      epoch: input.epoch,
      error: messageOf(err),
    });
  }
}

export interface FetchMatchCountInput {
  client: LanternClient;
  prefix: string;
  epoch: number;
  signal?: AbortSignal;
}

/**
 * Counts total matches for `prefix` in parallel with the scan. The count
 * is advisory, so a non-abort failure resolves to 0 rather than surfacing
 * an error over the (more important) suggestion list.
 */
export async function fetchMatchCount(
  input: FetchMatchCountInput,
  dispatch: (action: VertexPickerAction) => void,
): Promise<void> {
  try {
    const count = await countVerticesByPrefix(input.client, input.prefix, {
      signal: input.signal,
    });
    dispatch({ type: "COUNT_RECEIVED", epoch: input.epoch, count });
  } catch (err) {
    if (isAbortError(err)) {
      return;
    }
    dispatch({ type: "COUNT_RECEIVED", epoch: input.epoch, count: 0 });
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
