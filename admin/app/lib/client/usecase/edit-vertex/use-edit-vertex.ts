import { useCallback, useEffect, useMemo, useReducer, useRef } from "react";
import { useLanternClient } from "~/lib/client/infrastructure/api/use-lantern-client";
import { deleteVertexHandler, loadVertex, saveVertex } from "./handlers";
import { editVertexReducer } from "./reducer";
import { INITIAL_EDIT_VERTEX_STATE, type EditVertexState } from "./state";
import {
  selectDeleted,
  selectEditing,
  selectFormValid,
  selectPutVertexBody,
} from "./selectors";
import type {
  BytesEncoding,
  TtlInput,
  VertexInputs,
  VertexValueKind,
} from "./value-codec";

export interface UseEditVertexResult {
  state: EditVertexState;
  formValid: boolean;
  editing: boolean;
  deleted: boolean;
  beginEdit: () => void;
  cancelEdit: () => void;
  setKind: (kind: VertexValueKind) => void;
  setInput: (field: keyof VertexInputs, value: string) => void;
  setBool: (value: boolean) => void;
  setBytesEncoding: (value: BytesEncoding) => void;
  setTtl: (ttl: TtlInput) => void;
  save: () => Promise<void>;
  openDeleteDialog: () => void;
  closeDeleteDialog: () => void;
  confirmDelete: () => Promise<void>;
  reload: () => void;
}

/**
 * Owns the load → edit → save / delete cycle for a single vertex. The
 * `key` is the URL param, so the hook resets fully whenever the route
 * navigates to a different vertex.
 */
export function useEditVertex(key: string): UseEditVertexResult {
  const client = useLanternClient();
  const [state, dispatch] = useReducer(
    editVertexReducer,
    INITIAL_EDIT_VERTEX_STATE,
  );

  // Re-seed state when the URL key changes (incl. on first mount).
  useEffect(() => {
    dispatch({ type: "KEY_CHANGED", key });
  }, [key]);

  // Fire the load whenever the epoch is bumped.
  const lastEpochRef = useRef<number>(-1);
  useEffect(() => {
    if (state.key === "" || state.loadEpoch === lastEpochRef.current) {
      return;
    }
    lastEpochRef.current = state.loadEpoch;
    const controller = new AbortController();
    void loadVertex(
      {
        client,
        key: state.key,
        epoch: state.loadEpoch,
        signal: controller.signal,
      },
      dispatch,
    );
    return () => controller.abort();
  }, [client, state.key, state.loadEpoch]);

  const beginEdit = useCallback(() => {
    dispatch({ type: "EDIT_BEGUN" });
  }, []);
  const cancelEdit = useCallback(() => {
    dispatch({ type: "EDIT_CANCELED" });
  }, []);
  const setKind = useCallback((kind: VertexValueKind) => {
    dispatch({ type: "KIND_CHANGED", kind });
  }, []);
  const setInput = useCallback((field: keyof VertexInputs, value: string) => {
    dispatch({ type: "INPUT_CHANGED", field, value });
  }, []);
  const setBool = useCallback((value: boolean) => {
    dispatch({ type: "BOOL_INPUT_CHANGED", value });
  }, []);
  const setBytesEncoding = useCallback((value: BytesEncoding) => {
    dispatch({ type: "BYTES_ENCODING_CHANGED", value });
  }, []);
  const setTtl = useCallback((ttl: TtlInput) => {
    dispatch({ type: "TTL_CHANGED", ttl });
  }, []);

  const save = useCallback(async () => {
    const { body, error } = selectPutVertexBody(state);
    if (!body) {
      if (error) {
        dispatch({ type: "SAVE_FAILED", error });
      }
      return;
    }
    await saveVertex({ client, key: state.key, body }, dispatch);
  }, [client, state]);

  const openDeleteDialog = useCallback(() => {
    dispatch({ type: "DELETE_OPENED" });
  }, []);
  const closeDeleteDialog = useCallback(() => {
    dispatch({ type: "DELETE_CANCELED" });
  }, []);
  const confirmDelete = useCallback(async () => {
    await deleteVertexHandler({ client, key: state.key }, dispatch);
  }, [client, state.key]);

  const reload = useCallback(() => {
    dispatch({ type: "KEY_CHANGED", key });
  }, [key]);

  const formValid = useMemo(() => selectFormValid(state), [state]);
  const editing = selectEditing(state);
  const deleted = selectDeleted(state);

  return useMemo(
    () => ({
      state,
      formValid,
      editing,
      deleted,
      beginEdit,
      cancelEdit,
      setKind,
      setInput,
      setBool,
      setBytesEncoding,
      setTtl,
      save,
      openDeleteDialog,
      closeDeleteDialog,
      confirmDelete,
      reload,
    }),
    [
      state,
      formValid,
      editing,
      deleted,
      beginEdit,
      cancelEdit,
      setKind,
      setInput,
      setBool,
      setBytesEncoding,
      setTtl,
      save,
      openDeleteDialog,
      closeDeleteDialog,
      confirmDelete,
      reload,
    ],
  );
}
