import type { Edge } from "~/lib/client/infrastructure/api/get-edge";
import {
  INITIAL_EDGE_WRITE_INPUTS,
  type EdgeWriteInputs,
} from "./edge-codec";

export type EditEdgeLoadStatus =
  | "idle"
  | "loading"
  | "ready"
  | "not-found"
  | "error";

export type EditEdgeWriteStatus = "idle" | "saving" | "saved" | "error";

export type EditEdgeDeleteStatus =
  | "idle"
  | "deleting"
  | "deleted"
  | "error";

/**
 * State for the single-edge CRUD screen. Holds two independent forms —
 * one for `AddEdge` (accumulating contributions) and one for `PutEdge`
 * (idempotent replacement) — so the user can author both side-by-side
 * without losing partial input.
 */
export interface EditEdgeState {
  tail: string;
  head: string;
  loadEpoch: number;
  loadStatus: EditEdgeLoadStatus;
  loadError: string | null;
  edge: Edge | null;

  addInputs: EdgeWriteInputs;
  putInputs: EdgeWriteInputs;

  addStatus: EditEdgeWriteStatus;
  addError: string | null;
  putStatus: EditEdgeWriteStatus;
  putError: string | null;

  deleteStatus: EditEdgeDeleteStatus;
  deleteError: string | null;
  deleteRequested: boolean;
}

export const INITIAL_EDIT_EDGE_STATE: EditEdgeState = {
  tail: "",
  head: "",
  loadEpoch: 0,
  loadStatus: "idle",
  loadError: null,
  edge: null,
  addInputs: INITIAL_EDGE_WRITE_INPUTS,
  putInputs: INITIAL_EDGE_WRITE_INPUTS,
  addStatus: "idle",
  addError: null,
  putStatus: "idle",
  putError: null,
  deleteStatus: "idle",
  deleteError: null,
  deleteRequested: false,
};
