import { LanternApiError } from "~/lib/client/infrastructure/api/error";
import { PrometheusError } from "~/lib/client/infrastructure/api/prometheus-query-range";

/** Explain the refused destination without displaying credentials or raw bodies. */
export function diagnosticsError(error: unknown, destination: string): string {
  const status =
    error instanceof PrometheusError ? error.httpStatus : undefined;
  const code = error instanceof LanternApiError ? error.code : undefined;
  if (status === 401 || code === "unauthenticated") {
    return `${destination}: sign in again. The session is missing, expired or revoked.`;
  }
  if (status === 403 || code === "permission_denied") {
    return `${destination}: access refused. Ask a security administrator for read permission on Operations; data and security administration Roles do not grant it.`;
  }
  if (status === 503) {
    return `${destination}: diagnostics authorization or Prometheus is unavailable. Ask the operator to check Server status, the Operations authorization route and the fixed Prometheus upstream.`;
  }
  if (status === 502 || status === 504) {
    return `${destination}: the diagnostics gateway could not complete authorization or reach Prometheus. Ask the operator to check Server readiness, the Operations authorization route and the upstream service and network.`;
  }
  if (code === "unavailable" || code === "deadline_exceeded") {
    return `${destination}: Server unavailable. Check the selected Gateway and Server readiness, then refresh.`;
  }
  if (error instanceof PrometheusError && error.kind === "parse") {
    return `${destination}: unexpected response. Verify this URL serves the Prometheus API rather than an HTML page.`;
  }
  if (
    error instanceof TypeError ||
    (error instanceof PrometheusError && error.kind === "network")
  ) {
    return `${destination}: connection failed. Check the destination, network and trusted TLS certificate, then refresh.`;
  }
  return `${destination}: request failed. Ask the operator to inspect this service's diagnostics.`;
}
