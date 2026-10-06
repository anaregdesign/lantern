import { expect, test, type Page, type Route } from "@playwright/test";
import { securityUI } from "./helpers";

/** Rendered Connect/SDK fixture; real OIDC authority has a separate wire test. */
async function browseFixture(
  page: Page,
  respond: (method: string, prefix: string, route: Route) => Promise<void>,
) {
  const fixture = await securityUI(page);
  await page.route(
    `${fixture.primary}/browser/graph.v1.LanternService/*`,
    async (route) => {
      const method = new URL(route.request().url()).pathname.split("/").at(-1)!;
      const body = route.request().postDataJSON() as { prefix?: string };
      if (method === "GetServerStatus") return json(route, {});
      return respond(method, body.prefix ?? "", route);
    },
  );
  await page.goto(`${fixture.primary}/vertices`);
  return fixture;
}

function json(route: Route, body: unknown, status = 200) {
  return route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
}
function success(route: Route, method: string, prefix: string, count = 1) {
  return json(
    route,
    method === "ScanVertices"
      ? {
          vertices:
            count === 0 ? [] : [{ key: `${prefix}a`, string: "visible" }],
        }
      : { count: String(count) },
  );
}
function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

for (const width of [1280, 390]) {
  test(`denied scan/count guide read access and authorized prefix recovers at ${width}px`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    await browseFixture(page, async (method, prefix, route) => {
      if (prefix !== "tenant:")
        return json(
          route,
          { code: "permission_denied", message: "opaque diagnostic" },
          403,
        );
      return success(route, method, prefix);
    });
    const guidance = page.getByTestId("vertex-read-permission");
    await expect(guidance).toContainText("Vertex read permission required");
    await expect(guidance).toContainText("all prefixes");
    await expect(
      guidance.getByRole("link", { name: "Roles", exact: true }),
    ).toHaveAttribute("href", "/security/roles");
    await expect(page.getByTestId("vertex-count-status")).toHaveText(
      "Count denied",
    );
    await expect(page.getByTestId("vertex-count-badge")).toHaveCount(0);
    await expect(page.getByTestId("vertices-empty")).toHaveCount(0);
    await page.screenshot({
      path: testInfo.outputPath(`denied-${width}.png`),
      fullPage: true,
      animations: "disabled",
    });

    await page.getByTestId("vertex-prefix-input").fill("private:");
    await expect(guidance).toContainText("prefix private:");
    await expect(page.getByTestId("vertex-count-status")).toHaveText(
      "Count denied",
    );

    await page.getByTestId("vertex-prefix-input").fill("tenant:");
    await expect(
      page.getByRole("link", { name: "tenant:a", exact: true }),
    ).toBeVisible();
    await expect(page.getByTestId("vertex-count-badge")).toHaveText(
      "1 vertices",
    );
    await expect(page.getByTestId("pager-page")).toHaveText("Page 1 of 1");
    await expect(guidance).toHaveCount(0);
    await expect(page.getByTestId("vertex-count-status")).toHaveCount(0);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: testInfo.outputPath(`authorized-${width}.png`),
      fullPage: true,
      animations: "disabled",
    });
  });

  for (const code of ["permission_denied", "unavailable"] as const) {
    test(`successful page survives ${code} count and Refresh recovers at ${width}px`, async ({
      page,
    }, testInfo) => {
      await page.setViewportSize({ width, height: 900 });
      let fail = true;
      const release = deferred();
      await browseFixture(page, async (method, prefix, route) => {
        if (method === "ScanVertices") return success(route, method, prefix);
        if (fail)
          return json(
            route,
            { code, message: "opaque diagnostic" },
            code === "permission_denied" ? 403 : 503,
          );
        await release.promise;
        return success(route, method, prefix, 101);
      });
      await expect(
        page.getByRole("link", { name: "a", exact: true }),
      ).toBeVisible();
      await expect(page.getByTestId("vertex-count-status")).toHaveText(
        code === "permission_denied" ? "Count denied" : "Count unavailable",
      );
      await expect(page.getByTestId("vertex-count-badge")).toHaveCount(0);
      await expect(page.getByTestId("pager-page")).toHaveText("Page 1");
      await expect(
        page.getByTestId(
          code === "permission_denied"
            ? "vertex-read-permission"
            : "vertex-count-error",
        ),
      ).toBeVisible();
      await page.screenshot({
        path: testInfo.outputPath(`count-${code}-${width}.png`),
        fullPage: true,
        animations: "disabled",
      });
      fail = false;
      await page.getByRole("button", { name: "Refresh", exact: true }).click();
      await expect(page.getByTestId("vertex-count-status")).toHaveText(
        "Counting vertices…",
      );
      await expect(page.getByTestId("vertex-read-permission")).toHaveCount(0);
      await expect(page.getByTestId("vertex-count-error")).toHaveCount(0);
      release.resolve();
      await expect(page.getByTestId("vertex-count-badge")).toHaveText(
        "101 vertices",
      );
      await expect(page.getByTestId("pager-page")).toHaveText("Page 1 of 3");
      await expect(
        page.getByRole("link", { name: "a", exact: true }),
      ).toBeVisible();
    });
  }

  test(`successful zero is an empty result at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await browseFixture(page, async (method, prefix, route) =>
      success(route, method, prefix, 0),
    );
    await expect(page.getByTestId("vertex-count-badge")).toHaveText(
      "0 vertices",
    );
    await expect(page.getByTestId("vertices-empty")).toBeVisible();
    await expect(page.getByTestId("pager-page")).toHaveText("Page 1 of 1");
    await expect(page.getByTestId("vertex-read-permission")).toHaveCount(0);
  });
}

test("denied scan receives read guidance when count succeeds independently", async ({
  page,
}) => {
  await browseFixture(page, async (method, prefix, route) =>
    method === "ScanVertices"
      ? json(
          route,
          { code: "permission_denied", message: "opaque diagnostic" },
          403,
        )
      : success(route, method, prefix),
  );
  await expect(page.getByTestId("vertex-read-permission")).toBeVisible();
  await expect(page.getByTestId("vertex-count-badge")).toHaveText("1 vertices");
  await expect(page.getByTestId("vertices-empty")).toHaveCount(0);
});

for (const late of ["success", "denied", "unavailable"] as const) {
  test(`late ${late} scan/count responses cannot overwrite an authorized prefix`, async ({
    page,
  }) => {
    const arrived = deferred();
    const release = deferred();
    let waiting = 0;
    const completed = deferred();
    let settled = 0;
    await browseFixture(page, async (method, prefix, route) => {
      if (prefix !== "old:") return success(route, method, prefix);
      if (++waiting === 2) arrived.resolve();
      await release.promise;
      const response =
        late === "success"
          ? success(route, method, prefix, 999)
          : json(
              route,
              {
                code: late === "denied" ? "permission_denied" : "unavailable",
                message: "stale diagnostic",
              },
              late === "denied" ? 403 : 503,
            );
      // Prefix cancellation may have already closed the old browser request.
      await response.catch(() => undefined);
      if (++settled === 2) completed.resolve();
    });
    await page.getByTestId("vertex-prefix-input").fill("old:");
    await arrived.promise;
    await expect(page.getByTestId("vertex-count-status")).toHaveText(
      "Counting vertices…",
    );
    await expect(page.getByTestId("vertex-count-badge")).toHaveCount(0);
    await page.getByTestId("vertex-prefix-input").fill("tenant:");
    await expect(
      page.getByRole("link", { name: "tenant:a", exact: true }),
    ).toBeVisible();
    await expect(page.getByTestId("vertex-count-badge")).toHaveText(
      "1 vertices",
    );
    release.resolve();
    await completed.promise;
    await expect(page.getByTestId("vertex-count-badge")).toHaveText(
      "1 vertices",
    );
    await expect(
      page.getByRole("link", { name: "old:a", exact: true }),
    ).toHaveCount(0);
    await expect(page.getByTestId("vertex-read-permission")).toHaveCount(0);
    await expect(page.getByTestId("vertex-count-error")).toHaveCount(0);
    await expect(page.getByTestId("vertex-scan-error")).toHaveCount(0);
  });
}
