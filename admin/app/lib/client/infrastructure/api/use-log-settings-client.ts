import { useMemo } from "react";
import { useAuth } from "~/lib/client/usecase/auth/use-auth";
import { useConnection } from "~/lib/client/usecase/connection/connection-context";
import { createLogSettingsClient } from "./log-settings-client";

export function useLogSettingsClient() {
  const { state } = useAuth();
  const { connection } = useConnection();
  const signal = state.kind === "ready" ? state.signal : undefined;
  const csrf = state.kind === "ready" ? state.principal.csrfToken : undefined;
  return useMemo(
    () =>
      signal && csrf
        ? createLogSettingsClient(connection.baseUrl, signal, csrf)
        : null,
    [connection.baseUrl, signal, csrf],
  );
}
