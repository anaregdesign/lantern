import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";

export interface ScanVertexKeysRequest {
  prefix: string;
  limit?: number;
  cursor?: string;
}

export interface ScanVertexKeysResponse {
  keys: string[];
  nextCursor: string;
}

/**
 * Calls `LanternService.ScanVertexKeys` via `lantern-sdk/web` — the
 * keys-only, wire-efficient prefix listing that backs the Redis-familiar
 * `keys` verb (#674). Unlike `scanVertices` it returns just the matching
 * vertex keys (no values), and a non-empty `prefix` is REQUIRED (the server
 * rejects an empty prefix with `invalid_argument`).
 *
 * The wire cursor is bytes; this adapter decodes it from admin's base64
 * string representation and re-encodes the next cursor for the caller,
 * mirroring `scan-vertices.ts`. The cursor is its own opaque kind and is
 * NOT interchangeable with a `scanVertices` cursor.
 */
export async function scanVertexKeys(
  client: LanternClient,
  request: ScanVertexKeysRequest,
  init?: { signal?: AbortSignal },
): Promise<ScanVertexKeysResponse> {
  try {
    const page = await client.scanVertexKeys(
      request.prefix ?? "",
      { limit: request.limit ?? 0, cursor: decodeCursor(request.cursor) },
      init?.signal,
    );
    return {
      keys: page.keys,
      nextCursor: encodeCursor(page.nextCursor),
    };
  } catch (err) {
    throw LanternApiError.fromUnknown("ScanVertexKeys", err);
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
