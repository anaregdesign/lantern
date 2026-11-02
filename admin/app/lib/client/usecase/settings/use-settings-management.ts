import { useEffect, useMemo, useSyncExternalStore } from "react";
import {
  SettingsController,
  settingsView,
  type LogSettingsPort,
  type SettingsState,
} from "./settings-management";
import type { SecurityChangeRecovery } from "../security/security-change-recovery";

const empty: SettingsState = {
  phase: "loading",
  message: "",
  mutation: "idle",
  draft: 0,
  stale: false,
  originalId: "",
};
const subscribeEmpty = () => () => {};
const snapshotEmpty = () => empty;

export function useSettingsManagement(
  port: LogSettingsPort | null,
  signal: AbortSignal | undefined,
  recovery: SecurityChangeRecovery,
  owner: string,
  canMutate: boolean,
) {
  const controller = useMemo(
    () =>
      port && signal
        ? new SettingsController(port, signal, recovery, owner, canMutate)
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
  return { controller, state, view: settingsView(state, canMutate) };
}
