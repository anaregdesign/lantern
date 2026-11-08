import { expect, test } from "bun:test";
import { LanternError } from "lantern-sdk/web";
import { LanternApiError } from "~/lib/client/infrastructure/api/error";
import { PrometheusError } from "~/lib/client/infrastructure/api/prometheus-query-range";
import { diagnosticsError } from "./diagnostics-error";

test("diagnostics distinguish authentication, permission, upstream and protocol failures", () => {
  expect(diagnosticsError(PrometheusError.http(401), "/api/prom")).toContain(
    "sign in again",
  );
  expect(diagnosticsError(PrometheusError.http(403), "/api/prom")).toContain(
    "read permission on Operations",
  );
  expect(diagnosticsError(PrometheusError.http(503), "/api/prom")).toContain(
    "diagnostics authorization or Prometheus is unavailable",
  );
  for (const status of [502, 504]) {
    const message = diagnosticsError(PrometheusError.http(status), "/api/prom");
    expect(message).toContain(
      "could not complete authorization or reach Prometheus",
    );
    expect(message).toContain("Server readiness");
  }
  expect(
    diagnosticsError(PrometheusError.parse("HTML"), "/api/prom"),
  ).toContain("rather than an HTML page");
  expect(diagnosticsError(new TypeError("secret"), "Gateway")).toContain(
    "trusted TLS",
  );
  expect(diagnosticsError(new Error("private-token"), "Gateway")).not.toContain(
    "private-token",
  );
  for (const [code, message] of [
    [7, "read permission on Operations"],
    [16, "sign in again"],
    [14, "Server unavailable"],
  ] as const) {
    const error = LanternApiError.fromUnknown(
      "GetServerStatus",
      new LanternError("opaque", { cause: { code } }),
    );
    expect(diagnosticsError(error, "Gateway")).toContain(message);
  }
});
