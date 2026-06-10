/**
 * Default Prometheus query base URL for the Ops Metrics section.
 *
 * The bundled deployment topologies (compose / helm) reverse-proxy
 * Prometheus through the admin's own Caddy origin at `/api/prom`, so the
 * browser never makes a cross-origin request and CORS never applies. The
 * SPA therefore defaults to this same-origin path; operators running
 * Prometheus elsewhere can point the URL at an absolute `http(s)://…`
 * endpoint via the in-app Prometheus switcher.
 *
 * See `admin/Caddyfile` + `admin/docker-entrypoint.sh` for the opt-in
 * reverse-proxy route gated on `LANTERN_ADMIN_PROMETHEUS_UPSTREAM`.
 */
export const DEFAULT_PROMETHEUS_URL = "/api/prom";

/**
 * Normalises a user-entered Prometheus base URL.
 *
 * Unlike `normaliseBaseUrl` (which requires an absolute http(s) URL), the
 * Prometheus URL may also be a **root-relative path** like `/api/prom`,
 * because the default deployment reverse-proxies Prometheus through the
 * admin's own origin. Both forms are accepted:
 *
 * - Absolute: `http://localhost:9090`, `https://prom.example.com/prefix`
 * - Root-relative: `/api/prom`, `/metrics-proxy`
 *
 * In every case a trailing slash is stripped so callers can append
 * `/api/v1/query_range` unconditionally. Returns `null` for inputs that are
 * empty, use a non-http(s) scheme, or are relative paths that do not begin
 * with `/`.
 */
export function normalisePrometheusUrl(input: string): string | null {
  const trimmed = input.trim();
  if (trimmed === "") {
    return null;
  }

  // Root-relative same-origin path (the default deployment shape).
  if (trimmed.startsWith("/")) {
    // Reject protocol-relative `//host` — ambiguous and not what we mean.
    if (trimmed.startsWith("//")) {
      return null;
    }
    const stripped = trimmed.replace(/\/+$/, "");
    return stripped === "" ? "/" : stripped;
  }

  // Otherwise it must be an absolute http(s) URL.
  let url: URL;
  try {
    url = new URL(trimmed);
  } catch {
    return null;
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    return null;
  }
  const path = url.pathname.replace(/\/$/, "");
  return `${url.protocol}//${url.host}${path}`;
}
