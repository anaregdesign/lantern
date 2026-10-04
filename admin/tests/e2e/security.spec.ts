import { expect, test } from "@playwright/test";
import { securityUI } from "./helpers";

async function reviewRole(page: import("@playwright/test").Page) {
  await page
    .getByRole("textbox", { name: "Role ID", exact: true })
    .fill("scoped-reader");
  await page
    .getByRole("textbox", { name: "Role name", exact: true })
    .fill("Scoped reader");
  await page.getByRole("button", { name: "Add rule", exact: true }).click();
  await page
    .getByLabel("Rule 1 logical prefix", { exact: true })
    .fill("tenant:");
  await page
    .getByRole("button", { name: "Review Role change", exact: true })
    .click();
}
for (const width of [1280, 390]) {
  test(`Directed pair review and Server explanation at ${width}px`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    const fixture = await securityUI(page);
    await page.goto(`${fixture.primary}/security/roles`);
    await page
      .getByRole("textbox", { name: "Role ID", exact: true })
      .fill("connection-creator");
    await page
      .getByRole("textbox", { name: "Role name", exact: true })
      .fill("Connection creator");
    await page.getByRole("button", { name: "Add rule", exact: true }).click();
    await page
      .getByLabel("Rule 1 action", { exact: true })
      .selectOption({ label: "Create connections" });
    await expect(
      page.getByLabel("Rule 1 selector", { exact: true }),
    ).toBeDisabled();
    await page
      .getByLabel("Rule 1 tail prefix", { exact: true })
      .fill("users:alice:");
    await page
      .getByLabel("Rule 1 head prefix", { exact: true })
      .fill("profiles:");
    await page
      .getByRole("button", { name: "Review Role change", exact: true })
      .click();
    await page
      .getByRole("button", { name: "Apply reviewed change", exact: true })
      .click();
    await expect(
      page.getByText("Change committed.", { exact: false }),
    ).toBeVisible();
    const call = fixture.calls.find(
      (call) => call.method === "ApplySecurityChanges",
    )!;
    const changes = call.body.changes as { putRole: { rules: unknown[] } }[];
    expect(changes[0].putRole.rules).toEqual([
      {
        id: "rule-1",
        effect: "SECURITY_EFFECT_ALLOW",
        action: "SECURITY_ACTION_EDGE_CREATE",
        pair: { tailPrefix: "users:alice:", headPrefix: "profiles:" },
      },
    ]);
    await page.getByRole("button", { name: "Reload security state" }).click();
    await page.getByLabel("Explanation Issuer").fill("https://idp.example");
    await page.getByLabel("Explanation subject").fill("alice");
    await page
      .getByLabel("Explanation action")
      .selectOption({ label: "Create connections" });
    await page.getByLabel("Explanation tail").fill("users:alice:connections");
    await page.getByLabel("Explanation head").fill("profiles:bob");
    await page.getByRole("button", { name: "Ask Server" }).click();
    await expect(page.getByText("Denied · Server revision 3")).toBeVisible();
    const explain = fixture.calls.find(
      (call) => call.method === "ExplainAccess",
    )!;
    expect(explain.body.edge).toEqual({
      tail: "users:alice:connections",
      head: "profiles:bob",
    });
    expect(explain.body.logicalKey).toBeUndefined();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: testInfo.outputPath(`pair-${width}.png`),
      fullPage: true,
    });
  });
  test(`Role review, Server explanation and status at ${width}px`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    const fixture = await securityUI(page);
    await page.goto(`${fixture.primary}/security/roles`);
    await reviewRole(page);
    await expect(
      page.getByRole("region", { name: "Review security change" }),
    ).toBeVisible();
    await page
      .getByRole("button", { name: "Apply reviewed change", exact: true })
      .click();
    await expect(
      page.getByText("Change committed.", { exact: false }),
    ).toBeVisible();
    const sent = fixture.calls.find(
      (call) => call.method === "ApplySecurityChanges",
    )!;
    expect(sent.body.expectedRevision).toBe("3");
    expect(sent.csrf).toBe("c".repeat(43));
    expect(sent.authorization).toBeUndefined();
    await page
      .getByRole("button", { name: "Check original change status" })
      .click();
    await expect(
      page.getByText("Change enforced.", { exact: true }),
    ).toBeVisible();
    await page.getByRole("button", { name: "Reload security state" }).click();
    await page.getByLabel("Explanation Issuer").fill("https://idp.example");
    await page.getByLabel("Explanation subject").fill("alice");
    await page
      .getByLabel("Logical key", { exact: true })
      .fill("tenant:private:one");
    await page.getByRole("button", { name: "Ask Server" }).click();
    await expect(page.getByText("Denied · Server revision 3")).toBeVisible();
    await expect(page.getByText("private / hide: Deny")).toBeVisible();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    ).toBe(true);
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.screenshot({
      path: testInfo.outputPath(`security-${width}.png`),
      fullPage: true,
    });
  });
}
test("response loss performs status-only recovery", async ({ page }) => {
  const fixture = await securityUI(page, { apply: "lost" });
  await page.goto(`${fixture.primary}/security/roles`);
  await reviewRole(page);
  await page.getByRole("button", { name: "Apply reviewed change" }).click();
  await expect(
    page.getByText("The response was not confirmed.", { exact: false }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Review Role change" }),
  ).toBeDisabled();
  await page
    .getByRole("button", { name: "Check original change status" })
    .click();
  await expect(
    page.getByText("Change enforced.", { exact: true }),
  ).toBeVisible();
  expect(
    fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
  ).toHaveLength(1);
  expect(
    fixture.calls.find((call) => call.method === "GetSecurityChangeStatus")!
      .body.changeId,
  ).toBe(
    fixture.calls.find((call) => call.method === "ApplySecurityChanges")!.body
      .changeId,
  );
});
test("revision conflict requires reload and another review", async ({
  page,
}) => {
  const fixture = await securityUI(page, { apply: "conflict" });
  await page.goto(`${fixture.primary}/security/roles`);
  await reviewRole(page);
  await page.getByRole("button", { name: "Apply reviewed change" }).click();
  await expect(
    page.getByText("The revision changed.", { exact: false }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Apply reviewed change" }),
  ).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Review Role change" }),
  ).toBeDisabled();
  await page.getByRole("button", { name: "Reload security state" }).click();
  await expect(
    page.getByRole("button", { name: "Review Role change" }),
  ).toBeEnabled();
  expect(
    fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
  ).toHaveLength(1);
});
test("recent authentication is required", async ({ page }) => {
  const fixture = await securityUI(page, { recent: false });
  await page.goto(`${fixture.primary}/security/roles`);
  await reviewRole(page);
  await expect(
    page.getByRole("button", { name: "Apply reviewed change" }),
  ).toBeDisabled();
  await expect(
    page
      .getByRole("region", { name: "Review security change" })
      .getByRole("button", { name: "Sign in again" }),
  ).toBeVisible();
  expect(
    fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
  ).toHaveLength(0);
});
test("user membership uses exact identity and environment locks", async ({
  page,
}) => {
  const fixture = await securityUI(page);
  await page.goto(`${fixture.primary}/security/users`);
  await page
    .getByRole("button", { name: "alice · https://idp.example · 1 Roles" })
    .click();
  await expect(
    page.getByRole("button", { name: "Remove reader" }),
  ).toBeDisabled();
  await page.getByLabel("Existing Role ID").fill("cdc-identity");
  await page.getByRole("button", { name: "Review Role assignment" }).click();
  await page.getByRole("button", { name: "Apply reviewed change" }).click();
  expect(
    fixture.calls.find((call) => call.method === "ApplySecurityChanges")!.body
      .changes,
  ).toEqual([
    {
      putAssignment: {
        identity: {
          kind: "SECURITY_PRINCIPAL_KIND_OIDC",
          issuer: "https://idp.example",
          subject: "alice",
        },
        roleId: "cdc-identity",
      },
    },
  ]);
});
test("env Issuer configuration is locked", async ({ page }) => {
  const fixture = await securityUI(page);
  await page.goto(`${fixture.primary}/security/issuers`);
  await page
    .getByRole("button", {
      name: "https://idp.example · Enabled · Environment-owned · Secret bound",
    })
    .click();
  await expect(
    page.getByRole("button", { name: "Review Issuer change" }),
  ).toBeDisabled();
  await expect(
    page.getByRole("button", { name: "Review disable" }),
  ).toBeDisabled();
});
test("denied management cannot enable an editor", async ({ page }) => {
  const fixture = await securityUI(page, { denied: true });
  await page.goto(`${fixture.primary}/security/users`);
  await expect(
    page.getByText("Security management permission is required."),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Review user state" }),
  ).toBeDisabled();
});

test("environment Role policy locks and paginated member inspection", async ({
  page,
}) => {
  const fixture = await securityUI(page);
  await page.goto(`${fixture.primary}/security/roles`);
  await page
    .getByRole("button", {
      name: "Security administrator (security_admin) · 0 rules · Environment-owned",
    })
    .click();
  await expect(
    page.getByRole("button", { name: "Review Role change" }),
  ).toBeDisabled();
  await page.getByRole("button", { name: "Reader (reader) · 1 rules" }).click();
  await page.getByRole("button", { name: "Inspect first user page" }).click();
  await expect(
    page
      .getByRole("region", { name: "Role members" })
      .getByText("alice · https://idp.example"),
  ).toBeVisible();
  await expect(
    page.getByRole("link", { name: "Manage user memberships" }),
  ).toBeVisible();
});
