import { expect, test } from "@playwright/test";
import { securityUI } from "./helpers";

test.afterEach(async ({ page }) => {
  await page.unrouteAll({ behavior: "wait" });
  await page.context().unrouteAll({ behavior: "wait" });
});

async function reviewLogLevel(page: import("@playwright/test").Page) {
  await page
    .getByLabel("New log level", { exact: true })
    .selectOption({ label: "Warn" });
  await page
    .getByRole("button", { name: "Review log level change", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Apply reviewed change", exact: true }),
  ).toBeEnabled();
}

for (const width of [1280, 390]) {
  test(`log policy commit and this-node application remain distinct at ${width}px`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    const fixture = await securityUI(page, {
      logSettings: { application: "pending" },
    });
    await page.goto(`${fixture.primary}/settings`);
    await expect(
      page.getByRole("heading", { name: "Settings", exact: true }),
    ).toBeVisible();
    await expect(page.getByText("Environment", { exact: true })).toBeVisible();
    await expect(
      page.getByText("Startup setting active", { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("button", {
        name: "Review log level change",
        exact: true,
      }),
    ).toBeDisabled();
    await reviewLogLevel(page);
    await expect(
      page.getByRole("region", {
        name: "Review log level change",
        exact: true,
      }),
    ).toContainText("Applying this review can prevent an older server version");
    await expect(
      page.getByRole("region", { name: "Original change status", exact: true }),
    ).toHaveCount(0);
    await expect(
      page.getByRole("button", { name: "Apply reviewed change", exact: true }),
    ).toBeEnabled();
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.screenshot({
      path: testInfo.outputPath(`settings-review-${width}.png`),
      fullPage: true,
      animations: "disabled",
    });
    await expect(
      page.getByRole("button", { name: "Apply reviewed change", exact: true }),
    ).toBeEnabled();
    await page
      .getByRole("button", { name: "Apply reviewed change", exact: true })
      .click();
    await expect(
      page.getByText("The log policy was committed.", { exact: false }),
    ).toBeVisible();
    await expect(
      page.getByText("These values are stale.", { exact: false }),
    ).toBeVisible();
    await page
      .getByRole("button", { name: "Reload settings", exact: true })
      .click();
    await expect(
      page.getByText("Pending on this node", { exact: true }),
    ).toBeVisible();
    await expect(page.getByText("0 / 1", { exact: true })).toBeVisible();
    await page
      .getByRole("button", { name: "Reload settings", exact: true })
      .click();
    await expect(
      page.getByText("Managed policy", { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText("Applied on this node", { exact: true }),
    ).toBeVisible();
    await expect(page.getByText("1 / 1", { exact: true })).toBeVisible();
    expect(
      fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
    ).toHaveLength(1);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.screenshot({
      path: testInfo.outputPath(`settings-${width}.png`),
      fullPage: true,
      animations: "disabled",
    });
  });
}

test("response loss and page reload recover the same original without a second Apply", async ({
  page,
}, testInfo) => {
  const fixture = await securityUI(page, { apply: "lost" });
  await page.goto(`${fixture.primary}/settings`);
  await reviewLogLevel(page);
  await page
    .getByRole("button", { name: "Apply reviewed change", exact: true })
    .click();
  await expect(
    page.getByText("The response was not confirmed.", { exact: false }),
  ).toBeVisible();
  const first = fixture.calls.find(
    (call) => call.method === "ApplySecurityChanges",
  );
  expect(first).toBeDefined();
  await page.reload();
  await expect(
    page.getByRole("button", {
      name: "Check original change status",
      exact: true,
    }),
  ).toBeVisible();
  await expect(page.getByText("Managed policy", { exact: true })).toBeVisible();
  await expect(
    page.getByText("The original change is not confirmed.", { exact: false }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Review log level change", exact: true }),
  ).toBeDisabled();
  await page.screenshot({
    path: testInfo.outputPath("settings-original-unknown-1280.png"),
    fullPage: true,
    animations: "disabled",
  });
  await page
    .getByRole("button", { name: "Check original change status", exact: true })
    .click();
  await expect(
    page.getByText("The log policy was committed.", { exact: false }),
  ).toBeVisible();
  const status = fixture.calls
    .filter((call) => call.method === "GetSecurityChangeStatus")
    .at(-1)!;
  const original = first!.body.currentReview as {
    changeId: unknown;
    intentDigest: unknown;
  };
  expect(status.body.currentChangeId).toEqual(original.changeId);
  expect(status.body.currentIntentDigest).toEqual(original.intentDigest);
  await page
    .getByRole("button", { name: "Reload settings", exact: true })
    .click();
  await expect(page.getByText("Managed policy", { exact: true })).toBeVisible();
  expect(
    fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
  ).toHaveLength(1);
});

test("a conflict requires reload and a fresh review", async ({ page }) => {
  const fixture = await securityUI(page, { apply: "conflict" });
  await page.goto(`${fixture.primary}/settings`);
  await reviewLogLevel(page);
  await page
    .getByRole("button", { name: "Apply reviewed change", exact: true })
    .click();
  await expect(
    page.getByText("The original change was refused.", { exact: false }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Review log level change", exact: true }),
  ).toBeDisabled();
  await page
    .getByRole("button", { name: "Reload settings", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Review log level change", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Apply reviewed change", exact: true }),
  ).toBeEnabled();
  expect(
    fixture.calls.filter((call) => call.method === "PrepareSecurityChanges"),
  ).toHaveLength(2);
  expect(
    fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
  ).toHaveLength(1);
});

test("partial adoption remains visible and cancelling review sends no Apply", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 390, height: 900 });
  const fixture = await securityUI(page, {
    logSettings: { adoption: "fenced" },
  });
  await page.goto(`${fixture.primary}/settings`);
  await expect(
    page.getByText("This node is prepared for managed log policy", {
      exact: false,
    }),
  ).toBeVisible();
  await expect(
    page.getByText("Preparation change 19:" + "17".repeat(16), { exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: testInfo.outputPath("settings-fenced-390.png"),
    fullPage: true,
    animations: "disabled",
  });
  await reviewLogLevel(page);
  await page
    .getByRole("button", { name: "Cancel review", exact: true })
    .click();
  await expect(
    page.getByRole("region", { name: "Review log level change", exact: true }),
  ).toHaveCount(0);
  expect(
    fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
  ).toHaveLength(0);
});

test("authority loss keeps the previous managed observation explicitly stale", async ({
  page,
}) => {
  const fixture = await securityUI(page, {
    logSettings: { adoption: "managed" },
  });
  await page.goto(`${fixture.primary}/settings`);
  await expect(page.getByText("Managed policy", { exact: true })).toBeVisible();
  await page.route(`${fixture.primary}/**/GetLogSettings`, (route) =>
    route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({ code: "unavailable", message: "Unavailable" }),
    }),
  );
  await page
    .getByRole("button", { name: "Reload settings", exact: true })
    .click();
  await expect(
    page.getByText("These values are stale.", { exact: false }),
  ).toBeVisible();
  await expect(page.getByText("Managed policy", { exact: true })).toBeVisible();
  await expect(
    page.getByLabel("New log level", { exact: true }),
  ).toBeDisabled();
});

test("OFF shows the environment-owned explanation without a management mutation", async ({
  page,
}) => {
  const fixture = await securityUI(page, { mode: "off" });
  await page.goto(`${fixture.primary}/settings`);
  await expect(
    page.getByText("Settings remain environment-owned.", { exact: false }),
  ).toBeVisible();
  await expect(page.getByLabel("New log level", { exact: true })).toHaveCount(
    0,
  );
  expect(
    fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
  ).toHaveLength(0);
});

test("a member without an owned origin exposes settings without an editor", async ({
  page,
}) => {
  const fixture = await securityUI(page, { readOnly: true });
  await page.goto(`${fixture.primary}/settings`);
  await expect(page.getByText("Environment", { exact: true })).toBeVisible();
  await expect(
    page.getByLabel("New log level", { exact: true }),
  ).toBeDisabled();
  await expect(
    page.getByRole("button", { name: "Review log level change", exact: true }),
  ).toBeDisabled();
  expect(
    fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
  ).toHaveLength(0);
});

test("unknown log policy leaves session revocation available and recovers the same log original", async ({
  page,
}) => {
  const fixture = await securityUI(page, { logRecoveryAfterRevocation: true });
  await page.goto(`${fixture.primary}/settings`);
  await reviewLogLevel(page);
  await page
    .getByRole("button", { name: "Apply reviewed change", exact: true })
    .click();
  await expect(
    page.getByText("The response was not confirmed.", { exact: false }),
  ).toBeVisible();
  const loggingApply = fixture.calls.find(
    (call) => call.method === "ApplySecurityChanges",
  )!;
  const original = loggingApply.body.currentReview as {
    changeId: unknown;
    intentDigest: unknown;
  };

  await page.goto(`${fixture.primary}/security/users`);
  await page
    .getByRole("button", {
      name: "alice@example.test",
      exact: true,
    })
    .click();
  await page
    .getByRole("button", { name: "Edit user state", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Review session revocation", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Apply reviewed change", exact: true }),
  ).toBeEnabled();
  await page
    .getByRole("button", { name: "Apply reviewed change", exact: true })
    .click();
  await expect(
    page.getByText("The original change applied.", { exact: false }),
  ).toBeVisible();
  const applies = fixture.calls.filter(
    (call) => call.method === "ApplySecurityChanges",
  );
  expect(applies).toHaveLength(2);
  const securityReview = applies[1].body.currentReview as {
    changeId: unknown;
    changes: unknown[];
  };
  expect(securityReview.changeId).not.toEqual(original.changeId);
  expect(securityReview.changes).toEqual([
    {
      revokeUserSessions: {
        kind: "SECURITY_PRINCIPAL_KIND_OIDC",
        issuer: "https://idp.example",
        subject: "alice",
      },
    },
  ]);

  await page.goto(`${fixture.primary}/settings`);
  await expect(
    page.getByRole("button", {
      name: "Check original change status",
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Review log level change", exact: true }),
  ).toBeDisabled();
  await page
    .getByRole("button", { name: "Check original change status", exact: true })
    .click();
  await expect(
    page.getByText("The original change was refused.", { exact: false }),
  ).toBeVisible();
  const status = fixture.calls
    .filter((call) => call.method === "GetSecurityChangeStatus")
    .at(-1)!;
  expect(status.body.currentChangeId).toEqual(original.changeId);
  expect(status.body.currentIntentDigest).toEqual(original.intentDigest);
  const preparedLogging = fixture.calls.filter(
    (call) =>
      call.method === "PrepareSecurityChanges" &&
      (
        call.body.currentReview as {
          changes: Array<{ putLogPolicy?: unknown }>;
        }
      ).changes.some((change) => !!change.putLogPolicy),
  );
  expect(preparedLogging).toHaveLength(1);
  expect(
    fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
  ).toHaveLength(2);
});
