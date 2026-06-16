import { expect, test } from "@playwright/test";

import { CONNECT_URL, STORAGE_KEY, putVertices } from "./helpers";

/**
 * Seeds a small corpus for the Search screen (#627). The string values
 * carry deliberately rare tokens (`zorptangle`, `quibblefrost`) so a
 * keyword query matches exactly the seeded vertices and nothing the other
 * specs write to the shared server instance.
 *
 *   - 2 vertices contain `zorptangle`
 *   - 1 vertex contains `quibblefrost`
 *
 * Content search is opt-out (enabled by default); the Playwright webServer
 * pins `LANTERN_SEARCH_ENABLED=true` for determinism.
 */
test.beforeAll(async () => {
  await seed();
});

async function seed() {
  await putVertices([
    { key: "e2e:search:doc1", string: "zorptangle distributed consensus" },
    { key: "e2e:search:doc2", string: "zorptangle vector clocks" },
    { key: "e2e:search:doc3", string: "quibblefrost unrelated content" },
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

test.describe("/search", () => {
  test("prompts for a query before anything is typed", async ({ page }) => {
    await page.goto("/search");

    await expect(page.getByTestId("search-idle")).toBeVisible();
    await expect(page.getByTestId("search-results-table")).toHaveCount(0);
  });

  test("ranks the strongest content matches to the top", async ({ page }) => {
    await page.goto("/search");
    await page.getByTestId("search-query-input").fill("zorptangle");

    const table = page.getByTestId("search-results-table");
    await expect(table).toBeVisible();

    // The index is substring/fuzzy (n-gram BM25), so a vertex can match on a
    // stray shared n-gram. doc1 and doc2 carry the full `zorptangle` token,
    // so they always outrank that noise and occupy the top two ranks.
    await expect(
      table.getByRole("link", { name: "e2e:search:doc1" }),
    ).toBeVisible();
    await expect(
      table.getByRole("link", { name: "e2e:search:doc2" }),
    ).toBeVisible();

    const keyLinks = table.locator("tbody a");
    await expect(keyLinks.nth(0)).toHaveText(/e2e:search:doc[12]/);
    await expect(keyLinks.nth(1)).toHaveText(/e2e:search:doc[12]/);

    // Each ranked hit shows a relevance score, and the caption summarises the
    // match count.
    await expect(page.getByTestId("search-score").first()).toContainText(
      /\d\.\d{3}/,
    );
    await expect(page.getByTestId("search-caption")).toContainText(/result/);
  });

  test("Illuminate action seeds the Illuminate screen with the hit key", async ({
    page,
  }) => {
    await page.goto("/search");
    await page.getByTestId("search-query-input").fill("quibblefrost");

    const table = page.getByTestId("search-results-table");
    const illuminate = table
      .getByRole("button", { name: /Illuminate from e2e:search:doc3/i })
      .first();
    await expect(illuminate).toBeVisible();
    await illuminate.click();
    await expect(page).toHaveURL(/\/illuminate\?seed=e2e%3Asearch%3Adoc3/);
  });
});
