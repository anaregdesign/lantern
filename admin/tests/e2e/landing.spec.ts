import { expect, test } from "@playwright/test";
import { CONNECT_URL, STORAGE_KEY } from "./helpers";

test.beforeEach(async ({ page }) => {
  await page.addInitScript(
    ({ key, url }) => {
      if (!localStorage.getItem(key)) localStorage.setItem(key, url);
    },
    { key: STORAGE_KEY, url: CONNECT_URL },
  );
});

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
  await expect(page.getByText(CONNECT_URL)).toBeVisible();
});

test("a fresh browser selects its same-origin Gateway", async ({ page }) => {
  await page.addInitScript((key) => localStorage.removeItem(key), STORAGE_KEY);
  await page.goto("/");
  const origin = new URL(page.url()).origin;
  await expect(
    page.getByRole("button", { name: "Change gateway connection" }),
  ).toContainText(origin);
  await expect
    .poll(() => page.evaluate((key) => localStorage.getItem(key), STORAGE_KEY))
    .toBe(origin);
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

for (const width of [1280, 390]) {
  test(`Gateway changes stay drafts until saved at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.goto("/");
    const saved = () =>
      page.evaluate((key) => localStorage.getItem(key), STORAGE_KEY);
    const switcher = page.getByRole("button", {
      name: "Change gateway connection",
    });
    const dialog = page.getByRole("dialog", {
      name: "Lantern gateway connection",
    });
    const input = dialog.getByRole("textbox", { name: "Base URL" });
    const defaultUrl = new URL(page.url()).origin;

    await switcher.click();
    await dialog.getByRole("button", { name: "Reset to default" }).click();
    await expect(input).toHaveValue(defaultUrl);
    await expect(dialog.getByRole("status")).toContainText("Unsaved changes");
    expect(await saved()).toBe(CONNECT_URL);
    await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
    await expect(switcher).toContainText(CONNECT_URL);
    await page.reload();
    await expect(switcher).toContainText(CONNECT_URL);

    await switcher.click();
    await input.fill("invalid gateway");
    await input.press("Enter");
    await expect(
      dialog.getByText("Enter an http(s) URL like http://localhost:6380"),
    ).toBeVisible();
    expect(await saved()).toBe(CONNECT_URL);
    await dialog.getByRole("button", { name: "Reset to default" }).click();
    await expect(
      dialog.getByRole("button", { name: "Save changes" }),
    ).toBeInViewport();
    await input.press("Enter");
    await expect(dialog).toHaveCount(0);
    await expect(switcher).toContainText(defaultUrl);
    await expect.poll(saved).toBe(defaultUrl);
    await page.reload();
    await expect(switcher).toContainText(defaultUrl);
  });
}
