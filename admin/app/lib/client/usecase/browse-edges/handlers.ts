import { LanternApiError } from "~/lib/client/infrastructure/api/error";
import type { LanternClient } from "~/lib/client/infrastructure/api/lantern-client";
import { scanEdges } from "~/lib/client/infrastructure/api/scan-edges";
import type { BrowseEdgesAction } from "./reducer";
import type { EdgePage } from "./state";

export interface FetchEdgePageInput {
  client: LanternClient;
  tailPrefix: string;
  headPrefix: string;
  cursor: string;
  pageSize: number;
  epoch: number;
  /**
   * How the resulting page should enter the history stack. Defaults to
   * "append"; Refresh/retry passes "replace" so the current page is
   * overwritten in place instead of duplicated.
   */
  mode?: "append" | "replace";
  signal?: AbortSignal;
}

export async function fetchEdgePage(
  input: FetchEdgePageInput,
  dispatch: (action: BrowseEdgesAction) => void,
): Promise<void> {
  dispatch({ type: "PAGE_REQUESTED", epoch: input.epoch });
  try {
    const response = await scanEdges(
      input.client,
      {
        tailPrefix: input.tailPrefix,
        headPrefix: input.headPrefix,
        limit: input.pageSize,
        cursor: input.cursor || undefined,
      },
      { signal: input.signal },
    );
    const page: EdgePage = {
      edges: response.edges ?? [],
      startCursor: input.cursor,
      nextCursor: response.nextCursor ?? "",
    };
    dispatch({
      type: "PAGE_RECEIVED",
      epoch: input.epoch,
      page,
      mode: input.mode ?? "append",
    });
  } catch (err) {
    if (err instanceof DOMException && err.name === "AbortError") {
      return;
    }
    dispatch({
      type: "PAGE_FAILED",
      epoch: input.epoch,
      error: messageOf(err),
    });
  }
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
