import { useMemo } from "react";
import { createLanternClient, type LanternClient } from "./lantern-client";
import { useConnection } from "~/lib/client/usecase/connection/connection-context";
import { useAuth } from "~/lib/client/usecase/auth/use-auth";

/**
 * React-facing factory that returns a memoised Lantern client bound to the
 * currently active connection. Components and use-case hooks should depend on
 * this rather than constructing a client themselves.
 */
export function useLanternClient(): LanternClient {
  const { connection } = useConnection();
  const { state } = useAuth();
  const csrf = state.kind === "ready" ? state.principal.csrfToken : undefined;
  const signal =
    state.kind === "ready" || state.kind === "off" ? state.signal : undefined;
  return useMemo(() => {
    if (!signal)
      throw new Error(
        "Data client requires current authentication capabilities",
      );
    return createLanternClient({
      baseUrl: connection.baseUrl + (csrf ? "/browser" : ""),
      csrf,
      signal,
    });
  }, [connection.baseUrl, csrf, signal]);
}
