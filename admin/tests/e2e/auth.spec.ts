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
for (const width of [1280, 390]) {
  test(`ordinary session without recent evidence offers explicit step-up at ${width}px`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    const fixture = await securityUI(page, { recent: false });
    await page.goto(`${fixture.primary}/security/roles`);
    await expect(page.getByRole("button", { name: "Sign out" })).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Verify identity" }),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Sign in with Example" }),
    ).toHaveCount(0);
    await expect(
      page.getByRole("button", { name: "Review Role change" }),
    ).toBeVisible();
    expect(fixture.calls.some((call) => call.method === "ListRoles")).toBe(
      true,
    );
    expect(
      fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
    ).toHaveLength(0);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: testInfo.outputPath(`ordinary-session-${width}.png`),
      fullPage: true,
    });
    // Observe the rendered controller's redirect without contacting an IdP.
    await page.route(`${fixture.primary}/auth/login?**`, (route) =>
      route.fulfill({
        status: 200,
        contentType: "text/plain",
        body: "Step-up",
      }),
    );
    const redirect = page.waitForRequest(
      (request) => new URL(request.url()).pathname === "/auth/login",
    );
    await page.getByRole("button", { name: "Verify identity" }).click();
    const url = new URL((await redirect).url());
    expect(url.origin).toBe(fixture.primary);
    expect(url.searchParams.get("issuer")).toBe("https://idp.example");
    expect(url.searchParams.get("step_up")).toBe("true");
    expect(url.searchParams.get("return")).toBe("/");
  });
}
