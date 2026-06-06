import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import {
  browserStorage,
  connectionStorageKey,
} from "~/lib/client/infrastructure/browser/storage";
import { DEFAULT_BASE_URL, normaliseBaseUrl } from "./base-url";

export interface Connection {
  baseUrl: string;
}

export interface ConnectionContextValue {
  connection: Connection;
  setBaseUrl: (input: string) => boolean;
  reset: () => void;
}

const ConnectionContext = createContext<ConnectionContextValue | null>(null);

export interface ConnectionProviderProps {
  children: ReactNode;
}

/**
 * Owns the active Lantern gateway base URL. The value is persisted to
 * `localStorage` so it survives reloads. Components consume the URL through
 * the `useConnection` hook; the API client adapter is constructed from this
 * URL inside `lib/client/infrastructure/api/`.
 */
export function ConnectionProvider({ children }: ConnectionProviderProps) {
  const storage = useMemo(() => browserStorage(), []);
  const [baseUrl, setBaseUrlState] = useState<string>(() => {
    const stored = storage.get(connectionStorageKey);
    if (stored) {
      const normalised = normaliseBaseUrl(stored);
      if (normalised) {
        return normalised;
      }
    }
    return DEFAULT_BASE_URL;
  });

  useEffect(() => {
    storage.set(connectionStorageKey, baseUrl);
  }, [baseUrl, storage]);

  const setBaseUrl = useCallback((input: string) => {
    const normalised = normaliseBaseUrl(input);
    if (!normalised) {
      return false;
    }
    setBaseUrlState(normalised);
    return true;
  }, []);

  const reset = useCallback(() => {
    setBaseUrlState(DEFAULT_BASE_URL);
  }, []);

  const value = useMemo<ConnectionContextValue>(
    () => ({
      connection: { baseUrl },
      setBaseUrl,
      reset,
    }),
    [baseUrl, setBaseUrl, reset],
  );

  return (
    <ConnectionContext.Provider value={value}>
      {children}
    </ConnectionContext.Provider>
  );
}

export function useConnection(): ConnectionContextValue {
  const ctx = useContext(ConnectionContext);
  if (!ctx) {
    throw new Error("useConnection must be used inside a ConnectionProvider");
  }
  return ctx;
}
