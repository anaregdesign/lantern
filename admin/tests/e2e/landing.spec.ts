import { expect, test } from "@playwright/test";

test("landing page renders with navigation and gateway connection", async ({
  page,
}) => {
  await page.goto("/");
  await expect(
    page.getByRole("heading", { level: 1, name: "Lantern Admin" }),
  ).toBeVisible();
  await expect(page.getByRole("link", { name: /Open Browse/i })).toBeVisible();
  await expect(
    page.getByRole("link", { name: /Open Illuminate/i }),
  ).toBeVisible();
  await expect(page.getByRole("link", { name: /Open Ops/i })).toBeVisible();
  await expect(page.getByText("http://localhost:6381")).toBeVisible();
});

test.describe("placeholder routes", () => {
  test("Browse route renders its placeholder", async ({ page }) => {
    await page.goto("/browse");
    await expect(
      page.getByRole("heading", { level: 1, name: "Browse" }),
    ).toBeVisible();
    await expect(page.getByText(/Coming in #F2/)).toBeVisible();
  });

  test("Illuminate route renders its placeholder", async ({ page }) => {
    await page.goto("/illuminate");
    await expect(
      page.getByRole("heading", { level: 1, name: "Illuminate" }),
    ).toBeVisible();
    await expect(page.getByText(/Coming in #F4/)).toBeVisible();
  });

  test("Ops route renders its placeholder", async ({ page }) => {
    await page.goto("/ops");
    await expect(
      page.getByRole("heading", { level: 1, name: "Ops" }),
    ).toBeVisible();
    await expect(page.getByText(/Coming in #F5/)).toBeVisible();
  });
});
