import {
  connectSecurityWeb,
  parseBrowserSession,
  parseSessionRevocation,
  encodeCurrentLogoutRequest,
  type LanternArgs,
} from "lantern-sdk/web";
import type { AuthGateway } from "~/lib/client/usecase/auth/auth-state";

export function browserTransport(
  signal?: AbortSignal,
  csrf?: string,
): LanternArgs {
  return {
    interceptors: [
      (next) => (request) => {
        if (csrf) request.header.set("X-Lantern-CSRF", csrf);
        return next(request);
      },
    ],
    transportOptions: {
      fetch: (input: RequestInfo | URL, init?: RequestInit) =>
        fetch(input, {
          ...init,
          credentials: "same-origin",
          cache: "no-store",
          redirect: "error",
          signal: signal
            ? AbortSignal.any([signal, ...(init?.signal ? [init.signal] : [])])
            : init?.signal,
        }),
    },
  };
}
export function createSecurityClient(
  baseUrl: string,
  signal?: AbortSignal,
  csrf?: string,
) {
  return connectSecurityWeb(
    baseUrl + (csrf ? "/browser" : ""),
    browserTransport(signal, csrf),
  );
}
export function createAdminAuthGateway(baseUrl: string): AuthGateway {
  const security = createSecurityClient(baseUrl);
  const origin = typeof window === "undefined" ? "" : window.location.origin;
  const url = new URL(baseUrl);
  return {
    sameOriginHTTPS: url.protocol === "https:" && url.origin === origin,
    capabilities: (signal) =>
      security.getAuthCapabilities({}, { signal, timeoutMs: 5000 }),
    async session(signal) {
      const response = await fetch(baseUrl + "/auth/session", {
        signal: AbortSignal.any([signal, AbortSignal.timeout(5000)]),
        credentials: "same-origin",
        cache: "no-store",
        redirect: "error",
      });
      if (response.status === 401) return null;
      if (!response.ok)
        throw new Error("Browser authentication is unavailable.");
      const text = await response.text();
      if (text.length > 1 << 20)
        throw new Error("Invalid browser session response.");
      return parseBrowserSession(text);
    },
    async logout(request, csrf, signal) {
      const response = await fetch(baseUrl + "/auth/logout", {
        method: "POST",
        signal: AbortSignal.any([signal, AbortSignal.timeout(5000)]),
        credentials: "same-origin",
        cache: "no-store",
        redirect: "error",
        headers: { "X-Lantern-CSRF": csrf, "Content-Type": "application/json" },
        body: encodeCurrentLogoutRequest(request),
      });
      if (!response.ok) throw new Error("Logout could not be confirmed.");
      const body = await response.text();
      if (body.length > 1 << 20) throw new Error("Invalid logout response.");
      return parseSessionRevocation(body);
    },
    async logoutStatus(review, csrf, signal) {
      const response = await createSecurityClient(
        baseUrl,
        signal,
        csrf,
      ).getSecurityChangeStatus(
        {
          currentProfile: review.profile,
          currentChangeId: review.changeId,
          currentIntentDigest: review.intentDigest,
        },
        { signal, timeoutMs: 5000 },
      );
      if (!response.currentResult) throw new Error("Missing original result.");
      return response.currentResult;
    },
    login(issuer) {
      const login = new URL(baseUrl + "/auth/login");
      login.searchParams.set("issuer", issuer);
      login.searchParams.set("return", "/");
      window.location.assign(login.href);
    },
  };
}
