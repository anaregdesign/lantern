import { addEdge } from "~/lib/client/infrastructure/api/add-edge";
import type { AddEdgeBody } from "~/lib/client/infrastructure/api/add-edge";
import { deleteEdge } from "~/lib/client/infrastructure/api/delete-edge";
import { LanternApiError } from "~/lib/client/infrastructure/api/error";
import { getEdge } from "~/lib/client/infrastructure/api/get-edge";
import type { LanternClient } from "~/lib/client/infrastructure/api/lantern-client";
import { putEdge } from "~/lib/client/infrastructure/api/put-edge";
import type { PutEdgeBody } from "~/lib/client/infrastructure/api/put-edge";
import type { EditEdgeAction } from "./reducer";

export interface LoadEdgeInput {
  client: LanternClient;
  tail: string;
  head: string;
  epoch: number;
  signal?: AbortSignal;
}

export async function loadEdge(
  input: LoadEdgeInput,
  dispatch: (action: EditEdgeAction) => void,
): Promise<void> {
  dispatch({ type: "LOAD_REQUESTED", epoch: input.epoch });
  try {
    const edge = await getEdge(input.client, input.tail, input.head, {
      signal: input.signal,
    });
    dispatch({ type: "LOAD_RECEIVED", epoch: input.epoch, edge });
  } catch (err) {
    if (isAbortError(err)) return;
    dispatch({
      type: "LOAD_FAILED",
      epoch: input.epoch,
      error: messageOf(err),
    });
  }
}

export interface AddEdgeInput {
  client: LanternClient;
  tail: string;
  head: string;
  body: AddEdgeBody;
  signal?: AbortSignal;
}

export async function addEdgeHandler(
  input: AddEdgeInput,
  dispatch: (action: EditEdgeAction) => void,
): Promise<void> {
  dispatch({ type: "WRITE_REQUESTED", mode: "add" });
  try {
    const reply = await addEdge(
      input.client,
      input.tail,
      input.head,
      input.body,
      {
        signal: input.signal,
      },
    );
    if (reply.kind === "acceptedUndisclosed") {
      dispatch({ type: "WRITE_ACCEPTED_UNDISCLOSED", mode: "add" });
      return;
    }
    const fresh = await getEdge(input.client, input.tail, input.head, {
      signal: input.signal,
    });
    dispatch({ type: "WRITE_SUCCEEDED", mode: "add", edge: fresh });
  } catch (err) {
    if (isAbortError(err)) return;
    dispatch({ type: "WRITE_FAILED", mode: "add", error: messageOf(err) });
  }
}

export interface PutEdgeInput {
  client: LanternClient;
  tail: string;
  head: string;
  body: PutEdgeBody;
  signal?: AbortSignal;
}

export async function putEdgeHandler(
  input: PutEdgeInput,
  dispatch: (action: EditEdgeAction) => void,
): Promise<void> {
  dispatch({ type: "WRITE_REQUESTED", mode: "put" });
  try {
    const reply = await putEdge(
      input.client,
      input.tail,
      input.head,
      input.body,
      {
        signal: input.signal,
      },
    );
    if (reply.kind === "acceptedUndisclosed") {
      dispatch({ type: "WRITE_ACCEPTED_UNDISCLOSED", mode: "put" });
      return;
    }
    const fresh = await getEdge(input.client, input.tail, input.head, {
      signal: input.signal,
    });
    dispatch({ type: "WRITE_SUCCEEDED", mode: "put", edge: fresh });
  } catch (err) {
    if (isAbortError(err)) return;
    dispatch({ type: "WRITE_FAILED", mode: "put", error: messageOf(err) });
  }
}

export interface DeleteEdgeInput {
  client: LanternClient;
  tail: string;
  head: string;
  signal?: AbortSignal;
}

export async function deleteEdgeHandler(
  input: DeleteEdgeInput,
  dispatch: (action: EditEdgeAction) => void,
): Promise<void> {
  dispatch({ type: "DELETE_REQUESTED" });
  try {
    const reply = await deleteEdge(input.client, input.tail, input.head, {
      signal: input.signal,
    });
    dispatch({
      type:
        reply.kind === "acceptedUndisclosed"
          ? "DELETE_ACCEPTED_UNDISCLOSED"
          : "DELETE_SUCCEEDED",
    });
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
