import { useCallback, useEffect, useMemo, useReducer, useRef } from "react";
import { useLanternClient } from "~/lib/client/infrastructure/api/use-lantern-client";
import {
  addEdgeHandler,
  deleteEdgeHandler,
  loadEdge,
  putEdgeHandler,
} from "./handlers";
import { editEdgeReducer } from "./reducer";
import { INITIAL_EDIT_EDGE_STATE, type EditEdgeState } from "./state";
import type { EdgeWriteMode } from "./edge-codec";
import {
  selectAddBody,
  selectAddValid,
  selectPutBody,
  selectPutValid,
} from "./selectors";
import type { TtlInput } from "../edit-vertex/value-codec";

export interface UseEditEdgeResult {
  state: EditEdgeState;
  addValid: boolean;
  putValid: boolean;
  deleted: boolean;
  setWeight: (mode: EdgeWriteMode, value: string) => void;
  setTtl: (mode: EdgeWriteMode, ttl: TtlInput) => void;
  submitAdd: () => Promise<void>;
  submitPut: () => Promise<void>;
  openDeleteDialog: () => void;
  closeDeleteDialog: () => void;
  confirmDelete: () => Promise<void>;
  reload: () => void;
}

export function useEditEdge(tail: string, head: string): UseEditEdgeResult {
  const client = useLanternClient();
  const [state, dispatch] = useReducer(
    editEdgeReducer,
    INITIAL_EDIT_EDGE_STATE,
  );

  useEffect(() => {
    dispatch({ type: "TARGET_CHANGED", tail, head });
  }, [tail, head]);

  const lastEpochRef = useRef<number>(-1);
  useEffect(() => {
    if (state.tail === "" || state.head === "") return;
    if (state.loadEpoch === lastEpochRef.current) return;
    lastEpochRef.current = state.loadEpoch;
    const controller = new AbortController();
    void loadEdge(
      {
        client,
        tail: state.tail,
        head: state.head,
        epoch: state.loadEpoch,
        signal: controller.signal,
      },
      dispatch,
    );
    return () => controller.abort();
  }, [client, state.tail, state.head, state.loadEpoch]);

  const setWeight = useCallback((mode: EdgeWriteMode, value: string) => {
    dispatch({ type: "WEIGHT_CHANGED", mode, value });
  }, []);
  const setTtl = useCallback((mode: EdgeWriteMode, ttl: TtlInput) => {
    dispatch({ type: "TTL_CHANGED", mode, ttl });
  }, []);

  const submitAdd = useCallback(async () => {
    const { body, error } = selectAddBody(state);
    if (!body) {
      if (error) {
        dispatch({ type: "WRITE_FAILED", mode: "add", error });
      }
      return;
    }
    await addEdgeHandler(
      { client, tail: state.tail, head: state.head, body },
      dispatch,
    );
  }, [client, state]);

  const submitPut = useCallback(async () => {
    const { body, error } = selectPutBody(state);
    if (!body) {
      if (error) {
        dispatch({ type: "WRITE_FAILED", mode: "put", error });
      }
      return;
    }
    await putEdgeHandler(
      { client, tail: state.tail, head: state.head, body },
      dispatch,
    );
  }, [client, state]);

  const openDeleteDialog = useCallback(() => {
    dispatch({ type: "DELETE_OPENED" });
  }, []);
  const closeDeleteDialog = useCallback(() => {
    dispatch({ type: "DELETE_CANCELED" });
  }, []);
  const confirmDelete = useCallback(async () => {
    await deleteEdgeHandler(
      { client, tail: state.tail, head: state.head },
      dispatch,
    );
  }, [client, state.tail, state.head]);

  const reload = useCallback(() => {
    dispatch({ type: "TARGET_CHANGED", tail, head });
  }, [tail, head]);

  const addValid = useMemo(() => selectAddValid(state), [state]);
  const putValid = useMemo(() => selectPutValid(state), [state]);
  const deleted = state.deleteStatus === "deleted";

  return useMemo(
    () => ({
      state,
      addValid,
      putValid,
      deleted,
      setWeight,
      setTtl,
      submitAdd,
      submitPut,
      openDeleteDialog,
      closeDeleteDialog,
      confirmDelete,
      reload,
    }),
    [
      state,
      addValid,
      putValid,
      deleted,
      setWeight,
      setTtl,
      submitAdd,
      submitPut,
      openDeleteDialog,
      closeDeleteDialog,
      confirmDelete,
      reload,
    ],
  );
}
