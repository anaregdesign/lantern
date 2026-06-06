import { Edge as ProtoEdge } from "~/lib/api/gen/graph/v1/graph_pb";
import type { JsonValue } from "@bufbuild/protobuf";

import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import type { PutEdgesRequest, PutEdgesResponse } from "./types";

export type { PutEdgesRequest, PutEdgesResponse } from "./types";

/**
 * Calls `LanternService.PutEdges` over Connect-Web.
 *
 * Idempotent: each (tail, head) pair is overwritten with the supplied
 * weight and expiration. Used for seeding fixtures.
 */
export async function putEdges(
  client: LanternClient,
  request: PutEdgesRequest,
  init?: { signal?: AbortSignal },
): Promise<PutEdgesResponse> {
  const edges = (request.edges ?? []).map((e) =>
    ProtoEdge.fromJson(e as JsonValue),
  );
  try {
    const resp = await client.putEdges({ edges }, { signal: init?.signal });
    return resp.toJson() as PutEdgesResponse;
  } catch (err) {
    throw LanternApiError.fromUnknown("PutEdges", err);
  }
}
