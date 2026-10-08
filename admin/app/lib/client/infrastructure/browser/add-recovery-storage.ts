import type { AddRecoveryStorage } from "~/lib/client/usecase/add-recovery/add-recovery";
const key = "lantern.admin.add-recovery.v1";

export function browserAddRecoveryStorage(): AddRecoveryStorage {
  return {
    read: () =>
      typeof window === "undefined" ? null : window.sessionStorage.getItem(key),
    write: (value) => window.sessionStorage.setItem(key, value),
    newID: () => crypto.randomUUID(),
  };
}
