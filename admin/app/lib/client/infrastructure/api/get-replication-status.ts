import { ReplicationPeer_State } from "~/lib/api/gen/graph/v1/graph_pb";
import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";

/**
 * ReplicationPeerState is the JSON-friendly state label the Ops view
 * displays as a pill colour. Mirrors graph.v1.ReplicationPeer.State but
 * collapses the proto enum to a lowercase string so React state stays
 * primitive.
 */
export type ReplicationPeerState =
  | "unspecified"
  | "connecting"
  | "streaming"
  | "backoff"
  | "closed";

/**
 * ReplicationPeerRow is the per-peer row the table renders. address
 * doubles as the React list key.
 */
export interface ReplicationPeerRow {
  address: string;
  state: ReplicationPeerState;
  /** epoch milliseconds; 0 when last_event_at was unset. */
  lastEventAtMs: number;
  /** how many ms have elapsed since lastEventAtMs, server-relative. */
  stalenessMs: number;
  appliedSeq: number;
  /** non-empty when the last per-peer session loop hit a non-recoverable error. */
  error: string;
}

/**
 * ReplicationStatus is the JSON-flat view of GetReplicationStatusResponse.
 * Single-instance deployments (LANTERN_PEERS unset) report enabled=false
 * with an empty peers slice; the Ops view should render an empty-state
 * card in that branch.
 */
export interface ReplicationStatus {
  nodeId: string;
  /** epoch milliseconds; 0 when local_now was unset. */
  localNowMs: number;
  enabled: boolean;
  peers: ReplicationPeerRow[];
}

/**
 * Calls LanternService.GetReplicationStatus and normalises the response
 * into the flat ReplicationStatus shape used by the Ops view.
 */
export async function getReplicationStatus(
  client: LanternClient,
  init?: { signal?: AbortSignal },
): Promise<ReplicationStatus> {
  try {
    const resp = await client.getReplicationStatus(
      {},
      { signal: init?.signal },
    );
    const localNowMs = resp.localNow
      ? Number(resp.localNow.seconds) * 1000 +
        Math.floor(resp.localNow.nanos / 1_000_000)
      : 0;
    const peers: ReplicationPeerRow[] = resp.peers
      .map((p) => {
        const lastEventAtMs = p.lastEventAt
          ? Number(p.lastEventAt.seconds) * 1000 +
            Math.floor(p.lastEventAt.nanos / 1_000_000)
          : 0;
        return {
          address: p.address,
          state: stateLabel(p.state),
          lastEventAtMs,
          stalenessMs:
            lastEventAtMs > 0 && localNowMs > 0
              ? Math.max(0, localNowMs - lastEventAtMs)
              : 0,
          appliedSeq: Number(p.appliedSeq),
          error: p.error,
        };
      })
      // The wire-level slice ordering is intentionally unspecified
      // (per the proto comment). Sorting by address gives a stable
      // visual ordering across re-fetches.
      .sort((a, b) => a.address.localeCompare(b.address));
    return {
      nodeId: resp.nodeId,
      localNowMs,
      enabled: resp.enabled,
      peers,
    };
  } catch (err) {
    throw LanternApiError.fromUnknown("GetReplicationStatus", err);
  }
}

function stateLabel(s: ReplicationPeer_State): ReplicationPeerState {
  switch (s) {
    case ReplicationPeer_State.CONNECTING:
      return "connecting";
    case ReplicationPeer_State.STREAMING:
      return "streaming";
    case ReplicationPeer_State.BACKOFF:
      return "backoff";
    case ReplicationPeer_State.CLOSED:
      return "closed";
    default:
      return "unspecified";
  }
}
