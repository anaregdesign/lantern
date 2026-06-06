import type { Vertex } from "~/lib/client/infrastructure/api/get-vertex";
import { INITIAL_EDIT_VERTEX_STATE, type EditVertexState } from "./state";
import {
  INITIAL_TTL_INPUT,
  INITIAL_VERTEX_INPUTS,
  inputsFromVertex,
  kindOfVertex,
  type BytesEncoding,
  type TtlInput,
  type VertexInputs,
  type VertexValueKind,
} from "./value-codec";

export type EditVertexAction =
  | { type: "KEY_CHANGED"; key: string }
  | { type: "LOAD_REQUESTED"; epoch: number }
  | { type: "LOAD_RECEIVED"; epoch: number; vertex: Vertex | null }
  | { type: "LOAD_FAILED"; epoch: number; error: string }
  | { type: "EDIT_BEGUN" }
  | { type: "EDIT_CANCELED" }
  | { type: "KIND_CHANGED"; kind: VertexValueKind }
  | { type: "INPUT_CHANGED"; field: keyof VertexInputs; value: string }
  | { type: "BOOL_INPUT_CHANGED"; value: boolean }
  | { type: "BYTES_ENCODING_CHANGED"; value: BytesEncoding }
  | { type: "TTL_CHANGED"; ttl: TtlInput }
  | { type: "SAVE_REQUESTED" }
  | { type: "SAVE_SUCCEEDED"; vertex: Vertex }
  | { type: "SAVE_FAILED"; error: string }
  | { type: "DELETE_OPENED" }
  | { type: "DELETE_CANCELED" }
  | { type: "DELETE_REQUESTED" }
  | { type: "DELETE_SUCCEEDED" }
  | { type: "DELETE_FAILED"; error: string }
  | { type: "RESET" };

export function editVertexReducer(
  state: EditVertexState,
  action: EditVertexAction,
): EditVertexState {
  switch (action.type) {
    case "KEY_CHANGED": {
      if (action.key === state.key && state.loadStatus !== "idle") {
        return state;
      }
      return {
        ...INITIAL_EDIT_VERTEX_STATE,
        key: action.key,
        loadEpoch: state.loadEpoch + 1,
      };
    }
    case "LOAD_REQUESTED": {
      if (action.epoch !== state.loadEpoch) return state;
      return {
        ...state,
        loadStatus: "loading",
        loadError: null,
        vertex: null,
        mode: "view",
        saveStatus: "idle",
        saveError: null,
      };
    }
    case "LOAD_RECEIVED": {
      if (action.epoch !== state.loadEpoch) return state;
      if (action.vertex === null) {
        return {
          ...state,
          loadStatus: "not-found",
          loadError: null,
          vertex: null,
          kind: "string",
          inputs: INITIAL_VERTEX_INPUTS,
          ttl: INITIAL_TTL_INPUT,
        };
      }
      const kind = kindOfVertex(action.vertex);
      return {
        ...state,
        loadStatus: "ready",
        loadError: null,
        vertex: action.vertex,
        kind,
        inputs: inputsFromVertex(action.vertex),
        ttl: INITIAL_TTL_INPUT,
        saveStatus: "idle",
        saveError: null,
      };
    }
    case "LOAD_FAILED": {
      if (action.epoch !== state.loadEpoch) return state;
      return {
        ...state,
        loadStatus: "error",
        loadError: action.error,
      };
    }
    case "EDIT_BEGUN": {
      if (state.loadStatus !== "ready" && state.loadStatus !== "not-found") {
        return state;
      }
      return {
        ...state,
        mode: "edit",
        saveStatus: "idle",
        saveError: null,
      };
    }
    case "EDIT_CANCELED": {
      // Restore inputs from the underlying vertex (or blanks if not-found).
      if (state.vertex) {
        return {
          ...state,
          mode: "view",
          kind: kindOfVertex(state.vertex),
          inputs: inputsFromVertex(state.vertex),
          ttl: INITIAL_TTL_INPUT,
          saveStatus: "idle",
          saveError: null,
        };
      }
      return {
        ...state,
        mode: "view",
        kind: "string",
        inputs: INITIAL_VERTEX_INPUTS,
        ttl: INITIAL_TTL_INPUT,
        saveStatus: "idle",
        saveError: null,
      };
    }
    case "KIND_CHANGED": {
      return { ...state, kind: action.kind };
    }
    case "INPUT_CHANGED": {
      return {
        ...state,
        inputs: { ...state.inputs, [action.field]: action.value },
      };
    }
    case "BOOL_INPUT_CHANGED": {
      return {
        ...state,
        inputs: { ...state.inputs, bool: action.value },
      };
    }
    case "BYTES_ENCODING_CHANGED": {
      return {
        ...state,
        inputs: { ...state.inputs, bytesEncoding: action.value },
      };
    }
    case "TTL_CHANGED": {
      return { ...state, ttl: action.ttl };
    }
    case "SAVE_REQUESTED": {
      return { ...state, saveStatus: "saving", saveError: null };
    }
    case "SAVE_SUCCEEDED": {
      const kind = kindOfVertex(action.vertex);
      return {
        ...state,
        vertex: action.vertex,
        kind,
        inputs: inputsFromVertex(action.vertex),
        ttl: INITIAL_TTL_INPUT,
        mode: "view",
        loadStatus: "ready",
        loadError: null,
        saveStatus: "saved",
        saveError: null,
      };
    }
    case "SAVE_FAILED": {
      return {
        ...state,
        saveStatus: "error",
        saveError: action.error,
      };
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
        vertex: null,
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
    case "RESET": {
      return INITIAL_EDIT_VERTEX_STATE;
    }
  }
}
