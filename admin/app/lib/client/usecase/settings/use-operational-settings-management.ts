import { useEffect, useMemo, useSyncExternalStore } from "react";
import type { SecurityChangeRecovery } from "../security/security-change-recovery";
import {
  OperationalSettingsController,
  operationalBlocked,
  type OperationalSettingsPort,
  type OperationalSettingsState,
} from "./operational-settings-management";
const empty: OperationalSettingsState = {
  phase: "loading",
  message: "",
  mutation: "idle",
  retentionAcknowledged: false,
  stale: false,
  originalId: "",
};
const subscribeEmpty = () => () => {},
  snapshotEmpty = () => empty;
export function useOperationalSettingsManagement(
  port: OperationalSettingsPort | null,
  signal: AbortSignal | undefined,
  recovery: SecurityChangeRecovery,
  owner: string,
  canMutate: boolean,
) {
  const controller = useMemo(
    () =>
      port && signal
        ? new OperationalSettingsController(
            port,
            signal,
            recovery,
            owner,
            canMutate,
          )
        : null,
    [port, signal, recovery, owner, canMutate],
  );
  const state = useSyncExternalStore(
    controller?.subscribe ?? subscribeEmpty,
    controller?.getSnapshot ?? snapshotEmpty,
    controller?.getSnapshot ?? snapshotEmpty,
  );
  useEffect(() => {
    if (!controller) return;
    void controller.load();
    return () => controller.dispose();
  }, [controller]);
  return { controller, state, blocked: operationalBlocked(state, canMutate) };
}
