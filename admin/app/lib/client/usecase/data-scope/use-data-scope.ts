import { useAuth } from "~/lib/client/usecase/auth/use-auth";
import { scopeOptions } from "./scope-options";

export function useDataScope() {
  const { state, controller } = useAuth();
  const prefix = controller.getSelectedScope();
  const roles = state.kind === "ready" ? state.principal.roles : [];
  return {
    authenticated: state.kind === "ready",
    prefix,
    options: scopeOptions(
      roles,
      state.kind === "ready" ? state.principal.userId : undefined,
    ),
    select: (value: string, replaceExplicitPrefix = false) =>
      controller.selectScope(value, replaceExplicitPrefix),
  };
}
