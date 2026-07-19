import { expect, test } from "@playwright/test";

import { CONNECT_URL, STORAGE_KEY } from "./helpers";

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

/**
 * Builds a `query_range` matrix envelope with one series per `instance`, each
 * offset so the lines are distinct. Mirrors what Prometheus returns for a
 * per-replica query (`sum by (instance) (…)`): every series carries an
 * `instance` label, which is what drives the replica alias map and key.
 */
function multiInstanceEnvelope(
  now: number,
  instances: string[],
  step = 60,
  count = 10,
) {
  const result = instances.map((instance, idx) => {
    const values: Array<[number, string]> = [];
    for (let i = count - 1; i >= 0; i -= 1) {
      values.push([now - i * step, String(100 + idx * 10 + (count - i))]);
    }
    return { metric: { __name__: "lantern_vertices", instance }, values };
  });
  return { status: "success", data: { resultType: "matrix", result } };
}

test.describe("Ops metrics section", () => {
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(
      ({ key, value }) => window.localStorage.setItem(key, value),
      { key: STORAGE_KEY, value: CONNECT_URL },
    );
  });

  test("renders the metrics section below the status cards", async ({
    page,
  }) => {
    await page.goto("/ops");
    await expect(page.getByTestId("ops-server-card")).toBeVisible();
    await expect(page.getByTestId("ops-search-card")).toBeVisible();
    await expect(page.getByTestId("ops-search-health")).toHaveText("healthy");
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
    // The aggregation toggle defaults to per-replica.
    await expect(
      page
        .getByTestId("ops-metrics-agg-mode")
        .getByRole("tab", { name: "Per-replica" }),
    ).toHaveAttribute("aria-selected", "true");
  });

  test("persists the aggregation mode across reloads", async ({ page }) => {
    await page.goto("/ops");
    const toggle = page.getByTestId("ops-metrics-agg-mode");
    await toggle.getByRole("tab", { name: "Sum" }).click();
    await expect(toggle.getByRole("tab", { name: "Sum" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    await page.reload();
    await expect(toggle.getByRole("tab", { name: "Sum" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
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
    const section = page.getByTestId("ops-metrics-section");
    // The degraded banner must NOT appear once panels resolve.
    await expect(page.getByTestId("ops-metrics-degraded")).toHaveCount(0);
    // The cache-size panel is the first catalog entry; it renders a summary
    // line derived from the mocked series.
    await expect(page.getByTestId("ops-metric-cache-size")).toBeVisible();
    await expect(
      page.getByTestId("ops-metric-cache-size-summary"),
    ).toBeVisible();
    await expect(
      page.getByTestId("ops-metric-illuminate-outcomes-bfs-summary"),
    ).toBeVisible();
    await expect(
      page.getByTestId("ops-metric-illuminate-outcomes-ppr-summary"),
    ).toBeVisible();
    await expect(
      page.getByTestId("ops-metric-illuminate-outcomes-community-summary"),
    ).toBeVisible();
    await expect(page.getByTestId("ops-metric-traversal-outcomes")).toHaveCount(
      0,
    );
    for (const heading of [
      "Store inventory",
      "Request traffic",
      "Illuminate",
      "Search requests",
      "Search index",
      "Maintenance",
      "Replication",
      "Guardrails",
      "Process / Go runtime",
    ]) {
      await expect(
        section.getByRole("heading", { level: 3, name: heading }),
      ).toBeVisible();
    }
    await expect(
      page.getByTestId("ops-metrics-group-search-requests"),
    ).toBeVisible();
    await expect(
      page.getByTestId("ops-metrics-group-search-index"),
    ).toBeVisible();
    await expect(page.getByTestId("ops-metric-search-successes")).toBeVisible();
    await expect(
      page.getByTestId("ops-metric-search-successes-summary"),
    ).toBeVisible();
    await expect(
      page.getByTestId("ops-metric-search-index-state-summary"),
    ).toBeVisible();
    await expect(page.getByTestId("ops-metric-search-outcomes")).toHaveCount(0);
    await expect(
      page.getByTestId("ops-metric-replication-drops"),
    ).toBeVisible();
    await expect(page.getByTestId("ops-metric-rejections")).toHaveCount(0);
  });

  test("keeps absent search metrics local to the search panels", async ({
    page,
  }) => {
    await page.route("**/api/prom/api/v1/query_range*", async (route) => {
      const now = Math.floor(Date.now() / 1000);
      const query = new URL(route.request().url()).searchParams.get("query");
      const body = (query ?? "").includes("lantern_search_")
        ? { status: "success", data: { resultType: "matrix", result: [] } }
        : matrixEnvelope(now);
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(body),
      });
    });

    await page.goto("/ops");
    await expect(
      page.getByTestId("ops-metric-search-successes-empty"),
    ).toBeVisible();
    await expect(page.getByTestId("ops-metrics-degraded")).toHaveCount(0);
    await expect(
      page.getByTestId("ops-metric-cache-size-summary"),
    ).toBeVisible();
  });

  test("keeps status and metric sections readable on a narrow viewport", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/ops");
    await expect(page.getByTestId("ops-search-card")).toBeVisible();
    await expect(
      page.getByText("Query budgets", { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("heading", { level: 3, name: "Search requests" }),
    ).toBeVisible();
    await expect(
      page.getByRole("heading", { level: 4, name: "Search successes" }),
    ).toBeVisible();
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - window.innerWidth,
    );
    expect(overflow).toBeLessThanOrEqual(0);
  });

  test("shows the replica key in per-replica mode and hides it in sum mode", async ({
    page,
  }) => {
    // The mock branches on the composed query: per-replica queries group by
    // `instance` (so series carry an instance label and the replica key
    // appears); cluster-sum queries do not (so the key self-hides).
    await page.route("**/api/prom/api/v1/query_range*", async (route) => {
      const now = Math.floor(Date.now() / 1000);
      const query = new URL(route.request().url()).searchParams.get("query");
      const body = (query ?? "").includes("instance")
        ? multiInstanceEnvelope(now, [
            "10.0.0.1:9090",
            "10.0.0.2:9090",
            "10.0.0.3:9090",
          ])
        : matrixEnvelope(now);
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(body),
      });
    });

    await page.goto("/ops");
    // Per-replica is the default: the key maps each alias to its instance.
    const key = page.getByTestId("ops-metrics-replica-key");
    await expect(key).toBeVisible();
    await expect(key).toContainText("r0");
    await expect(key).toContainText("10.0.0.1:9090");
    await expect(key).toContainText("r2");
    await expect(key).toContainText("10.0.0.3:9090");

    // Switching to cluster-sum drops the instance grouping; series lose their
    // instance label and the key disappears.
    await page
      .getByTestId("ops-metrics-agg-mode")
      .getByRole("tab", { name: "Sum" })
      .click();
    await expect(key).toHaveCount(0);
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
