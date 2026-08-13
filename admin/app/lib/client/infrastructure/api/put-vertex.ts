import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import { requireAppliedPutOutcome } from "./put-outcome";
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
 * Returns the authoritative outcome and fails closed when the server did not
 * leave the value live, so the editor cannot display a false success.
 */
export async function putVertex(
  client: LanternClient,
  key: string,
  body: PutVertexBody,
  init?: { signal?: AbortSignal },
): Promise<PutVertexResponse> {
  const flat: Vertex = { ...(body.vertex ?? {}), key };
  try {
    const outcome = await client.putVertex(
      flatVertexToSdkInput(flat),
      init?.signal,
    );
    requireAppliedPutOutcome(
      "PutVertex",
      `vertex ${JSON.stringify(key)}`,
      outcome,
    );
    return { outcome };
  } catch (err) {
    throw LanternApiError.fromUnknown("PutVertex", err);
  }
}
