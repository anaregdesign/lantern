import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import { sdkEdgeToFlat } from "./to-flat";
import type { ScanEdgesRequest, ScanEdgesResponse } from "./types";

export type { Edge, ScanEdgesRequest, ScanEdgesResponse } from "./types";

/**
 * Calls `LanternService.ScanEdges` via `lantern-sdk/web`. Either
 * prefix may be empty; both empty scans every edge. The wire cursor
 * is bytes; admin carries it as a base64 string for opaque
 * round-tripping through URL state (#409).
 */
export async function scanEdges(
  client: LanternClient,
  request: ScanEdgesRequest,
  init?: { signal?: AbortSignal },
): Promise<ScanEdgesResponse> {
  try {
    const page = await client.scanEdges(
      {
        tailPrefix: request.tailPrefix ?? "",
        headPrefix: request.headPrefix ?? "",
        limit: request.limit ?? 0,
        cursor: decodeCursor(request.cursor),
      },
      init?.signal,
    );
    return {
      edges: page.edges.map(sdkEdgeToFlat),
      nextCursor: encodeCursor(page.nextCursor),
    };
  } catch (err) {
    throw LanternApiError.fromUnknown("ScanEdges", err);
  }
}

function decodeCursor(cursor: string | undefined): Uint8Array {
  if (!cursor) return new Uint8Array();
  const bin = atob(cursor);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

function encodeCursor(cursor: Uint8Array): string {
  if (cursor.length === 0) return "";
  let s = "";
  for (let i = 0; i < cursor.length; i++) s += String.fromCharCode(cursor[i]);
  return btoa(s);
}
