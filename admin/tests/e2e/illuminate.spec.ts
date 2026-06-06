import { expect, test } from "@playwright/test";

const GATEWAY_URL =
  process.env.LANTERN_E2E_GATEWAY_URL ?? "http://127.0.0.1:6381";
const STORAGE_KEY = "lantern.admin.baseUrl";

/**
 * Seeds a tiny star graph centred on `e2e:illum:hub` so the Illuminate
 * screen has at least one neighbour to render. The other tests that share
 * this gateway also seed under `e2e:vertex:*`; we use a distinct prefix
 * to keep the assertions tight.
 */
test.beforeAll(async () => {
  const put = await fetch(`${GATEWAY_URL}/v1/vertices`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      vertices: [
        { key: "e2e:illum:hub", value: { string: "hub" } },
        { key: "e2e:illum:left", value: { int32: 1 } },
        { key: "e2e:illum:right", value: { int32: 2 } },
      ],
    }),
  });
  if (!put.ok) {
    throw new Error(`seed vertices failed: ${put.status} ${await put.text()}`);
  }
  const putEdges = await fetch(`${GATEWAY_URL}/v1/edges/put`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      edges: [
        { tail: "e2e:illum:hub", head: "e2e:illum:left", weight: 1 },
        { tail: "e2e:illum:hub", head: "e2e:illum:right", weight: 3 },
      ],
    }),
  });
  if (!putEdges.ok) {
    throw new Error(
      `seed edges failed: ${putEdges.status} ${await putEdges.text()}`,
    );
  }
});

test.beforeEach(async ({ page }) => {
  await page.addInitScript(
    ({ key, value }) => {
      try {
        window.localStorage.setItem(key, value);
      } catch {
        // Storage may be unavailable in some browser modes.
      }
    },
    { key: STORAGE_KEY, value: GATEWAY_URL },
  );
});

test.describe("/illuminate", () => {
  test("shows the seed prompt when no ?seed= is present", async ({ page }) => {
    await page.goto("/illuminate");
    await expect(page.getByTestId("illuminate-seed-prompt")).toBeVisible();
    await expect(page.getByTestId("illuminate-open")).toBeDisabled();
  });

  test("renders the canvas and neighbour table for a seed", async ({
    page,
  }) => {
    const seed = encodeURIComponent("e2e:illum:hub");
    await page.goto(`/illuminate?seed=${seed}`);

    await expect(page.getByTestId("illuminate-toolbar")).toBeVisible();
    await expect(page.getByTestId("illuminate-seed")).toHaveText(
      "e2e:illum:hub",
    );
    await expect(page.getByTestId("illuminate-canvas")).toBeVisible();

    // Pop is disabled until the user pushes a second seed onto the stack.
    await expect(page.getByTestId("illuminate-pop")).toBeDisabled();

    // Refresh should be enabled once the seed is set.
    await expect(page.getByTestId("illuminate-refresh")).toBeEnabled();

    // The disclosure summary reflects the live frame counts. Wait for the
    // fetch to land — the count goes from 0/0 to 3/2 — before clicking
    // so we don't race the React re-render and end up with a stale node.
    const summary = page.getByRole("group").getByText(/List view \(3 vertices/);
    await expect(summary).toBeVisible();
    await summary.click();

    const table = page.getByTestId("illuminate-table");
    await expect(table).toBeVisible();
    await expect(
      table.getByRole("link", { name: "e2e:illum:hub" }),
    ).toBeVisible();
    await expect(
      table.getByRole("link", { name: "e2e:illum:left" }),
    ).toBeVisible();
    await expect(
      table.getByRole("link", { name: "e2e:illum:right" }),
    ).toBeVisible();
  });

  test("Re-seed from the list view pushes a new seed and enables Pop", async ({
    page,
  }) => {
    const seed = encodeURIComponent("e2e:illum:hub");
    await page.goto(`/illuminate?seed=${seed}`);
    await expect(page.getByTestId("illuminate-toolbar")).toBeVisible();

    // Wait for the seed's frame to land before opening the disclosure.
    const summary = page.getByRole("group").getByText(/List view \(3 vertices/);
    await expect(summary).toBeVisible();
    await summary.click();

    const table = page.getByTestId("illuminate-table");
    await table
      .getByRole("button", { name: "Re-seed from e2e:illum:left" })
      .click();

    await expect(page.getByTestId("illuminate-seed")).toHaveText(
      "e2e:illum:left",
    );
    await expect(page).toHaveURL(/\?seed=e2e%3Aillum%3Aleft/);
    await expect(page.getByTestId("illuminate-pop")).toBeEnabled();

    await page.getByTestId("illuminate-pop").click();
    await expect(page.getByTestId("illuminate-seed")).toHaveText(
      "e2e:illum:hub",
    );
    await expect(page.getByTestId("illuminate-pop")).toBeDisabled();
  });
});
