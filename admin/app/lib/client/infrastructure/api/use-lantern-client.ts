import { useMemo } from "react";
import { createLanternClient, type LanternClient } from "./lantern-client";
import { useConnection } from "~/lib/client/usecase/connection/connection-context";

/**
 * React-facing factory that returns a memoised Lantern client bound to the
 * currently active connection. Components and use-case hooks should depend on
 * this rather than constructing a client themselves.
 */
export function useLanternClient(): LanternClient {
  const { connection } = useConnection();
  return useMemo(
    () =>
      createLanternClient({
        baseUrl: connection.baseUrl,
        token: connection.token,
      }),
    [connection.baseUrl, connection.token],
  );
}
