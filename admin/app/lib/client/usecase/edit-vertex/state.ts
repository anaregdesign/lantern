import type { Vertex } from "~/lib/client/infrastructure/api/get-vertex";
import {
  INITIAL_TTL_INPUT,
  INITIAL_VERTEX_INPUTS,
  type TtlInput,
  type VertexInputs,
  type VertexValueKind,
} from "./value-codec";

export type EditVertexLoadStatus =
  | "idle"
  | "loading"
  | "ready"
  | "not-found"
  | "error";

export type EditVertexMode = "view" | "edit";

export type EditVertexSaveStatus = "idle" | "saving" | "saved" | "error";

export type EditVertexDeleteStatus =
  | "idle"
  | "deleting"
  | "deleted"
  | "error";

/**
 * State for the single-vertex CRUD screen. The `loadEpoch` is bumped
 * whenever the `key` changes so stale async loads can be discarded
 * (mirrors the browse-vertices reducer pattern).
 */
export interface EditVertexState {
  key: string;
  loadEpoch: number;
  loadStatus: EditVertexLoadStatus;
  loadError: string | null;
  vertex: Vertex | null;

  mode: EditVertexMode;
  kind: VertexValueKind;
  inputs: VertexInputs;
  ttl: TtlInput;

  saveStatus: EditVertexSaveStatus;
  saveError: string | null;

  deleteStatus: EditVertexDeleteStatus;
  deleteError: string | null;
  /** True after the user confirms the Dialog. */
  deleteRequested: boolean;
}

export const INITIAL_EDIT_VERTEX_STATE: EditVertexState = {
  key: "",
  loadEpoch: 0,
  loadStatus: "idle",
  loadError: null,
  vertex: null,
  mode: "view",
  kind: "string",
  inputs: INITIAL_VERTEX_INPUTS,
  ttl: INITIAL_TTL_INPUT,
  saveStatus: "idle",
  saveError: null,
  deleteStatus: "idle",
  deleteError: null,
  deleteRequested: false,
};
