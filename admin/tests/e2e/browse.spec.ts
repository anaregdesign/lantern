import { expect, test } from "@playwright/test";

import { CONNECT_URL, STORAGE_KEY, putEdges, putVertices } from "./helpers";

/**
 * Seeds a small graph for the Browse screen:
 *
 *   - 3 vertices under prefix `e2e:vertex:`
 *   - 1 vertex under prefix `e2e:other:` (must NOT appear when filtering)
 *   - 2 edges `e2e:vertex:a → e2e:vertex:b` and `e2e:vertex:a → e2e:vertex:c`
 *
 * The seed runs against the additive Connect listener started by
 * Playwright's webServer (#337 / #339).
 */
test.beforeAll(async () => {
  await seed();
});

async function seed() {
  await putVertices([
    { key: "e2e:vertex:a", string: "alpha" },
    { key: "e2e:vertex:b", int32: 42 },
    { key: "e2e:vertex:c", bool: true },
    { key: "e2e:other:z", string: "ignored" },
  ]);
  await putEdges([
    { tail: "e2e:vertex:a", head: "e2e:vertex:b", weight: 1 },
    { tail: "e2e:vertex:a", head: "e2e:vertex:c", weight: 2 },
    { tail: "e2e:other:z", head: "e2e:vertex:b", weight: 9 },
  ]);
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript(
    ({ key, value }) => {
      try {
        window.localStorage.setItem(key, value);
      } catch {
        // ignore — storage may be unavailable in private mode
      }
    },
    { key: STORAGE_KEY, value: CONNECT_URL },
  );
});

test.describe("/vertices", () => {
  test("filters by prefix and renders typed values", async ({ page }) => {
    await page.goto("/vertices");

    await page.getByTestId("vertex-prefix-input").fill("e2e:vertex:");

    const table = page.getByTestId("vertices-table");
    await expect(table).toBeVisible();
    await expect(
      table.getByRole("link", { name: "e2e:vertex:a" }),
    ).toBeVisible();
    await expect(
      table.getByRole("link", { name: "e2e:vertex:b" }),
    ).toBeVisible();
    await expect(
      table.getByRole("link", { name: "e2e:vertex:c" }),
    ).toBeVisible();

    // The ignored prefix bucket MUST NOT leak in.
    await expect(table.getByRole("link", { name: "e2e:other:z" })).toHaveCount(
      0,
    );

    // CountVerticesByPrefix should populate the count badge with at
    // least 3 (the seed) — eventually consistent because the count RPC
    // races the scan.
    await expect(page.getByTestId("vertex-count-badge")).toContainText(/\d/);
  });

  test("Illuminate action navigates to the Illuminate screen seeded with the row key", async ({
    page,
  }) => {
    await page.goto("/vertices");
    await page.getByTestId("vertex-prefix-input").fill("e2e:vertex:");

    const table = page.getByTestId("vertices-table");
    const illuminate = table
      .getByRole("button", { name: /Illuminate from e2e:vertex:a/i })
      .first();
    await expect(illuminate).toBeVisible();
    await illuminate.click();
    await expect(page).toHaveURL(/\/illuminate\?seed=e2e%3Avertex%3Aa/);
  });
});

test.describe("/edges", () => {
  test("filters by tail prefix", async ({ page }) => {
    await page.goto("/edges");
    await page.getByTestId("edge-tail-prefix-input").fill("e2e:vertex:");

    const table = page.getByTestId("edges-table");
    await expect(table).toBeVisible();
    // Both seeded edges with tail under `e2e:vertex:` should be present.
    await expect(table.getByRole("link", { name: "e2e:vertex:b" })).toHaveCount(
      1,
    );
    await expect(table.getByRole("link", { name: "e2e:vertex:c" })).toHaveCount(
      1,
    );
    // The edge with tail `e2e:other:z` must NOT leak in.
    await expect(table.getByRole("link", { name: "e2e:other:z" })).toHaveCount(
      0,
    );
  });

  test("filters by combined tail+head prefix", async ({ page }) => {
    await page.goto("/edges");
    await page.getByTestId("edge-tail-prefix-input").fill("e2e:vertex:");
    await page.getByTestId("edge-head-prefix-input").fill("e2e:vertex:b");

    const table = page.getByTestId("edges-table");
    await expect(table).toBeVisible();
    await expect(table.getByRole("link", { name: "e2e:vertex:b" })).toHaveCount(
      1,
    );
    await expect(table.getByRole("link", { name: "e2e:vertex:c" })).toHaveCount(
      0,
    );
  });
});
