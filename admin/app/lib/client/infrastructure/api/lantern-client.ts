export interface LanternClientOptions {
  baseUrl: string;
  fetch?: typeof fetch;
}

export interface LanternClient {
  /** Configured base URL, e.g. `http://localhost:6381`. */
  readonly baseUrl: string;
  /**
   * Calls the Lantern gateway with a path relative to the base URL.
   * The response is returned untyped at this layer — typed wrappers live
   * alongside the codegen'd `lantern-api.gen.ts` consumers under
   * `lib/client/usecase/`.
   */
  request: (path: string, init?: RequestInit) => Promise<Response>;
}

/**
 * Thin adapter around `fetch` that owns the gateway base URL. Keep all
 * Lantern HTTP knowledge in this module; do not call `fetch` directly from
 * use-cases or components.
 */
export function createLanternClient(opts: LanternClientOptions): LanternClient {
  const fetchImpl = opts.fetch ?? globalThis.fetch.bind(globalThis);
  const baseUrl = opts.baseUrl.replace(/\/$/, "");

  return {
    baseUrl,
    request: (path, init) => {
      const url = path.startsWith("http")
        ? path
        : `${baseUrl}${path.startsWith("/") ? path : `/${path}`}`;
      const headers = new Headers(init?.headers);
      if (!headers.has("Accept")) {
        headers.set("Accept", "application/json");
      }
      if (
        init?.body !== undefined &&
        init.body !== null &&
        !headers.has("Content-Type")
      ) {
        headers.set("Content-Type", "application/json");
      }
      return fetchImpl(url, { ...init, headers });
    },
  };
}
