import { connectWeb, type Lantern } from "lantern-sdk/web";

/**
 * Thin re-export of the lantern-sdk Lantern client type. The admin SPA
 * has historically called the client type `LanternClient`; keeping
 * that alias avoids renaming every usecase hook now that the SDK is
 * the source of truth (#409).
 */
export type LanternClient = Lantern;

export interface LanternClientOptions {
  baseUrl: string;
  /** Optional bearer token for LANTERN_AUTH_TOKENS servers (#850). */
  token?: string;
}

/**
 * Build a `lantern-sdk/web` Connect-Web client bound to the supplied
 * gateway base URL. The base URL is normalised by trimming any
 * trailing slash; the SDK itself enforces the scheme requirement
 * (`http://` or `https://`).
 *
 * The browser flavour of the SDK speaks Connect over HTTP/1.1 fetch
 * with the JSON codec by default — matching what the legacy admin
 * adapter shipped. Same wire shape, same server requirements
 * (`LANTERN_CORS_ALLOWED_ORIGINS` must include the admin origin), one
 * fewer adapter layer to maintain.
 */
export function createLanternClient(opts: LanternClientOptions): LanternClient {
  return connectWeb(opts.baseUrl.replace(/\/$/, ""), {
    token: opts.token || undefined,
  });
}
