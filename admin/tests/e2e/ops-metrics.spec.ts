import { expect, test } from "@playwright/test";

/**
 * Ops Metrics (#524) — the Prometheus time-series half of the Ops page.
 *
 * The Playwright `webServer` serves the SPA from a `vite preview` build with
 * NO Prometheus behind `/api/prom`, so the default state is "degraded": every
 * panel's `query_range` request falls through to the SPA `index.html`
 * fallback, the adapter rejects with a `parse` error, and MetricsSection
 * surfaces the aggregate warning banner. The second scenario installs a
 * `page.route` mock for `/api/prom/api/v1/query_range` so the panels render
 * real charts without needing a live Prometheus.
 */

/**
 * Builds a Prometheus `query_range` matrix envelope with `count` samples of a
 * single series, spaced `step` seconds apart and ending at `now`.
 */
function matrixEnvelope(now: number, step = 60, count = 10) {
  const values: Array<[number, string]> = [];
  for (let i = count - 1; i >= 0; i -= 1) {
    values.push([now - i * step, String(100 + (count - i))]);
  }
  return {
    status: "success",
    data: {
      resultType: "matrix",
      result: [{ metric: { __name__: "lantern_vertices" }, values }],
    },
  };
}

test.describe("Ops metrics section", () => {
  test("renders the metrics section below the status cards", async ({
    page,
  }) => {
    await page.goto("/ops");
    await expect(page.getByTestId("ops-server-card")).toBeVisible();
    const section = page.getByTestId("ops-metrics-section");
    await expect(section).toBeVisible();
    await expect(
      section.getByRole("heading", { level: 2, name: "Metrics" }),
    ).toBeVisible();
    // The Prometheus switcher advertises the default same-origin endpoint.
    await expect(page.getByTestId("ops-prometheus-switcher")).toContainText(
      "/api/prom",
    );
    // The range selector defaults to 1h.
    await expect(
      page.getByTestId("ops-metrics-range").getByRole("tab", { name: "1h" }),
    ).toHaveAttribute("aria-selected", "true");
  });

  test("shows the degraded banner when no Prometheus is reachable", async ({
    page,
  }) => {
    // vite preview has no /api/prom upstream — every panel fails to load.
    await page.goto("/ops");
    await expect(page.getByTestId("ops-metrics-degraded")).toBeVisible();
    await expect(page.getByTestId("ops-metrics-degraded")).toContainText(
      "/api/prom",
    );
  });

  test("renders charts when query_range is mocked", async ({ page }) => {
    await page.route("**/api/prom/api/v1/query_range*", async (route) => {
      const now = Math.floor(Date.now() / 1000);
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(matrixEnvelope(now)),
      });
    });

    await page.goto("/ops");
    // The degraded banner must NOT appear once panels resolve.
    await expect(page.getByTestId("ops-metrics-degraded")).toHaveCount(0);
    // The cache-size panel is the first catalog entry; it renders a summary
    // line derived from the mocked series.
    await expect(page.getByTestId("ops-metric-cache-size")).toBeVisible();
    await expect(
      page.getByTestId("ops-metric-cache-size-summary"),
    ).toBeVisible();
  });

  test("persists a runtime Prometheus URL override", async ({ page }) => {
    await page.goto("/ops");
    await page.getByTestId("ops-prometheus-switcher").click();
    const input = page.getByTestId("ops-prometheus-input");
    await expect(input).toBeVisible();
    await input.fill("https://prom.example.com");
    await page.getByTestId("ops-prometheus-save").click();
    await expect(page.getByTestId("ops-prometheus-switcher")).toContainText(
      "https://prom.example.com",
    );
    // The override survives a reload (localStorage persistence).
    await page.reload();
    await expect(page.getByTestId("ops-prometheus-switcher")).toContainText(
      "https://prom.example.com",
    );
  });
});
