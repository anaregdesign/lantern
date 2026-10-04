import type { AuthLifecycle } from "~/lib/client/usecase/auth/AuthProvider";
const channelName = "lantern.admin.auth-lifetime";
const storageKey = "lantern.admin.auth-invalidation";
export const browserAuthLifecycle: AuthLifecycle = {
  watch(refresh) {
    const channel =
      typeof BroadcastChannel === "undefined"
        ? undefined
        : new BroadcastChannel(channelName);
    const resume = () => {
      if (document.visibilityState === "visible") refresh(false);
    };
    const storage = (event: StorageEvent) => {
      if (event.key === storageKey) refresh(true);
    };
    if (channel) channel.onmessage = () => refresh(true);
    window.addEventListener("pageshow", resume);
    window.addEventListener("focus", resume);
    document.addEventListener("visibilitychange", resume);
    window.addEventListener("storage", storage);
    return () => {
      channel?.close();
      window.removeEventListener("pageshow", resume);
      window.removeEventListener("focus", resume);
      document.removeEventListener("visibilitychange", resume);
      window.removeEventListener("storage", storage);
    };
  },
  announceLogout() {
    if (typeof BroadcastChannel !== "undefined") {
      const channel = new BroadcastChannel(channelName);
      channel.postMessage("invalidate");
      channel.close();
    }
    try {
      window.localStorage.setItem(storageKey, crypto.randomUUID());
    } catch {
      /* BroadcastChannel still invalidates compatible tabs. */
    }
  },
};
