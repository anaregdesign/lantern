import { LanternApiError } from "~/lib/client/infrastructure/api/error";
import {
  illuminate,
  type IlluminateRequest,
} from "~/lib/client/infrastructure/api/illuminate";
import type { LanternClient } from "~/lib/client/infrastructure/api/lantern-client";
import type { IlluminateAction } from "./reducer";
import type { IlluminateControls, IlluminateFrame } from "./state";

export interface FetchIlluminateInput {
  client: LanternClient;
  seed: string;
  controls: IlluminateControls;
  epoch: number;
  signal?: AbortSignal;
}

/**
 * Fetches one Illuminate frame and dispatches the resulting state
 * transition. Swallows `AbortError` because cancellation is a normal
 * control-flow event in this view (the user drags a slider and we move on
 * to a fresh request).
 */
export async function fetchIlluminate(
  input: FetchIlluminateInput,
  dispatch: (action: IlluminateAction) => void,
): Promise<void> {
  dispatch({ type: "FETCH_REQUESTED", epoch: input.epoch });
  try {
    const request: IlluminateRequest = {
      seed: input.seed,
      step: input.controls.step,
      k: input.controls.k,
      tfidf: input.controls.tfidf,
      optimization: input.controls.optimization,
    };
    const response = await illuminate(input.client, request, {
      signal: input.signal,
    });
    const frame: IlluminateFrame = {
      seed: input.seed,
      controls: input.controls,
      vertices: response.graph?.vertices ?? [],
      edges: response.graph?.edges ?? [],
    };
    dispatch({ type: "FETCH_RECEIVED", epoch: input.epoch, frame });
  } catch (err) {
    if (isAbortError(err)) {
      return;
    }
    dispatch({
      type: "FETCH_FAILED",
      epoch: input.epoch,
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
