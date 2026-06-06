import { deleteVertex } from "~/lib/client/infrastructure/api/delete-vertex";
import { LanternApiError } from "~/lib/client/infrastructure/api/error";
import { getVertex } from "~/lib/client/infrastructure/api/get-vertex";
import type { LanternClient } from "~/lib/client/infrastructure/api/lantern-client";
import { putVertex } from "~/lib/client/infrastructure/api/put-vertex";
import type { PutVertexBody } from "~/lib/client/infrastructure/api/put-vertex";
import type { EditVertexAction } from "./reducer";

export interface LoadVertexInput {
  client: LanternClient;
  key: string;
  epoch: number;
  signal?: AbortSignal;
}

export async function loadVertex(
  input: LoadVertexInput,
  dispatch: (action: EditVertexAction) => void,
): Promise<void> {
  dispatch({ type: "LOAD_REQUESTED", epoch: input.epoch });
  try {
    const vertex = await getVertex(input.client, input.key, {
      signal: input.signal,
    });
    dispatch({ type: "LOAD_RECEIVED", epoch: input.epoch, vertex });
  } catch (err) {
    if (isAbortError(err)) return;
    dispatch({
      type: "LOAD_FAILED",
      epoch: input.epoch,
      error: messageOf(err),
    });
  }
}

export interface SaveVertexInput {
  client: LanternClient;
  key: string;
  body: PutVertexBody;
  signal?: AbortSignal;
}

/**
 * Performs the PutVertex round-trip and then re-reads the vertex so the
 * view shows the canonical server-side representation (including any
 * server-applied normalization).
 */
export async function saveVertex(
  input: SaveVertexInput,
  dispatch: (action: EditVertexAction) => void,
): Promise<void> {
  dispatch({ type: "SAVE_REQUESTED" });
  try {
    await putVertex(input.client, input.key, input.body, {
      signal: input.signal,
    });
    const fresh = await getVertex(input.client, input.key, {
      signal: input.signal,
    });
    if (!fresh) {
      dispatch({
        type: "SAVE_FAILED",
        error: "Vertex disappeared immediately after save.",
      });
      return;
    }
    dispatch({ type: "SAVE_SUCCEEDED", vertex: fresh });
  } catch (err) {
    if (isAbortError(err)) return;
    dispatch({ type: "SAVE_FAILED", error: messageOf(err) });
  }
}

export interface DeleteVertexInput {
  client: LanternClient;
  key: string;
  signal?: AbortSignal;
}

export async function deleteVertexHandler(
  input: DeleteVertexInput,
  dispatch: (action: EditVertexAction) => void,
): Promise<void> {
  dispatch({ type: "DELETE_REQUESTED" });
  try {
    await deleteVertex(input.client, input.key, { signal: input.signal });
    dispatch({ type: "DELETE_SUCCEEDED" });
  } catch (err) {
    if (isAbortError(err)) return;
    dispatch({ type: "DELETE_FAILED", error: messageOf(err) });
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
