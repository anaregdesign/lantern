const STORAGE_KEY = "lantern.admin.baseUrl";

export interface BrowserStorage {
  get(key: string): string | null;
  set(key: string, value: string): void;
  remove(key: string): void;
}

/**
 * Returns a storage implementation backed by `window.localStorage`. Falls
 * back to an in-memory store when localStorage is unavailable (e.g. private
 * browsing modes, server execution).
 */
export function browserStorage(): BrowserStorage {
  if (typeof window === "undefined" || !window.localStorage) {
    return memoryStorage();
  }
  return {
    get: (key) => window.localStorage.getItem(key),
    set: (key, value) => window.localStorage.setItem(key, value),
    remove: (key) => window.localStorage.removeItem(key),
  };
}

function memoryStorage(): BrowserStorage {
  const map = new Map<string, string>();
  return {
    get: (key) => (map.has(key) ? (map.get(key) ?? null) : null),
    set: (key, value) => {
      map.set(key, value);
    },
    remove: (key) => {
      map.delete(key);
    },
  };
}

export const connectionStorageKey = STORAGE_KEY;
