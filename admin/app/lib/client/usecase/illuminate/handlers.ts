import { LanternApiError } from "~/lib/client/infrastructure/api/error";
import {
  illuminate,
  type IlluminateRequest,
} from "~/lib/client/infrastructure/api/illuminate";
import type { LanternClient } from "~/lib/client/infrastructure/api/lantern-client";
import type { IlluminateAction } from "./reducer";
import type { IlluminateControls } from "./state";

export interface ExpandInput {
  client: LanternClient;
  expansionId: number;
  origin: string;
  controls: IlluminateControls;
  startedAtMs: number;
  signal?: AbortSignal;
}

/**
 * Fires one Illuminate call and dispatches the resulting expansion
 * action. Swallows `AbortError` because cancellation is a normal
 * control-flow event in this view (the hook aborts in-flight
 * expansions on Clear / unmount / initial-seed change).
 *
 * Per #466 each expansion carries its own AbortController, so a later
 * click does NOT cancel earlier ones — they all merge into the
 * accumulator as they return.
 */
export async function runExpansion(
  input: ExpandInput,
  dispatch: (action: IlluminateAction) => void,
): Promise<void> {
  dispatch({
    type: "EXPANSION_REQUESTED",
    expansionId: input.expansionId,
    origin: input.origin,
    controls: input.controls,
    startedAtMs: input.startedAtMs,
  });
  try {
    const request: IlluminateRequest = {
      seed: input.origin,
      step: input.controls.step,
      k: input.controls.k,
      algorithm: input.controls.algorithm,
      objective: input.controls.objective,
      weighting: input.controls.weighting,
      vertexPrefix: input.controls.vertexPrefix,
    };
    const response = await illuminate(input.client, request, {
      signal: input.signal,
    });
    dispatch({
      type: "EXPANSION_RECEIVED",
      expansionId: input.expansionId,
      origin: input.origin,
      controls: input.controls,
      startedAtMs: input.startedAtMs,
      vertices: response.graph?.vertices ?? [],
      edges: response.graph?.edges ?? [],
      receivedAtMs:
        typeof performance !== "undefined" ? performance.now() : Date.now(),
    });
  } catch (err) {
    if (isAbortError(err)) {
      // Synthesise a FAILED that the reducer treats as "discount the
      // pending counter" without setting an error; we still need to
      // balance the optimistic EXPANSION_REQUESTED.
      dispatch({
        type: "EXPANSION_FAILED",
        expansionId: input.expansionId,
        error: "",
      });
      return;
    }
    dispatch({
      type: "EXPANSION_FAILED",
      expansionId: input.expansionId,
      error: messageOf(err),
    });
  }
}

function isAbortError(err: unknown): boolean {
  return err instanceof DOMException && err.name === "AbortError";
}

function messageOf(err: unknown): string {
  if (err instanceof LanternApiError) {
    return err.grpcMessage ?? err.message;
  }
  if (err instanceof Error) {
    return err.message;
  }
  return String(err);
}
