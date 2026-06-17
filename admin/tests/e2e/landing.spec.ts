import { expect, test } from "@playwright/test";

test("landing page renders with navigation and gateway connection", async ({
  page,
}) => {
  await page.goto("/");
  await expect(
    page.getByRole("heading", { level: 1, name: "Lantern Admin" }),
  ).toBeVisible();
  // The Home tiles mirror the three nav surfaces (#655): Data / CLI / Ops.
  // Vertices + Edges collapsed into the single Data tile (#650); the
  // Illuminate explorer folded into CLI (#651).
  await expect(page.getByRole("link", { name: /Open Data/i })).toBeVisible();
  // #439 — landing tile + AppShell nav expose /cli alongside the other
  // top-level sections.
  await expect(page.getByRole("link", { name: /Open CLI/i })).toBeVisible();
  await expect(page.getByRole("link", { name: /Open Ops/i })).toBeVisible();
  await expect(page.getByText("http://localhost:6380")).toBeVisible();
});

test.describe("placeholder routes", () => {
  test("Ops route renders the server status card", async ({ page }) => {
    await page.goto("/ops");
    await expect(
      page.getByRole("heading", { level: 1, name: "Ops" }),
    ).toBeVisible();
    // Both cards mount; the server-status card is the canonical
    // "did the page wire up" probe because GetServerStatus does not
    // require replication to be configured.
    await expect(page.getByTestId("ops-server-card")).toBeVisible();
    await expect(page.getByTestId("ops-replication-card")).toBeVisible();
  });
});
