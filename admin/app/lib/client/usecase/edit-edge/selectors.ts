import type { EditEdgeState } from "./state";
import {
  buildAddEdgeBody,
  buildPutEdgeBody,
  type BuildEdgeBodyResult,
} from "./edge-codec";
import type { AddEdgeBody } from "~/lib/client/infrastructure/api/add-edge";
import type { PutEdgeBody } from "~/lib/client/infrastructure/api/put-edge";

export function selectAddBody(
  state: EditEdgeState,
  now: number = Date.now(),
): BuildEdgeBodyResult<AddEdgeBody> {
  return buildAddEdgeBody(state.addInputs, now);
}

export function selectPutBody(
  state: EditEdgeState,
  now: number = Date.now(),
): BuildEdgeBodyResult<PutEdgeBody> {
  return buildPutEdgeBody(state.putInputs, now);
}

export function selectAddValid(
  state: EditEdgeState,
  now: number = Date.now(),
): boolean {
  return selectAddBody(state, now).body !== null;
}

export function selectPutValid(
  state: EditEdgeState,
  now: number = Date.now(),
): boolean {
  return selectPutBody(state, now).body !== null;
}
