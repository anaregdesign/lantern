import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import { flatEdgeToSdkInput } from "./to-flat";
import type { AddEdgeBody, AddEdgeResponse, Edge } from "./types";

export type { AddEdgeBody, AddEdgeResponse, Edge } from "./types";

/**
 * Calls `LanternService.AddEdge` via `lantern-sdk/web`.
 *
 * Non-idempotent: each call accumulates another time-decaying
 * contribution onto the (tail, head) edge. `tail` and `head` always
 * override any value carried on `body.edge` so the call shape
 * mirrors the legacy REST URL where the endpoints lived in the path
 * (#409).
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
    await client.addEdge(flatEdgeToSdkInput(flat), init?.signal);
    return {};
  } catch (err) {
    throw LanternApiError.fromUnknown("AddEdge", err);
  }
}
