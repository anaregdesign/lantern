import { Vertex as ProtoVertex } from "~/lib/api/gen/graph/v1/graph_pb";
import type { JsonValue } from "@bufbuild/protobuf";

import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import type { PutVerticesRequest, PutVerticesResponse } from "./types";

export type { PutVerticesRequest, PutVerticesResponse } from "./types";

/**
 * Calls `LanternService.PutVertices` over Connect-Web.
 *
 * Idempotent batch upsert: each vertex by key replaces any existing
 * value at that key.
 */
export async function putVertices(
  client: LanternClient,
  request: PutVerticesRequest,
  init?: { signal?: AbortSignal },
): Promise<PutVerticesResponse> {
  const vertices = (request.vertices ?? []).map((v) =>
    ProtoVertex.fromJson(v as JsonValue),
  );
  try {
    const resp = await client.putVertices(
      { vertices },
      { signal: init?.signal },
    );
    return resp.toJson() as PutVerticesResponse;
  } catch (err) {
    throw LanternApiError.fromUnknown("PutVertices", err);
  }
}
