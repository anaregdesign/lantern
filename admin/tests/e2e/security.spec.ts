import { expect, test } from "@playwright/test";
import { Buffer } from "node:buffer";
import { securityUI } from "./helpers";

// Finish fixture requests before Playwright closes the browser context. Route
// transitions can leave a lazy asset fetch in flight after the last assertion.
test.afterEach(async ({ page }) => {
  await page.unrouteAll({ behavior: "wait" });
  await page.context().unrouteAll({ behavior: "wait" });
});

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
test("audit interrupts slow review preparation and permits explicit fresh review", async ({
  page,
}) => {
  const fixture = await securityUI(page);
  let release = () => {};
  let intercepted = () => {};
  const received = new Promise<void>((resolve) => {
    intercepted = resolve;
  });
  const paused = new Promise<void>((resolve) => {
    release = resolve;
  });
  let prepares = 0;
  await page.route(
    `${fixture.primary}/**/PrepareSecurityChanges`,
    async (route) => {
      if (++prepares !== 1) return route.fallback();
      intercepted();
      await paused;
      try {
        await route.fallback();
      } catch (error) {
        if (!route.request().failure()) throw error;
      }
    },
  );
  try {
    await page.goto(`${fixture.primary}/security/roles`);
    await reviewRole(page);
    await received;
    await expect(
      page
        .getByRole("region", { name: "Review security change", exact: true })
        .getByText("Checking the reviewed change with Server…", {
          exact: true,
        }),
    ).toBeVisible();
    await page.getByRole("button", { name: "Load audit", exact: true }).click();
    await expect(
      page.getByText("The review check was interrupted.", { exact: false }),
    ).toBeVisible();
    await expect(
      page.getByText("Checking the reviewed change with Server…", {
        exact: true,
      }),
    ).toHaveCount(0);
    await expect(
      page.getByRole("button", { name: "Apply reviewed change", exact: true }),
    ).toBeDisabled();
    await expect(
      page.getByRole("button", { name: "Review Role change", exact: true }),
    ).toBeEnabled();
    release();
    await page
      .getByRole("button", { name: "Review Role change", exact: true })
      .click();
    await expect(
      page.getByRole("button", { name: "Apply reviewed change", exact: true }),
    ).toBeEnabled();
    expect(
      fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
    ).toHaveLength(0);
    await page
      .getByRole("button", { name: "Apply reviewed change", exact: true })
      .click();
    await expect(
      page.getByText("Change committed.", { exact: false }),
    ).toBeVisible();
    expect(
      fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
    ).toHaveLength(1);
  } finally {
    release();
  }
});
for (const width of [1280, 390]) {
  test(`Head-managed Role review and Server explanation at ${width}px`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    const fixture = await securityUI(page);
    await page.goto(`${fixture.primary}/security/roles`);
    await page
      .getByRole("textbox", { name: "Role ID", exact: true })
      .fill("head-manager");
    await page
      .getByRole("textbox", { name: "Role name", exact: true })
      .fill("Head relationship manager");
    await page.getByRole("button", { name: "Add rule", exact: true }).click();
    await page
      .getByLabel("Rule 1 action", { exact: true })
      .selectOption({ label: "Read vertices" });
    await page
      .getByLabel("Rule 1 logical prefix", { exact: true })
      .fill("users:alice:");
    await page.getByRole("button", { name: "Add rule", exact: true }).click();
    await page
      .getByLabel("Rule 2 action", { exact: true })
      .selectOption({ label: "Write vertices" });
    await page
      .getByLabel("Rule 2 logical prefix", { exact: true })
      .fill("profiles:");
    await expect(
      page.getByLabel("Rule 1 selector", { exact: true }),
    ).toHaveCount(0);
    for (const label of ["Create connections", "Delete edges"]) {
      await expect(
        page
          .getByLabel("Rule 2 action")
          .getByRole("option", { name: label, exact: true }),
      ).toHaveCount(0);
    }
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
        action: "SECURITY_ACTION_VERTEX_READ",
        prefix: "users:alice:",
      },
      {
        id: "rule-2",
        effect: "SECURITY_EFFECT_ALLOW",
        action: "SECURITY_ACTION_VERTEX_WRITE",
        prefix: "profiles:",
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
    await expect(
      page.getByText("private / hide: Deny · head · Write vertices"),
    ).toBeVisible();
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
      path: testInfo.outputPath(`head-role-${width}.png`),
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
    await expect(
      page.getByText("Original Apply outcomes: 1: applied."),
    ).toBeVisible();
    await page.getByRole("button", { name: "Reload security state" }).click();
    await page.getByLabel("Explanation Issuer").fill("https://idp.example");
    await page.getByLabel("Explanation subject").fill("alice");
    await page
      .getByLabel("Logical key", { exact: true })
      .fill("tenant:private:one");
    await page.getByRole("button", { name: "Ask Server" }).click();
    await expect(page.getByText("Denied · Server revision 3")).toBeVisible();
    await expect(
      page.getByText("private / hide: Deny · Read vertices"),
    ).toBeVisible();
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
  await expect(
    page.getByText("Original item outcomes are unavailable;", { exact: false }),
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

for (const width of [1280, 390]) {
  test(`response-loss remount recovers proof-only pending then enforced at ${width}px`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    const fixture = await securityUI(page, {
      apply: "lost",
      status: "pending-then-enforced",
    });
    await page.goto(`${fixture.primary}/security/roles`);
    await reviewRole(page);
    await page.getByRole("button", { name: "Apply reviewed change" }).click();
    await expect(
      page.getByText("The response was not confirmed.", { exact: false }),
    ).toBeVisible();
    const sent = fixture.calls.find(
      (call) => call.method === "ApplySecurityChanges",
    )!;
    await page
      .getByRole("navigation", { name: "Security", exact: true })
      .getByRole("link", { name: "Users", exact: true })
      .click();
    await expect(
      page.getByText("A prior control change is retained.", { exact: false }),
    ).toBeVisible();
    await page
      .getByRole("button", { name: "Check original change status" })
      .click();
    await expect(
      page.getByText("Change committed.", { exact: false }),
    ).toBeVisible();
    await expect(
      page.getByText("Original item outcomes are unavailable;", {
        exact: false,
      }),
    ).toBeVisible();
    await page
      .getByRole("navigation", { name: "Security", exact: true })
      .getByRole("link", { name: "Roles", exact: true })
      .click();
    await expect(page.getByText("Committed revision 4")).toBeVisible();
    await page
      .getByRole("button", { name: "Check original change status" })
      .click();
    await expect(
      page.getByText("Change enforced.", { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText("Original item outcomes are unavailable;", {
        exact: false,
      }),
    ).toBeVisible();
    expect(
      fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
    ).toHaveLength(1);
    const statuses = fixture.calls.filter(
      (call) => call.method === "GetSecurityChangeStatus",
    );
    expect(statuses).toHaveLength(2);
    expect(
      statuses.every((call) => call.body.changeId === sent.body.changeId),
    ).toBe(true);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: testInfo.outputPath(`proof-recovery-${width}.png`),
      fullPage: true,
    });
  });
}

test("unknown status retains the original ID and disables another Apply", async ({
  page,
}) => {
  const fixture = await securityUI(page, { apply: "lost", status: "unknown" });
  await page.goto(`${fixture.primary}/security/roles`);
  await reviewRole(page);
  await page.getByRole("button", { name: "Apply reviewed change" }).click();
  await page
    .getByRole("button", { name: "Check original change status" })
    .click();
  await expect(
    page.getByText("Change status is unavailable.", { exact: false }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Review Role change" }),
  ).toBeDisabled();
  await expect(
    page.getByText("Committed revision", { exact: false }),
  ).toHaveCount(0);
  expect(
    fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
  ).toHaveLength(1);
});

test("mismatched status preserves the original Apply outcomes and pending state", async ({
  page,
}) => {
  const fixture = await securityUI(page, { status: "mismatch" });
  await page.goto(`${fixture.primary}/security/roles`);
  await reviewRole(page);
  await page.getByRole("button", { name: "Apply reviewed change" }).click();
  await page
    .getByRole("button", { name: "Check original change status" })
    .click();
  await expect(
    page.getByText("Change status is unavailable.", { exact: false }),
  ).toBeVisible();
  await expect(
    page.getByText("Original Apply outcomes: 1: applied."),
  ).toBeVisible();
  await expect(page.getByText("Change enforced.", { exact: true })).toHaveCount(
    0,
  );
  await expect(
    page.getByRole("region", { name: "Change status" }),
  ).toContainText("pending");
});
test("revision conflict requires reload and another review", async ({
  page,
}) => {
  const fixture = await securityUI(page, { apply: "conflict" });
  await page.goto(`${fixture.primary}/security/roles`);
  await reviewRole(page);
  await page.getByRole("button", { name: "Apply reviewed change" }).click();
  await expect(
    page.getByText("The security revision changed.", { exact: false }),
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
test("ordinary Role review applies with an older ordinary session", async ({
  page,
}) => {
  const fixture = await securityUI(page, { recent: false });
  await page.goto(`${fixture.primary}/security/roles`);
  await reviewRole(page);
  await expect(
    page.getByRole("button", { name: "Apply reviewed change" }),
  ).toBeEnabled();
  await page.getByRole("button", { name: "Apply reviewed change" }).click();
  expect(
    fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
  ).toHaveLength(1);
  expect(
    fixture.calls.find((call) => call.method === "ApplySecurityChanges")!.body
      .authorizationProof,
  ).toBeUndefined();
});

test("high-impact review keeps the Admin window active through operation approval", async ({
  page,
}) => {
  const fixture = await securityUI(page, {
    recent: false,
    reauthentication: true,
  });
  await page.goto(`${fixture.primary}/security/roles`);
  await reviewRole(page);
  await expect(
    page.getByRole("button", { name: "Apply reviewed change" }),
  ).toBeDisabled();
  const popupPromise = page.waitForEvent("popup");
  await page
    .getByRole("button", { name: "Reauthenticate reviewed change" })
    .click();
  const popup = await popupPromise;
  await expect(
    popup.getByText("Authentication recorded.", { exact: false }),
  ).toBeVisible();
  expect(page.url()).toBe(`${fixture.primary}/security/roles`);
  await page.getByRole("button", { name: "Check reauthentication" }).click();
  await expect(
    page.getByRole("button", { name: "Apply reviewed change" }),
  ).toBeEnabled();
  await page.getByRole("button", { name: "Apply reviewed change" }).click();
  const review = fixture.calls.find(
    (call) => call.method === "PrepareSecurityChanges",
  )!.body.review as Record<string, unknown>;
  const applied = fixture.calls.find(
    (call) => call.method === "ApplySecurityChanges",
  )!.body;
  expect(applied.changeId).toBe(review.changeId);
  expect(applied.authorizationProof).toBe(
    Buffer.alloc(32, 9).toString("base64"),
  );
  expect(
    fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
  ).toHaveLength(1);
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

for (const width of [1280, 390]) {
  test(`blind Edge handling has no effect or automatic readback at ${width}px`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    const fixture = await securityUI(page);
    let reads = 0;
    let writes = 0;
    await page.route(
      `${fixture.primary}/browser/graph.v1.LanternService/*`,
      async (route) => {
        const method = new URL(route.request().url()).pathname
          .split("/")
          .at(-1);
        if (method === "ScanVertices")
          return route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({ vertices: [] }),
          });
        if (method === "GetEdge") {
          reads++;
          return route.fulfill({
            status: 403,
            contentType: "application/json",
            body: JSON.stringify({
              code: "permission_denied",
              message: "Edge read is unavailable",
            }),
          });
        }
        if (
          [
            "AddEdge",
            "AddEdges",
            "PutEdge",
            "PutEdges",
            "DeleteEdge",
            "DeleteEdges",
          ].includes(method ?? "")
        ) {
          writes++;
          return route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({
              acceptance: {
                kind: "MUTATION_ACCEPTANCE_KIND_HANDLED_EFFECT_UNDISCLOSED",
              },
            }),
          });
        }
        throw new Error(`Unexpected blind Edge call: ${method}`);
      },
    );
    await page.goto(`${fixture.primary}/edges/tails%3Aa/heads%3Ab`);
    await expect(page.getByTestId("edge-form-add")).toBeVisible();
    await page.getByTestId("edge-add-weight").fill("7");
    await page.getByTestId("edge-add-submit").click();
    await expect(
      page.getByText("Request handled.", { exact: false }),
    ).toBeVisible();
    await expect(page.getByTestId("edge-current-weight")).toHaveCount(0);
    expect(reads).toBe(1);
    expect(writes).toBe(1);
    await expect(page.getByTestId("edge-add-submit")).toBeDisabled();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: testInfo.outputPath(`blind-Edge-${width}.png`),
      fullPage: true,
    });
    await page.goto(`${fixture.primary}/cli`);
    const input = page.getByTestId("cli-input");
    for (const command of [
      "put edge tails:a heads:b 7",
      "delete edge tails:a heads:b",
    ]) {
      const previous = await page.getByTestId("cli-entry-ok").count();
      await input.fill(command);
      await input.press("Enter");
      await expect(page.getByTestId("cli-entry-ok")).toHaveCount(previous + 1);
      await expect(page.getByTestId("cli-entry-ok").last()).toContainText(
        '"acceptance": "acceptedUndisclosed"',
      );
      await expect(page.getByTestId("cli-entry-ok").last()).not.toContainText(
        '"deleted"',
      );
    }
    expect(reads).toBe(1);
    expect(writes).toBe(3);
  });
}

test("definitive first refusal allows correction only after a fresh review with another ID", async ({
  page,
}) => {
  const fixture = await securityUI(page, { apply: "unknown-role-once" });
  await page.goto(`${fixture.primary}/security/roles`);
  await reviewRole(page);
  await page
    .getByRole("button", { name: "Apply reviewed change", exact: true })
    .click();
  await expect(
    page.getByText("A referenced Role does not exist.", { exact: false }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Apply reviewed change", exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Review Role change", exact: true }),
  ).toBeDisabled();
  const first = fixture.calls.filter(
    (call) => call.method === "ApplySecurityChanges",
  )[0];
  await page
    .getByRole("button", { name: "Reload security state", exact: true })
    .click();
  await page
    .getByRole("textbox", { name: "Role name", exact: true })
    .fill("Corrected Role");
  expect(
    fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
  ).toHaveLength(1);
  await page
    .getByRole("button", { name: "Review Role change", exact: true })
    .click();
  expect(
    fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
  ).toHaveLength(1);
  await page
    .getByRole("button", { name: "Apply reviewed change", exact: true })
    .click();
  await expect(
    page.getByText("Change committed.", { exact: false }),
  ).toBeVisible();
  const attempts = fixture.calls.filter(
    (call) => call.method === "ApplySecurityChanges",
  );
  expect(attempts).toHaveLength(2);
  expect(attempts[1].body.changeId).not.toEqual(first.body.changeId);
  const corrected = attempts[1].body.changes as { putRole: { name: string } }[];
  expect(corrected[0].putRole.name).toBe("Corrected Role");
});

test("generic committed-ID conflict remains status-only across route remount", async ({
  page,
}) => {
  const fixture = await securityUI(page, {
    apply: "generic-conflict",
    status: "unknown",
  });
  await page.goto(`${fixture.primary}/security/roles`);
  await reviewRole(page);
  await page
    .getByRole("button", { name: "Apply reviewed change", exact: true })
    .click();
  await expect(
    page.getByText("The response was not confirmed.", { exact: false }),
  ).toBeVisible();
  await page.getByRole("link", { name: "Users", exact: true }).click();
  await page.getByRole("link", { name: "Roles", exact: true }).click();
  await page
    .getByRole("button", { name: "Check original change status", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Review Role change", exact: true }),
  ).toBeDisabled();
  expect(
    fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
  ).toHaveLength(1);
});
