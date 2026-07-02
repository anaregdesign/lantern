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
  connectionTokenStorageKey,
} from "~/lib/client/infrastructure/browser/storage";
import { DEFAULT_BASE_URL, normaliseBaseUrl } from "./base-url";

export interface Connection {
  baseUrl: string;
  /**
   * Optional bearer token for servers running with LANTERN_AUTH_TOKENS
   * (#850). Empty string = no auth header.
   */
  token: string;
}

export interface ConnectionContextValue {
  connection: Connection;
  setBaseUrl: (input: string) => boolean;
  setToken: (input: string) => void;
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

  const [token, setTokenState] = useState<string>(
    () => storage.get(connectionTokenStorageKey) ?? "",
  );

  useEffect(() => {
    storage.set(connectionStorageKey, baseUrl);
  }, [baseUrl, storage]);

  useEffect(() => {
    if (token) {
      storage.set(connectionTokenStorageKey, token);
    } else {
      storage.remove(connectionTokenStorageKey);
    }
  }, [token, storage]);

  const setBaseUrl = useCallback((input: string) => {
    const normalised = normaliseBaseUrl(input);
    if (!normalised) {
      return false;
    }
    setBaseUrlState(normalised);
    return true;
  }, []);

  const setToken = useCallback((input: string) => {
    setTokenState(input.trim());
  }, []);

  const reset = useCallback(() => {
    setBaseUrlState(DEFAULT_BASE_URL);
    setTokenState("");
  }, []);

  const value = useMemo<ConnectionContextValue>(
    () => ({
      connection: { baseUrl, token },
      setBaseUrl,
      setToken,
      reset,
    }),
    [baseUrl, token, setBaseUrl, setToken, reset],
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
