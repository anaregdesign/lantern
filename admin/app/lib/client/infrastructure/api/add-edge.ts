import { Edge as ProtoEdge } from "~/lib/api/gen/graph/v1/graph_pb";
import type { JsonValue } from "@bufbuild/protobuf";

import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import type { AddEdgeBody, AddEdgeResponse, Edge } from "./types";

export type { AddEdgeBody, AddEdgeResponse, Edge } from "./types";

/**
 * Calls `LanternService.AddEdge` over Connect-Web.
 *
 * Non-idempotent: each call accumulates another time-decaying
 * contribution onto the (tail, head) edge. `tail` and `head` always
 * override any value carried on `body.edge` so the call shape mirrors
 * the legacy REST URL where the endpoints lived in the path.
 */
export async function addEdge(
  client: LanternClient,
  tail: string,
  head: string,
  body: AddEdgeBody,
  init?: { signal?: AbortSignal },
): Promise<AddEdgeResponse> {
  const flat: Edge = { ...(body.edge ?? {}), tail, head };
  try {
    const resp = await client.addEdge(
      { edge: ProtoEdge.fromJson(flat as JsonValue) },
      { signal: init?.signal },
    );
    return resp.toJson() as AddEdgeResponse;
  } catch (err) {
    throw LanternApiError.fromUnknown("AddEdge", err);
  }
}
