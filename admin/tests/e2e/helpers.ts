import { Buffer } from "node:buffer";

/**
 * The Lantern Connect listener URL the Playwright webServer starts on
 * (see playwright.config.ts). Tests seed data through this URL using
 * Connect+JSON's `POST /graph.v1.LanternService/<Method>` shape rather
 * than the legacy grpc-gateway REST URLs — the gateway is no longer
 * started in the e2e profile (#339).
 */
export const CONNECT_URL =
  process.env.LANTERN_E2E_GATEWAY_URL ?? "http://127.0.0.1:6381";

/**
 * The localStorage key the admin SPA stores the active gateway URL
 * under. Re-exported here so each spec can call
 * `localStorage.setItem(STORAGE_KEY, CONNECT_URL)` from a tiny
 * page.addInitScript shim.
 */
export const STORAGE_KEY = "lantern.admin.baseUrl";

/**
 * Issues a unary Connect+JSON RPC against the Lantern server's
 * additive Connect listener. The body is the proto-JSON shape: oneof
 * fields appear flat on the message (e.g. `{ key, string: "alpha" }`
 * rather than the legacy gateway's nested `{ key, value: { string } }`).
 * Throws on any non-2xx response so failures stop the test early
 * rather than continuing against half-seeded state.
 */
export async function connectCall(
  method: string,
  body: unknown,
): Promise<unknown> {
  const resp = await fetch(`${CONNECT_URL}/graph.v1.LanternService/${method}`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "Connect-Protocol-Version": "1",
    },
    body: JSON.stringify(body),
  });
  if (!resp.ok) {
    throw new Error(
      `${method} failed: ${resp.status} ${await resp.text().catch(() => "")}`,
    );
  }
  if (resp.status === 204) {
    return {};
  }
  return resp.json();
}

/**
 * Convenience wrapper for the most common seed operation: writing a
 * batch of vertices. Each entry is the proto-JSON shape; the caller
 * supplies the typed oneof field directly.
 */
export async function putVertices(
  vertices: Array<Record<string, unknown>>,
): Promise<void> {
  await connectCall("PutVertices", { vertices });
}

/**
 * Convenience wrapper for seeding edges. Each entry carries
 * `{tail, head, weight, expiration?}`.
 */
export async function putEdges(
  edges: Array<Record<string, unknown>>,
): Promise<void> {
  await connectCall("PutEdges", { edges });
}

/**
 * Convenience wrapper for `DeleteVerticesByPrefix`. Tests use it to
 * clean up before / after a scenario so a flaky run does not poison
 * subsequent runs.
 */
export async function deleteVerticesByPrefix(prefix: string): Promise<void> {
  await connectCall("DeleteVerticesByPrefix", { prefix });
}

/**
 * Encodes raw bytes into the base64 string the proto-JSON `bytes`
 * field expects. Tests carry it through unchanged.
 */
export function bytesToBase64(bytes: Uint8Array): string {
  return Buffer.from(bytes).toString("base64");
}
