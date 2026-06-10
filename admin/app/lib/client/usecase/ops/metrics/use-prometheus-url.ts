import { useCallback, useEffect, useMemo, useState } from "react";

import {
  browserStorage,
  prometheusStorageKey,
} from "~/lib/client/infrastructure/browser/storage";
import {
  DEFAULT_PROMETHEUS_URL,
  normalisePrometheusUrl,
} from "./prometheus-url";

export interface UsePrometheusUrl {
  prometheusUrl: string;
  /** Validates + applies a new URL. Returns false (and ignores) on invalid input. */
  setPrometheusUrl: (input: string) => boolean;
  /** Restores the same-origin reverse-proxy default. */
  reset: () => void;
}

/**
 * Owns the Prometheus query base URL for the Ops Metrics section. The value
 * is persisted to `localStorage` so it survives reloads. Unlike the gateway
 * connection this is a plain hook (not a context) because only the Metrics
 * section reads it — mirroring `connection-context` would be over-scoped.
 */
export function usePrometheusUrl(): UsePrometheusUrl {
  const storage = useMemo(() => browserStorage(), []);
  const [prometheusUrl, setState] = useState<string>(() => {
    const stored = storage.get(prometheusStorageKey);
    if (stored) {
      const normalised = normalisePrometheusUrl(stored);
      if (normalised) {
        return normalised;
      }
    }
    return DEFAULT_PROMETHEUS_URL;
  });

  useEffect(() => {
    storage.set(prometheusStorageKey, prometheusUrl);
  }, [prometheusUrl, storage]);

  const setPrometheusUrl = useCallback((input: string) => {
    const normalised = normalisePrometheusUrl(input);
    if (!normalised) {
      return false;
    }
    setState(normalised);
    return true;
  }, []);

  const reset = useCallback(() => {
    setState(DEFAULT_PROMETHEUS_URL);
  }, []);

  return { prometheusUrl, setPrometheusUrl, reset };
}
