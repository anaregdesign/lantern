import type { ServerStatus } from "~/lib/client/infrastructure/api/get-server-status";
import type { ReplicationStatus } from "~/lib/client/infrastructure/api/get-replication-status";
import { type OpsState, INITIAL_OPS_STATE } from "./state";

/**
 * OpsAction is the union of every state transition the Ops reducer
 * understands. Each transition is one-way so undo/redo is not
 * required; the reducer is therefore a flat switch with no extra
 * machinery.
 */
export type OpsAction =
  | { type: "FETCH_STARTED"; epoch: number }
  | { type: "SERVER_LOADED"; epoch: number; data: ServerStatus }
  | { type: "SERVER_ERROR"; epoch: number; error: string }
  | { type: "REPLICATION_LOADED"; epoch: number; data: ReplicationStatus }
  | { type: "REPLICATION_ERROR"; epoch: number; error: string }
  | { type: "SET_POLL_MS"; pollMs: number }
  | { type: "RESET" };

/**
 * opsReducer applies an OpsAction. fetchEpoch gating: handlers
 * dispatch with the epoch they observed when they fired. Any newer
 * epoch means the user kicked off a fresh refresh while the previous
 * fetch was in flight; the stale result is discarded so the card
 * never reverts to old data.
 */
export function opsReducer(state: OpsState, action: OpsAction): OpsState {
  switch (action.type) {
    case "FETCH_STARTED": {
      const isFirstFetch =
        state.server.status === "idle" && state.replication.status === "idle";
      return {
        ...state,
        fetchEpoch: action.epoch,
        server: isFirstFetch
          ? { ...state.server, status: "loading" }
          : state.server,
        replication: isFirstFetch
          ? { ...state.replication, status: "loading" }
          : state.replication,
      };
    }
    case "SERVER_LOADED":
      if (action.epoch !== state.fetchEpoch) return state;
      return {
        ...state,
        server: { status: "ready", data: action.data, error: null },
      };
    case "SERVER_ERROR":
      if (action.epoch !== state.fetchEpoch) return state;
      return {
        ...state,
        server: { ...state.server, status: "error", error: action.error },
      };
    case "REPLICATION_LOADED":
      if (action.epoch !== state.fetchEpoch) return state;
      return {
        ...state,
        replication: { status: "ready", data: action.data, error: null },
      };
    case "REPLICATION_ERROR":
      if (action.epoch !== state.fetchEpoch) return state;
      return {
        ...state,
        replication: {
          ...state.replication,
          status: "error",
          error: action.error,
        },
      };
    case "SET_POLL_MS":
      return { ...state, pollMs: Math.max(0, action.pollMs) };
    case "RESET":
      return INITIAL_OPS_STATE;
  }
}
