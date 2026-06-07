import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import { sdkVertexToFlat } from "./to-flat";
import type { ScanVerticesRequest, ScanVerticesResponse } from "./types";

export type {
  ScanVerticesRequest,
  ScanVerticesResponse,
  Vertex,
} from "./types";

/**
 * Calls `LanternService.ScanVertices` via `lantern-sdk/web`. Pass
 * `cursor` from a previous response's `nextCursor` to fetch the next
 * page. The wire cursor is bytes; this adapter decodes it from
 * admin's base64-string representation (the legacy adapter's wire
 * shape) and re-encodes the next cursor for the caller (#409).
 */
export async function scanVertices(
  client: LanternClient,
  request: ScanVerticesRequest,
  init?: { signal?: AbortSignal },
): Promise<ScanVerticesResponse> {
  try {
    const page = await client.scanVertices(
      request.prefix ?? "",
      { limit: request.limit ?? 0, cursor: decodeCursor(request.cursor) },
      init?.signal,
    );
    return {
      vertices: page.vertices.map((v) => {
        // Page items come back as SDK rich Vertex objects already;
        // sdkVertexToFlat re-projects them onto admin's flat shape.
        return sdkVertexToFlat(v);
      }),
      nextCursor: encodeCursor(page.nextCursor),
    };
  } catch (err) {
    throw LanternApiError.fromUnknown("ScanVertices", err);
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
