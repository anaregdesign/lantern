import type { SecurityAction } from "lantern-sdk/web";
import { useAuth } from "~/lib/client/usecase/auth/use-auth";
import { deniedScopes, scopeOptions } from "./scope-options";

export function useDataScope(actions: readonly SecurityAction[]) {
  const { state, controller } = useAuth();
  const prefix = controller.getSelectedScope();
  const roles = state.kind === "ready" ? state.principal.roles : [];
  return {
    authenticated: state.kind === "ready",
    prefix,
    options: scopeOptions(roles, actions),
    denied: deniedScopes(roles, actions, prefix),
    exceptions: (value: string) => deniedScopes(roles, actions, value),
    select: (value: string) => controller.selectScope(value),
  };
}
