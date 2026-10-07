import { expect, test } from "@playwright/test";
import { securityUI } from "./helpers";

for (const width of [1280, 390]) {
  test(`Server Role scope selection clears old rows and scopes generated traversal at ${width}px`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    const allow = (action: string, prefix: string) => ({
      id: action + prefix,
      action: "SECURITY_ACTION_" + action,
      effect: "SECURITY_EFFECT_ALLOW",
      prefix,
    });
    const fixture = await securityUI(page, {
      roles: [
        {
          id: "scoped",
          rules: [
            allow("VERTEX_READ", "tenant:"),
            allow("VERTEX_READ", "audit:"),
            allow("QUERY", "tenant:"),
            {
              ...allow("VERTEX_READ", "tenant:private:"),
              effect: "SECURITY_EFFECT_DENY",
            },
          ],
        },
      ],
    });
    const calls: Array<{ method: string; prefix: string }> = [];
    await page.route(
      `${fixture.primary}/browser/graph.v1.LanternService/**`,
      async (route) => {
        const method = new URL(route.request().url()).pathname
          .split("/")
          .at(-1)!;
        const body = route.request().postDataJSON();
        calls.push({ method, prefix: body.prefix ?? body.vertexPrefix ?? "" });
        const vertices = [
          {
            key: body.prefix === "audit:" ? "audit:visible" : "tenant:visible",
            string: "visible",
          },
        ];
        await route.fulfill({
          contentType: "application/json",
          body: JSON.stringify(
            method === "ScanVertices"
              ? { vertices }
              : method === "CountVerticesByPrefix"
                ? { count: "1" }
                : {},
          ),
        });
      },
    );
    await page.goto(`${fixture.primary}/vertices`);
    await expect(page.getByTestId("data-scope-select")).toBeVisible();
    await expect(page.getByTestId("scope-deny-exceptions")).toContainText(
      "tenant:private:",
    );
    await page.getByTestId("data-scope-select").selectOption("audit:");
    await expect(
      page.getByRole("table", { name: "Vertices", exact: true }),
    ).toContainText("audit:visible");
    await expect(
      page.getByRole("table", { name: "Vertices", exact: true }),
    ).not.toContainText("tenant:visible");
    await page
      .getByRole("tab", { name: "Content search", exact: true })
      .click();
    await expect(
      page.getByTestId("data-scope-select").locator("option[value='audit:']"),
    ).toHaveText("audit: (explicit prefix)");
    await page.getByTestId("data-scope-select").selectOption("tenant:");
    await expect(
      page.getByRole("tab", { name: "Content search", exact: true }),
    ).toHaveAttribute("aria-selected", "true");
    await expect(
      page.getByLabel("Key namespace prefix", { exact: true }),
    ).toHaveValue("tenant:");
    await page.goto(`${fixture.primary}/cli`);
    await page.getByTestId("data-scope-select").selectOption("tenant:");
    await expect(page.locator("#cli-axis-picker-preview")).toContainText(
      "prefix=tenant:",
    );
    await expect(page.getByTestId("cli-axis-prefix")).toHaveAttribute(
      "readonly",
      "",
    );
    const prompt = page.getByTestId("cli-input");
    await prompt.fill("scan vertices outside:");
    await prompt.press("Enter");
    await expect
      .poll(
        () => calls.filter((c) => c.method === "ScanVertices").at(-1)?.prefix,
      )
      .toBe("outside:");
    await page.getByTestId("data-scope-select").selectOption("");
    await expect(page.getByTestId("cli-scrollback")).not.toContainText(
      "scan vertices outside:",
    );
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: testInfo.outputPath(`scope-${width}.png`),
      fullPage: true,
    });
  });
}

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
