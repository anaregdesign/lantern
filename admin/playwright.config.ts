import { fileURLToPath } from "node:url";
import { dirname } from "node:path";
import { defineConfig, devices } from "@playwright/test";

const __dirname = dirname(fileURLToPath(import.meta.url));

// Ports are env-overridable (defaults match CI) so the suite can run on a
// machine that already has a Lantern instance on :6380 — e.g. a local
// `deploy/compose` cluster — without a bind conflict. CI sets none of these,
// so it keeps the canonical 4173 / 6380 / 9090 trio.
const PORT = Number(process.env.LANTERN_E2E_PREVIEW_PORT ?? 4173);
// The Lantern primary listener (:6380 by default) serves Connect /
// gRPC / gRPC-Web on the same h2c socket. The admin SPA talks to it
// directly via Connect-Web.
const LANTERN_PORT = Number(process.env.LANTERN_PORT ?? 6380);
const METRICS_PORT = Number(process.env.LANTERN_E2E_METRICS_PORT ?? 9090);
const CONNECT_URL = `http://127.0.0.1:${LANTERN_PORT}`;

process.env.LANTERN_E2E_GATEWAY_URL = CONNECT_URL;

export default defineConfig({
  testDir: "./tests/e2e",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: process.env.CI ? "github" : "list",
  use: {
    baseURL: `http://127.0.0.1:${PORT}`,
    trace: "on-first-retry",
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
  webServer: [
    {
      command: `go run ../server/cmd`,
      // Health probe lives on the metrics server, not the primary
      // listener — readyz is the canonical Lantern liveness probe per
      // docs/ha-runbook.md. Wait for it before starting the SPA.
      url: `http://127.0.0.1:${METRICS_PORT}/healthz`,
      cwd: __dirname,
      reuseExistingServer: !process.env.CI,
      timeout: 120_000,
      env: {
        LANTERN_PORT: String(LANTERN_PORT),
        LANTERN_METRICS_ADDR: `:${METRICS_PORT}`,
        LANTERN_CORS_ALLOWED_ORIGINS: `http://127.0.0.1:${PORT}`,
        LANTERN_LOG_LEVEL: "warn",
        // Content search is opt-out (on by default), but pin it explicitly
        // so the search e2e (#627) stays deterministic if the default ever
        // changes.
        LANTERN_SEARCH_ENABLED: "true",
      },
    },
    {
      command: `bun x vite preview --outDir build/client --host 127.0.0.1 --port ${PORT} --strictPort`,
      url: `http://127.0.0.1:${PORT}`,
      reuseExistingServer: !process.env.CI,
      timeout: 120_000,
    },
  ],
});
