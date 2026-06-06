import { Vertex as ProtoVertex } from "~/lib/api/gen/graph/v1/graph_pb";
import type { JsonValue } from "@bufbuild/protobuf";

import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import type { PutVertexBody, PutVertexResponse, Vertex } from "./types";

export type { PutVertexBody, PutVertexResponse, Vertex } from "./types";

/**
 * Calls `LanternService.PutVertex` over Connect-Web.
 *
 * The `key` argument always overrides any `body.vertex.key` so the
 * call shape mirrors the legacy REST URL (where the key lived in the
 * path). This keeps the existing edit-vertex flow unchanged: callers
 * still pass the key separately even though the wire form is a single
 * proto message.
 */
export async function putVertex(
  client: LanternClient,
  key: string,
  body: PutVertexBody,
  init?: { signal?: AbortSignal },
): Promise<PutVertexResponse> {
  const flat: Vertex = { ...(body.vertex ?? {}), key };
  try {
    const resp = await client.putVertex(
      { vertex: ProtoVertex.fromJson(flat as JsonValue) },
      { signal: init?.signal },
    );
    return resp.toJson() as PutVertexResponse;
  } catch (err) {
    throw LanternApiError.fromUnknown("PutVertex", err);
  }
}
