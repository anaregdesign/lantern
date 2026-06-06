import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import type { ScanEdgesRequest, ScanEdgesResponse } from "./types";

export type { Edge, ScanEdgesRequest, ScanEdgesResponse } from "./types";

/**
 * Calls `LanternService.ScanEdges` over Connect-Web.
 *
 * Pass `cursor` from a previous response's `nextCursor` to fetch the
 * next page. Either prefix may be empty; both empty scans every edge.
 */
export async function scanEdges(
  client: LanternClient,
  request: ScanEdgesRequest,
  init?: { signal?: AbortSignal },
): Promise<ScanEdgesResponse> {
  try {
    const resp = await client.scanEdges(
      {
        tailPrefix: request.tailPrefix ?? "",
        headPrefix: request.headPrefix ?? "",
        limit: request.limit ?? 0,
        cursor: decodeCursor(request.cursor),
      },
      { signal: init?.signal },
    );
    return resp.toJson() as ScanEdgesResponse;
  } catch (err) {
    throw LanternApiError.fromUnknown("ScanEdges", err);
  }
}

function decodeCursor(cursor: string | undefined): Uint8Array<ArrayBuffer> {
  if (!cursor) {
    return new Uint8Array(new ArrayBuffer(0));
  }
  const bin = atob(cursor);
  const buf = new ArrayBuffer(bin.length);
  const out = new Uint8Array(buf);
  for (let i = 0; i < bin.length; i++) {
    out[i] = bin.charCodeAt(i);
  }
  return out;
}
