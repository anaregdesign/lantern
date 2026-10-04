import { useEffect, useMemo, useSyncExternalStore } from "react";
import {
  SecurityManagementController,
  type SecurityManagementPort,
  type SecuritySection,
  type SecurityChangeRecovery,
} from "./security-management";

export function useSecurityManagement(
  port: SecurityManagementPort,
  section: SecuritySection,
  signal: AbortSignal,
  recovery?: SecurityChangeRecovery,
  owner = "",
) {
  const controller = useMemo(
    () =>
      new SecurityManagementController(port, section, signal, recovery, owner),
    [port, section, signal, recovery, owner],
  );
  const state = useSyncExternalStore(
    controller.subscribe,
    controller.getSnapshot,
    controller.getSnapshot,
  );
  useEffect(() => {
    void controller.load();
    return () => controller.dispose();
  }, [controller]);
  return { controller, state };
}
