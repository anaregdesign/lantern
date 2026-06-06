import type { ServerStatus } from "~/lib/client/infrastructure/api/get-server-status";
import type { ReplicationStatus } from "~/lib/client/infrastructure/api/get-replication-status";

/**
 * OpsStatus is the lifecycle phase of a polling card. "idle" is the
 * pre-first-fetch state; "ready" means we have a payload to display;
 * "error" surfaces the last LanternApiError message. "loading" is
 * only set on the first fetch — subsequent revalidates stay in
 * "ready" so the card UI does not flash while polling.
 */
export type OpsStatus = "idle" | "loading" | "ready" | "error";

/**
 * OpsState is the aggregate the Ops page reducer manages. Both cards
 * (server + replication) are independent — one card erroring does not
 * tear down the other. fetchEpoch is bumped on every manual refresh so
 * stale handlers can discard their result (mirrors the
 * browse-vertices / illuminate pattern).
 */
export interface OpsState {
  server: {
    status: OpsStatus;
    data: ServerStatus | null;
    error: string | null;
  };
  replication: {
    status: OpsStatus;
    data: ReplicationStatus | null;
    error: string | null;
  };
  /**
   * Auto-poll cadence in milliseconds. 0 disables polling (manual
   * refresh only). Default 5000 ms matches the Ops issue spec.
   */
  pollMs: number;
  fetchEpoch: number;
}

export const DEFAULT_POLL_MS = 5_000;

export const INITIAL_OPS_STATE: OpsState = {
  server: { status: "idle", data: null, error: null },
  replication: { status: "idle", data: null, error: null },
  pollMs: DEFAULT_POLL_MS,
  fetchEpoch: 0,
};
