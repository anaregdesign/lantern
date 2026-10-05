import { countVerticesByPrefix } from "~/lib/client/infrastructure/api/count-vertices-by-prefix";
import { LanternApiError } from "~/lib/client/infrastructure/api/error";
import type { LanternClient } from "~/lib/client/infrastructure/api/lantern-client";
import { scanVertices } from "~/lib/client/infrastructure/api/scan-vertices";
import type { BrowseVerticesAction } from "./reducer";
import type { BrowseVerticesFailure, VertexPage } from "./state";

export interface FetchPageInput {
  client: LanternClient;
  prefix: string;
  cursor: string;
  pageSize: number;
  epoch: number;
  requestId: number;
  /**
   * How the resulting page should enter the history stack. Defaults to
   * "append"; Refresh/retry passes "replace" so the current page is
   * overwritten in place instead of duplicated. A fresh first page uses
   * "reset" to discard history from an earlier client or page-size setting.
   */
  mode?: "append" | "replace" | "reset";
  signal?: AbortSignal;
}

/**
 * Fetches one page of vertices and dispatches the resulting state
 * transition. The handler swallows `AbortError` because cancellation is a
 * normal control-flow event in this view (the user types another character
 * and we move on to a fresh request).
 */
export async function fetchPage(
  input: FetchPageInput,
  dispatch: (action: BrowseVerticesAction) => void,
): Promise<void> {
  dispatch({
    type: "PAGE_REQUESTED",
    epoch: input.epoch,
    requestId: input.requestId,
  });
  try {
    const response = await scanVertices(
      input.client,
      {
        prefix: input.prefix,
        limit: input.pageSize,
        cursor: input.cursor || undefined,
      },
      { signal: input.signal },
    );
    const page: VertexPage = {
      vertices: response.vertices ?? [],
      startCursor: input.cursor,
      nextCursor: response.nextCursor ?? "",
    };
    dispatch({
      type: "PAGE_RECEIVED",
      epoch: input.epoch,
      requestId: input.requestId,
      page,
      mode: input.mode ?? "append",
    });
  } catch (err) {
    if (isAbortError(err)) {
      return;
    }
    dispatch({
      type: "PAGE_FAILED",
      epoch: input.epoch,
      requestId: input.requestId,
      error: failureOf(err),
    });
  }
}

export interface FetchCountInput {
  client: LanternClient;
  prefix: string;
  epoch: number;
  requestId: number;
  signal?: AbortSignal;
}

export async function fetchCount(
  input: FetchCountInput,
  dispatch: (action: BrowseVerticesAction) => void,
): Promise<void> {
  dispatch({
    type: "COUNT_REQUESTED",
    epoch: input.epoch,
    requestId: input.requestId,
  });
  try {
    const count = await countVerticesByPrefix(input.client, input.prefix, {
      signal: input.signal,
    });
    dispatch({
      type: "COUNT_RECEIVED",
      epoch: input.epoch,
      requestId: input.requestId,
      count,
    });
  } catch (err) {
    if (isAbortError(err)) {
      return;
    }
    dispatch({
      type: "COUNT_FAILED",
      epoch: input.epoch,
      requestId: input.requestId,
      error: failureOf(err),
    });
  }
}

function isAbortError(err: unknown): boolean {
  return err instanceof DOMException && err.name === "AbortError";
}

function failureOf(err: unknown): BrowseVerticesFailure {
  return {
    kind:
      err instanceof LanternApiError && err.code === "permission_denied"
        ? "denied"
        : "unavailable",
    message: messageOf(err),
  };
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
