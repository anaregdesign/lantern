import type { SecurityRecoveryStorage } from "~/lib/client/usecase/security/security-change-recovery";
const key = "lantern.admin.security-change-recovery.v1";

export function browserSecurityRecoveryStorage(): SecurityRecoveryStorage {
  return {
    read: () => window.sessionStorage.getItem(key),
    replace: (value) => window.sessionStorage.setItem(key, value),
  };
}
