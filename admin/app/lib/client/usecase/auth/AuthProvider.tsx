import {
  useEffect,
  useMemo,
  useSyncExternalStore,
  type ReactNode,
} from "react";
import { useConnection } from "~/lib/client/usecase/connection/connection-context";
import {
  AuthController,
  SessionRevocationRecovery,
  type AuthGateway,
} from "./auth-state";
import { SecurityChangeRecovery } from "~/lib/client/usecase/security/security-management";
import { AuthContext } from "./use-auth";
import { SecurityDraftRecovery } from "~/lib/client/usecase/security/security-draft-recovery";
import { AddRecoveryStore } from "~/lib/client/usecase/add-recovery/add-recovery";
import { browserAddRecoveryStorage } from "~/lib/client/infrastructure/browser/add-recovery-storage";
import { browserSecurityRecoveryStorage } from "~/lib/client/infrastructure/browser/security-change-recovery-storage";

export interface AuthLifecycle {
  watch(refresh: (logout: boolean) => void): () => void;
  announceLogout(): void;
}
export function AuthProvider({
  children,
  gatewayFactory,
  lifecycle,
}: {
  children: ReactNode;
  gatewayFactory(baseUrl: string): AuthGateway;
  lifecycle: AuthLifecycle;
}) {
  const { connection } = useConnection();
  // One writer survives gateway changes. An old in-flight Add must not
  // overwrite the retained metadata of a newly selected gateway.
  const adds = useMemo(
    () => new AddRecoveryStore(browserAddRecoveryStorage()),
    [],
  );
  const recovery = useMemo(
    () => new SecurityChangeRecovery(browserSecurityRecoveryStorage()),
    [],
  );
  const drafts = useMemo(() => new SecurityDraftRecovery(), []);
  const logoutRecovery = useMemo(() => new SessionRevocationRecovery(), []);
  const controller = useMemo(
    () =>
      new AuthController(
        gatewayFactory(connection.baseUrl),
        Date.now,
        logoutRecovery,
      ),
    [connection.baseUrl, gatewayFactory, logoutRecovery],
  );
  const state = useSyncExternalStore(
    controller.subscribe,
    controller.getSnapshot,
    controller.getSnapshot,
  );
  useEffect(() => {
    void controller.refresh();
    const stop = lifecycle.watch((logout) => {
      if (logout) {
        drafts.clear();
        controller.invalidateSession();
      }
      void controller.refresh(controller.getSnapshot().kind !== "off");
    });
    const interval = setInterval(() => {
      void controller.refresh();
    }, 15_000);
    return () => {
      clearInterval(interval);
      stop();
      controller.dispose();
    };
  }, [controller, lifecycle, recovery, drafts]);
  useEffect(() => {
    if (state.kind !== "ready" && state.kind !== "checking") drafts.clear();
  }, [state.kind, drafts]);
  const value = useMemo(
    () => ({
      state,
      controller,
      recovery,
      drafts,
      draftOwner: controller.getDraftOwner(),
      adds,
      logout: async () => {
        drafts.clear();
        const pending = controller.logout();
        lifecycle.announceLogout();
        await pending;
      },
    }),
    [state, controller, lifecycle, recovery, drafts, adds],
  );
  return (
    <AuthContext.Provider key={connection.baseUrl} value={value}>
      {children}
    </AuthContext.Provider>
  );
}
