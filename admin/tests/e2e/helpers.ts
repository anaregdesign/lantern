import { Buffer } from "node:buffer";

/**
 * The Lantern primary listener URL the Playwright webServer starts on
 * (see playwright.config.ts). Tests seed data through this URL using
 * Connect+JSON's `POST /graph.v1.LanternService/<Method>` shape. The
 * primary :6380 port multiplexes Connect / gRPC / gRPC-Web on the same
 * h2c socket.
 */
export const CONNECT_URL =
  process.env.LANTERN_E2E_GATEWAY_URL ?? "http://127.0.0.1:6380";

/**
 * The localStorage key the admin SPA stores the active gateway URL
 * under. Re-exported here so each spec can call
 * `localStorage.setItem(STORAGE_KEY, CONNECT_URL)` from a tiny
 * page.addInitScript shim.
 */
export const STORAGE_KEY = "lantern.admin.baseUrl";

/**
 * Issues a unary Connect+JSON RPC against the Lantern server's
 * primary listener. The body is the proto-JSON shape: oneof
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

/** Rendered SPA contract fixture; real Connect authority is tested separately. */
export async function securityUI(
  page: import("@playwright/test").Page,
  options: {
    mode?: "ready" | "login" | "off" | "unavailable";
    recent?: boolean;
    apply?: "conflict" | "lost";
    status?: "pending-then-enforced" | "unknown" | "mismatch";
    denied?: boolean;
    reauthentication?: boolean;
  } = {},
) {
  const calls: Array<{
    method: string;
    body: Record<string, unknown>;
    csrf?: string;
    authorization?: string;
  }> = [];
  const version = {
    revision: "3",
    digest: Buffer.alloc(32, 1).toString("base64"),
    generation: Buffer.alloc(16, 2).toString("base64"),
  };
  let signedIn = options.mode !== "login";
  let statusCalls = 0;
  const primary = "https://admin.example";
  let approved = false;
  const authorizationPath = "/auth/management-authorization/" + "A".repeat(43);
  if (options.reauthentication)
    await page.context().route(primary + authorizationPath, async (route) => {
      approved = true;
      await route.fulfill({
        contentType: "text/html",
        body: "<title>Operation authentication</title><p>Authentication recorded. Return to the reviewed change in Admin.</p>",
      });
    });
  await page.addInitScript(
    ({ key, url }) => {
      localStorage.setItem(key, url);
      localStorage.setItem("lantern.admin.authToken", "retired-token");
    },
    { key: STORAGE_KEY, url: primary },
  );
  await page.route(`${primary}/**`, async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const json = (body: unknown, status = 200) =>
      route.fulfill({
        status,
        contentType: "application/json",
        body: JSON.stringify(body),
        headers: { "cache-control": "no-store" },
      });
    if (path.endsWith("/GetAuthCapabilities"))
      return json(
        options.mode === "off"
          ? { mode: "AUTH_MODE_OFF", protocolVersion: 1, ready: true }
          : {
              mode: "AUTH_MODE_OIDC",
              protocolVersion: 1,
              ready: options.mode !== "unavailable",
              loginPath: "/auth/login",
              loginIssuers: [
                { issuer: "https://idp.example", label: "Example" },
              ],
            },
      );
    if (path === "/auth/session") {
      if (!signedIn) return json({ code: "unauthenticated" }, 401);
      return json({
        mode: "AUTH_MODE_OIDC",
        principal: {
          identity: {
            kind: "SECURITY_PRINCIPAL_KIND_OIDC",
            issuer: "https://idp.example",
            subject: "admin",
          },
          version,
          expiresAt: new Date(Date.now() + 25_000).toISOString(),
          recentAuthentication: options.recent !== false,
          csrfToken: "c".repeat(43),
        },
      });
    }
    if (path === "/auth/logout") {
      signedIn = false;
      return json({
        version,
        enforcement: "SECURITY_ENFORCEMENT_STATE_COMMITTED_PENDING",
      });
    }
    if (path.startsWith("/browser/graph.v1.LanternSecurityService/")) {
      const method = path.split("/").at(-1)!;
      const body = request.postDataJSON() as Record<string, unknown>;
      calls.push({
        method,
        body,
        csrf: request.headers()["x-lantern-csrf"],
        authorization: request.headers().authorization,
      });
      if (options.denied)
        return json({ code: "permission_denied", message: "Denied" }, 403);
      if (method === "ListRoles")
        return json({
          version,
          roles: [
            {
              id: "security_admin",
              name: "Security administrator",
              envOwned: true,
              rules: [],
            },
            {
              id: "reader",
              name: "Reader",
              rules: [
                {
                  id: "read",
                  action: "SECURITY_ACTION_VERTEX_READ",
                  effect: "SECURITY_EFFECT_ALLOW",
                  prefix: "tenant:",
                },
              ],
            },
          ],
        });
      if (method === "ListUsers")
        return json({
          version,
          users: [
            {
              identity: {
                kind: "SECURITY_PRINCIPAL_KIND_OIDC",
                issuer: "https://idp.example",
                subject: "alice",
              },
              state: "SECURITY_PRINCIPAL_STATE_ACTIVE",
              assignments: [{ roleId: "reader", envOwned: true }],
            },
          ],
        });
      if (method === "ListIssuers")
        return json({
          version,
          issuers: [
            {
              issuer: "https://idp.example",
              enabled: true,
              clientId: "admin",
              apiAudience: "lantern",
              redirectUri: `${primary}/auth/callback`,
              algorithms: ["EdDSA"],
              envOwned: true,
              configRevision: "1",
              hasSecretBinding: true,
            },
          ],
        });
      if (method === "ApplySecurityChanges") {
        if (options.apply === "conflict")
          return json({ code: "aborted", message: "Revision conflict" }, 409);
        if (options.apply === "lost") return route.abort("failed");
        return json({
          version: { ...version, revision: "4" },
          applied: [true],
          enforcement: "SECURITY_ENFORCEMENT_STATE_COMMITTED_PENDING",
        });
      }
      if (method === "PrepareSecurityChanges") {
        const review = body.review as Record<string, unknown>;
        return json({
          expectedVersion: review.expectedVersion,
          changeId: review.changeId,
          intentDigest: Buffer.alloc(32, 4).toString("base64"),
          requirement: options.reauthentication
            ? "SECURITY_AUTHORIZATION_REQUIREMENT_REAUTHENTICATION"
            : "SECURITY_AUTHORIZATION_REQUIREMENT_ORDINARY",
        });
      }
      if (method === "BeginSecurityChangeAuthorization")
        return json({
          authorizationId: Buffer.alloc(32, 8).toString("base64"),
          startUrl: primary + authorizationPath,
          expiresAt: new Date(Date.now() + 60_000).toISOString(),
        });
      if (method === "GetSecurityChangeAuthorization")
        return json({
          authorizationId: body.authorizationId,
          state: approved
            ? "SECURITY_AUTHORIZATION_STATE_APPROVED"
            : "SECURITY_AUTHORIZATION_STATE_PENDING",
          authorizationProof: approved
            ? Buffer.alloc(32, 9).toString("base64")
            : "",
          expiresAt: new Date(Date.now() + 60_000).toISOString(),
        });
      if (method === "GetSecurityChangeStatus") {
        statusCalls++;
        if (options.status === "unknown")
          return json(
            {
              code: "failed_precondition",
              message: "Outside retained history",
            },
            412,
          );
        return json({
          version: {
            ...version,
            revision: "4",
            digest:
              options.status === "mismatch"
                ? Buffer.alloc(32, 9).toString("base64")
                : version.digest,
          },
          changeId: body.changeId,
          enforcement:
            options.status === "pending-then-enforced" && statusCalls === 1
              ? "SECURITY_ENFORCEMENT_STATE_COMMITTED_PENDING"
              : "SECURITY_ENFORCEMENT_STATE_ENFORCED",
        });
      }
      if (method === "ExplainAccess")
        return json({
          version,
          allowed: false,
          matches: [
            {
              roleId: "private",
              ruleId: "hide",
              effect: "SECURITY_EFFECT_DENY",
              action: body.edge
                ? "SECURITY_ACTION_VERTEX_WRITE"
                : "SECURITY_ACTION_VERTEX_READ",
              endpoint: body.edge ? "head" : "",
            },
          ],
        });
      if (method === "GetRoleTemplates")
        return json({
          version,
          roles: [
            {
              id: "reader",
              name: "Reader template",
              rules: [
                {
                  id: "read",
                  action: "SECURITY_ACTION_VERTEX_READ",
                  effect: "SECURITY_EFFECT_ALLOW",
                  prefix: body.prefix,
                },
              ],
            },
          ],
        });
      if (method === "ListSecurityAudit")
        return json({
          version,
          records: [
            {
              revision: "4",
              operation: "security.update",
              outcome: "committed",
              changeId: "redacted-id",
            },
          ],
        });
      if (method === "ValidateIssuer") return json({ valid: true });
    }
    const base = new URL(
      `http://127.0.0.1:${process.env.LANTERN_E2E_PREVIEW_PORT ?? 4173}`,
    );
    const local = new URL(request.url());
    local.protocol = base.protocol;
    local.host = base.host;
    const response = await page.request.get(local.href);
    return route.fulfill({ response });
  });
  return { calls, primary };
}
