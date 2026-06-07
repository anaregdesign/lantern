import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import { flatVertexToSdkInput } from "./to-flat";
import type { PutVertexBody, PutVertexResponse, Vertex } from "./types";

export type { PutVertexBody, PutVertexResponse, Vertex } from "./types";

/**
 * Calls `LanternService.PutVertex` via `lantern-sdk/web`. The `key`
 * argument always overrides any `body.vertex.key` so the call shape
 * mirrors the legacy REST URL (where the key lived in the path) and
 * the edit-vertex form does not have to re-stitch the key into the
 * payload before sending (#409).
 *
 * Returns the wire response shape (empty object today, kept as
 * `PutVertexResponse` for upward compatibility with future write-side
 * acknowledgements).
 */
export async function putVertex(
  client: LanternClient,
  key: string,
  body: PutVertexBody,
  init?: { signal?: AbortSignal },
): Promise<PutVertexResponse> {
  const flat: Vertex = { ...(body.vertex ?? {}), key };
  try {
    await client.putVertex(flatVertexToSdkInput(flat), init?.signal);
    return {};
  } catch (err) {
    throw LanternApiError.fromUnknown("PutVertex", err);
  }
}
