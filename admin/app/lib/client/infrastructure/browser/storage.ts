const STORAGE_KEY = "lantern.admin.baseUrl";
const PROMETHEUS_STORAGE_KEY = "lantern.admin.prometheusUrl";
const METRICS_RANGE_STORAGE_KEY = "lantern.admin.metricsRange";
const METRICS_AGG_MODE_STORAGE_KEY = "lantern.admin.metricsAggMode";

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

/**
 * localStorage key the admin SPA stores the Prometheus query base URL
 * under (used by the Ops Metrics section). Defaults to the same-origin
 * reverse-proxy path `/api/prom` — see `usecase/ops/metrics/prometheus-url.ts`.
 */
export const prometheusStorageKey = PROMETHEUS_STORAGE_KEY;

/**
 * localStorage key the admin SPA stores the Ops Metrics time range
 * selection under (e.g. `1h`). See `usecase/ops/metrics/range.ts`.
 */
export const metricsRangeStorageKey = METRICS_RANGE_STORAGE_KEY;

/**
 * localStorage key the admin SPA stores the Ops Metrics aggregation mode
 * under (`per-replica` or `sum`). See `usecase/ops/metrics/mode.ts`.
 */
export const metricsAggModeStorageKey = METRICS_AGG_MODE_STORAGE_KEY;
