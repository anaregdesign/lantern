import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import type { ScanVerticesRequest, ScanVerticesResponse } from "./types";

export type {
  ScanVerticesRequest,
  ScanVerticesResponse,
  Vertex,
} from "./types";

/**
 * Calls `LanternService.ScanVertices` over Connect-Web.
 *
 * Pass `cursor` from a previous response's `nextCursor` to fetch the
 * next page. The server enforces a default + hard maximum on `limit`;
 * passing `0` (or omitting it) accepts the server default.
 *
 * The wire cursor is bytes; the Connect+JSON transport encodes it as
 * base64 in either direction, so callers continue to round-trip the
 * `nextCursor` string from one response straight back into the next
 * request without ever decoding it themselves.
 */
export async function scanVertices(
  client: LanternClient,
  request: ScanVerticesRequest,
  init?: { signal?: AbortSignal },
): Promise<ScanVerticesResponse> {
  try {
    const resp = await client.scanVertices(
      {
        prefix: request.prefix ?? "",
        limit: request.limit ?? 0,
        cursor: decodeCursor(request.cursor),
      },
      { signal: init?.signal },
    );
    return resp.toJson() as ScanVerticesResponse;
  } catch (err) {
    throw LanternApiError.fromUnknown("ScanVertices", err);
  }
}

/**
 * Cursor bytes round-trip as base64 in protobuf JSON. The legacy REST
 * adapter passed the cursor through opaquely as a JSON string, so we
 * decode it to a Uint8Array on the way in (Connect-Web's transport
 * re-encodes it the same way the server emitted it).
 */
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
