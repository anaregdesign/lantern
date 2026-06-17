import { expect, test } from "@playwright/test";

test("landing page renders with navigation and gateway connection", async ({
  page,
}) => {
  await page.goto("/");
  await expect(
    page.getByRole("heading", { level: 1, name: "Lantern Admin" }),
  ).toBeVisible();
  await expect(
    page.getByRole("link", { name: /Open Vertices/i }),
  ).toBeVisible();
  await expect(page.getByRole("link", { name: /Open Edges/i })).toBeVisible();
  await expect(page.getByRole("link", { name: /Open Ops/i })).toBeVisible();
  // #439 — landing tile + AppShell nav now expose /cli alongside the
  // other top-level sections.
  await expect(page.getByRole("link", { name: /Open CLI/i })).toBeVisible();
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
