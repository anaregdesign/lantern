import { expect, test } from "@playwright/test";
import { securityUI } from "./helpers";

test("OFF shows setup guidance without security mutation controls", async ({
  page,
}) => {
  const fixture = await securityUI(page, { mode: "off" });
  await page.goto(`${fixture.primary}/security/issuers`);
  await expect(
    page.getByText("Authentication is off.", { exact: false }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Review Issuer change" }),
  ).toHaveCount(0);
  expect(
    await page.evaluate(() => localStorage.getItem("lantern.admin.authToken")),
  ).toBeNull();
});
test("login fails closed", async ({ page }) => {
  const fixture = await securityUI(page, { mode: "login" });
  await page.goto(`${fixture.primary}/security/users`);
  await expect(
    page.getByRole("button", { name: "Sign in with Example" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Review user state" }),
  ).toHaveCount(0);
});
test("unavailable capabilities do not render data or OFF controls", async ({
  page,
}) => {
  const fixture = await securityUI(page, { mode: "unavailable" });
  await page.goto(`${fixture.primary}/security/roles`);
  await expect(
    page.getByText("Authentication service is unavailable."),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Review Role change" }),
  ).toHaveCount(0);
});
test("logout clears protected views", async ({ page }) => {
  const fixture = await securityUI(page);
  await page.goto(`${fixture.primary}/security/roles`);
  await expect(
    page.getByRole("button", { name: "Review Role change" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(
    page.getByRole("button", { name: "Sign in with Example" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Review Role change" }),
  ).toHaveCount(0);
});
