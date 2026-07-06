import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import type { AddDecayingEdgeBody, AddDecayingEdgeResponse } from "./types";

export type { AddDecayingEdgeBody, AddDecayingEdgeResponse } from "./types";

/**
 * Calls the SDK's `Lantern.addDecayingEdge` (#953) — the client-side
 * geometric decay staircase that expands into one staggered-TTL
 * `AddEdges` batch. Non-idempotent, like a plain `add edge`: each call
 * accumulates a fresh decaying contribution onto the (tail, head) edge.
 *
 * The SDK validates the `DecayOptions` contract synchronously and throws
 * `InvalidArgumentError` when the curve is ill-formed (ratio outside
 * (0, 1), steps outside [1, MAX_DECAY_STEPS], non-positive interval, or a
 * curve that underflows float32 to zero). We surface that — and any wire
 * fault — as the usual `LanternApiError` the scrollback already renders.
 *
 * Returns the edge's effective (live-sum) weight immediately after the
 * add, which the dispatcher echoes as `total`.
 */
export async function addDecayingEdge(
  client: LanternClient,
  tail: string,
  head: string,
  body: AddDecayingEdgeBody,
  init?: { signal?: AbortSignal },
): Promise<AddDecayingEdgeResponse> {
  try {
    const effectiveWeight = await client.addDecayingEdge(
      tail,
      head,
      {
        initialWeight: body.initialWeight,
        ratio: body.ratio,
        steps: body.steps,
        intervalSeconds: body.intervalSeconds,
      },
      init?.signal,
    );
    return { effectiveWeight };
  } catch (err) {
    throw LanternApiError.fromUnknown("AddDecayingEdge", err);
  }
}
