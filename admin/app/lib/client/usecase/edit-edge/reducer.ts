import type { Edge } from "~/lib/client/infrastructure/api/get-edge";
import { INITIAL_EDIT_EDGE_STATE, type EditEdgeState } from "./state";
import {
  INITIAL_EDGE_WRITE_INPUTS,
  inputsFromEdge,
  type EdgeWriteInputs,
  type EdgeWriteMode,
} from "./edge-codec";
import type { TtlInput } from "../edit-vertex/value-codec";

export type EditEdgeAction =
  | { type: "TARGET_CHANGED"; tail: string; head: string }
  | { type: "LOAD_REQUESTED"; epoch: number }
  | { type: "LOAD_RECEIVED"; epoch: number; edge: Edge | null }
  | { type: "LOAD_FAILED"; epoch: number; error: string }
  | { type: "WEIGHT_CHANGED"; mode: EdgeWriteMode; value: string }
  | { type: "TTL_CHANGED"; mode: EdgeWriteMode; ttl: TtlInput }
  | { type: "WRITE_REQUESTED"; mode: EdgeWriteMode }
  | { type: "WRITE_SUCCEEDED"; mode: EdgeWriteMode; edge: Edge | null }
  | { type: "WRITE_FAILED"; mode: EdgeWriteMode; error: string }
  | { type: "DELETE_OPENED" }
  | { type: "DELETE_CANCELED" }
  | { type: "DELETE_REQUESTED" }
  | { type: "DELETE_SUCCEEDED" }
  | { type: "DELETE_FAILED"; error: string }
  | { type: "RESET" };

function applyInputUpdate(
  state: EditEdgeState,
  mode: EdgeWriteMode,
  update: (prev: EdgeWriteInputs) => EdgeWriteInputs,
): EditEdgeState {
  if (mode === "add") {
    return { ...state, addInputs: update(state.addInputs) };
  }
  return { ...state, putInputs: update(state.putInputs) };
}

export function editEdgeReducer(
  state: EditEdgeState,
  action: EditEdgeAction,
): EditEdgeState {
  switch (action.type) {
    case "TARGET_CHANGED": {
      if (
        action.tail === state.tail &&
        action.head === state.head &&
        state.loadStatus !== "idle"
      ) {
        return state;
      }
      return {
        ...INITIAL_EDIT_EDGE_STATE,
        tail: action.tail,
        head: action.head,
        loadEpoch: state.loadEpoch + 1,
      };
    }
    case "LOAD_REQUESTED": {
      if (action.epoch !== state.loadEpoch) return state;
      return {
        ...state,
        loadStatus: "loading",
        loadError: null,
        edge: null,
        addStatus: "idle",
        addError: null,
        putStatus: "idle",
        putError: null,
      };
    }
    case "LOAD_RECEIVED": {
      if (action.epoch !== state.loadEpoch) return state;
      const seeded = inputsFromEdge(action.edge);
      return {
        ...state,
        loadStatus: action.edge ? "ready" : "not-found",
        edge: action.edge,
        addInputs: INITIAL_EDGE_WRITE_INPUTS,
        putInputs: seeded,
      };
    }
    case "LOAD_FAILED": {
      if (action.epoch !== state.loadEpoch) return state;
      return { ...state, loadStatus: "error", loadError: action.error };
    }
    case "WEIGHT_CHANGED": {
      return applyInputUpdate(state, action.mode, (prev) => ({
        ...prev,
        weight: action.value,
      }));
    }
    case "TTL_CHANGED": {
      return applyInputUpdate(state, action.mode, (prev) => ({
        ...prev,
        ttl: action.ttl,
      }));
    }
    case "WRITE_REQUESTED": {
      if (action.mode === "add") {
        return { ...state, addStatus: "saving", addError: null };
      }
      return { ...state, putStatus: "saving", putError: null };
    }
    case "WRITE_SUCCEEDED": {
      const next: EditEdgeState = {
        ...state,
        edge: action.edge ?? state.edge,
        loadStatus: action.edge ? "ready" : state.loadStatus,
      };
      if (action.mode === "add") {
        next.addStatus = "saved";
        next.addInputs = INITIAL_EDGE_WRITE_INPUTS;
      } else {
        next.putStatus = "saved";
        next.putInputs = inputsFromEdge(action.edge);
      }
      return next;
    }
    case "WRITE_FAILED": {
      if (action.mode === "add") {
        return { ...state, addStatus: "error", addError: action.error };
      }
      return { ...state, putStatus: "error", putError: action.error };
    }
    case "DELETE_OPENED": {
      return { ...state, deleteRequested: true, deleteError: null };
    }
    case "DELETE_CANCELED": {
      return { ...state, deleteRequested: false };
    }
    case "DELETE_REQUESTED": {
      return { ...state, deleteStatus: "deleting", deleteError: null };
    }
    case "DELETE_SUCCEEDED": {
      return {
        ...state,
        deleteStatus: "deleted",
        deleteRequested: false,
        edge: null,
        loadStatus: "not-found",
      };
    }
    case "DELETE_FAILED": {
      return {
        ...state,
        deleteStatus: "error",
        deleteError: action.error,
        deleteRequested: false,
      };
    }
    case "RESET":
      return INITIAL_EDIT_EDGE_STATE;
  }
}
