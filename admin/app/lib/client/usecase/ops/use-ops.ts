import { useCallback, useEffect, useReducer, useRef, useState } from "react";

import { getServerStatus } from "~/lib/client/infrastructure/api/get-server-status";
import { getReplicationStatus } from "~/lib/client/infrastructure/api/get-replication-status";
import { useLanternClient } from "~/lib/client/infrastructure/api/use-lantern-client";
import { opsReducer } from "./reducer";
import { INITIAL_OPS_STATE } from "./state";
import { diagnosticsError } from "./diagnostics-error";
import { startPolling } from "./polling";

/**
 * useOps is the hook the OpsPage consumes. It owns:
 *   1. the polling timer (cleared on unmount + on pollMs change)
 *   2. the AbortController per fetch round (one round = one
 *      GetServerStatus + one GetReplicationStatus in parallel)
 *   3. the manual-refresh handler exposed to the toolbar
 *   4. the pollMs setter exposed to the toolbar
 *
 * Errors from either RPC are stored on their card; one failing card
 * does NOT cancel the other.
 */
export function useOps() {
  const client = useLanternClient();
  const [state, dispatch] = useReducer(opsReducer, INITIAL_OPS_STATE);
  // epochRef carries the latest dispatched epoch so the polling
  // closure does not depend on state (which would re-create the
  // polling lifecycle every render and reset the timer).
  const epochRef = useRef(0);
  const [refreshNonce, setRefreshNonce] = useState(0);

  const fetchRound = useCallback(
    async (signal: AbortSignal) => {
      const epoch = epochRef.current + 1;
      epochRef.current = epoch;
      dispatch({ type: "FETCH_STARTED", epoch });
      // Run both RPCs in parallel — neither blocks the other on
      // failure. The reducer stamps both results with the same
      // epoch so a stale round can be discarded atomically.
      await Promise.allSettled([
        getServerStatus(client, { signal })
          .then((data) => {
            if (!signal.aborted)
              dispatch({ type: "SERVER_LOADED", epoch, data });
          })
          .catch((err: unknown) => {
            if (signal.aborted || (err as Error)?.name === "AbortError") return;
            dispatch({
              type: "SERVER_ERROR",
              epoch,
              error: diagnosticsError(
                err,
                "Server status at the selected Gateway",
              ),
            });
          }),
        getReplicationStatus(client, { signal })
          .then((data) => {
            if (!signal.aborted)
              dispatch({ type: "REPLICATION_LOADED", epoch, data });
          })
          .catch((err: unknown) => {
            if (signal.aborted || (err as Error)?.name === "AbortError") return;
            dispatch({
              type: "REPLICATION_ERROR",
              epoch,
              error: diagnosticsError(
                err,
                "Replication status at the selected Gateway",
              ),
            });
          }),
      ]);
    },
    [client],
  );

  const refresh = useCallback(() => {
    setRefreshNonce((value) => value + 1);
  }, []);

  const setPollMs = useCallback((ms: number) => {
    dispatch({ type: "SET_POLL_MS", pollMs: ms });
  }, []);

  // First-paint fetch + polling timer. The cleanup aborts any
  // in-flight requests on unmount or before a re-fetch fires.
  useEffect(
    () => startPolling(fetchRound, state.pollMs),
    [fetchRound, state.pollMs, refreshNonce],
  );

  return {
    state,
    refresh,
    setPollMs,
  };
}
