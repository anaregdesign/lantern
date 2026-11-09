import { useEffect, useMemo, useSyncExternalStore } from "react";
import {
  SecurityManagementController,
  type SecurityManagementPort,
  type SecuritySection,
  type SecurityChangeRecovery,
} from "./security-management";
import type { SecurityDraftRecovery } from "./security-draft-recovery";

export function useSecurityManagement(
  port: SecurityManagementPort,
  section: SecuritySection,
  signal: AbortSignal,
  recovery?: SecurityChangeRecovery,
  owner = "",
  drafts?: SecurityDraftRecovery,
  draftOwner?: object,
) {
  const controller = useMemo(
    () =>
      new SecurityManagementController(
        port,
        section,
        signal,
        recovery,
        owner,
        drafts,
        draftOwner,
      ),
    [port, section, signal, recovery, owner, drafts, draftOwner],
  );
  const state = useSyncExternalStore(
    controller.subscribe,
    controller.getSnapshot,
    controller.getSnapshot,
  );
  useEffect(() => {
    void controller.initialize();
    return () => controller.dispose();
  }, [controller]);
  return { controller, state };
}
