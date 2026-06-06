import { Edge as ProtoEdge } from "~/lib/api/gen/graph/v1/graph_pb";
import type { JsonValue } from "@bufbuild/protobuf";

import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import type { Edge, PutEdgeBody, PutEdgeResponse } from "./types";

export type { Edge, PutEdgeBody, PutEdgeResponse } from "./types";

/**
 * Calls `LanternService.PutEdge` over Connect-Web.
 *
 * Idempotent: overwrites the (tail, head) edge with the supplied
 * weight and expiration. `tail` and `head` always override any value
 * on `body.edge`.
 */
export async function putEdge(
  client: LanternClient,
  tail: string,
  head: string,
  body: PutEdgeBody,
  init?: { signal?: AbortSignal },
): Promise<PutEdgeResponse> {
  const flat: Edge = { ...(body.edge ?? {}), tail, head };
  try {
    const resp = await client.putEdge(
      { edge: ProtoEdge.fromJson(flat as JsonValue) },
      { signal: init?.signal },
    );
    return resp.toJson() as PutEdgeResponse;
  } catch (err) {
    throw LanternApiError.fromUnknown("PutEdge", err);
  }
}
