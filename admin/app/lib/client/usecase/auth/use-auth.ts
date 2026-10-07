import { createContext, useContext } from "react";
import type { AuthController, AuthState } from "./auth-state";
import type { SecurityChangeRecovery } from "~/lib/client/usecase/security/security-management";
import type { AddRecoveryStore } from "~/lib/client/usecase/add-recovery/add-recovery";
export interface AuthContextValue {
  adds: AddRecoveryStore;
  recovery: SecurityChangeRecovery;
  state: AuthState;
  controller: AuthController;
  logout(): Promise<void>;
}
export const AuthContext = createContext<AuthContextValue | null>(null);
export function useAuth(): AuthContextValue {
  const value = useContext(AuthContext);
  if (!value) throw new Error("useAuth requires AuthProvider");
  return value;
}
