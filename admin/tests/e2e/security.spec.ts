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
  await page.getByRole("button", { name: "Create Role", exact: true }).click();
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
      page.getByText("The original change applied.", { exact: false }),
    ).toBeVisible();
    expect(
      fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
    ).toHaveLength(1);
  } finally {
    release();
  }
});
for (const width of [1280, 390]) {
  test(`Allow-only Role review and Server explanation at ${width}px`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    const fixture = await securityUI(page);
    await page.goto(`${fixture.primary}/security/roles`);
    await page
      .getByRole("button", { name: "Create Role", exact: true })
      .click();
    await page
      .getByRole("textbox", { name: "Role ID", exact: true })
      .fill("head-manager");
    await page
      .getByRole("textbox", { name: "Role name", exact: true })
      .fill("Head relationship manager");
    await page.getByRole("button", { name: "Add rule", exact: true }).click();
    await page
      .getByLabel("Rule 1 permission", { exact: true })
      .selectOption({ label: "Read" });
    await page
      .getByLabel("Rule 1 logical prefix", { exact: true })
      .fill("users:alice:");
    await page.getByRole("button", { name: "Add rule", exact: true }).click();
    await page
      .getByLabel("Rule 2 permission", { exact: true })
      .selectOption({ label: "Write" });
    await page
      .getByLabel("Rule 2 logical prefix", { exact: true })
      .fill("profiles:");
    await expect(
      page.getByLabel("Rule 1 selector", { exact: true }),
    ).toHaveCount(0);
    for (const label of ["Create connections", "Delete edges"]) {
      await expect(
        page
          .getByLabel("Rule 2 permission")
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
      page.getByText("The original change applied.", { exact: false }),
    ).toBeVisible();
    const call = fixture.calls.find(
      (call) => call.method === "ApplySecurityChanges",
    )!;
    const changes = (call.body.currentReview as Record<string, unknown>)
      .changes as { putRole: { rules: unknown[] } }[];
    expect(changes[0].putRole.rules).toEqual([
      {
        id: "rule-1",
        permission: "SECURITY_PERMISSION_READ",
        resourceKind: "SECURITY_RESOURCE_DATA",
        prefix: "users:alice:",
      },
      {
        id: "rule-2",
        permission: "SECURITY_PERMISSION_WRITE",
        resourceKind: "SECURITY_RESOURCE_DATA",
        prefix: "profiles:",
      },
    ]);
    await page.getByRole("button", { name: "Reload security state" }).click();
    await page
      .getByRole("button", {
        name: "Explain another user's access",
        exact: true,
      })
      .click();
    await page.getByLabel("Explanation Issuer").fill("https://idp.example");
    await page.getByLabel("Explanation subject").fill("alice");
    await page
      .getByLabel("Explanation permission")
      .selectOption({ label: "Write" });
    await page.getByLabel("Data target", { exact: true }).selectOption("edge");
    await page.getByLabel("Explanation tail").fill("users:alice:connections");
    await page.getByLabel("Explanation head").fill("profiles:bob");
    await page.getByRole("button", { name: "Ask Server" }).click();
    await expect(page.getByText("Refused · Policy cut 3")).toBeVisible();
    await expect(
      page.getByText("No sufficient matching Allow under the current policy."),
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
    expect(
      await page
        .getByText("Saved Roles", { exact: true })
        .evaluate((element) => element.getBoundingClientRect().width),
    ).toBeGreaterThan(150);
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
      page.getByText("The original change applied.", { exact: false }),
    ).toBeVisible();
    const sent = fixture.calls.find(
      (call) => call.method === "ApplySecurityChanges",
    )!;
    expect(
      (
        (sent.body.currentReview as Record<string, unknown>)
          .expectedCut as Record<string, unknown>
      ).sequence,
    ).toBe("3");
    expect(sent.body.expectedRevision).toBeUndefined();
    expect(sent.csrf).toBe("c".repeat(43));
    expect(sent.authorization).toBeUndefined();
    await page
      .getByRole("button", { name: "Check original change status" })
      .click();
    await expect(
      page.getByText("New authorizations using earlier policy have stopped", {
        exact: false,
      }),
    ).toBeVisible();
    await expect(
      page.getByText("Original commit 11 · 1: APPLIED"),
    ).toBeVisible();
    await page.getByRole("button", { name: "Reload security state" }).click();
    await page
      .getByRole("button", {
        name: "Explain another user's access",
        exact: true,
      })
      .click();
    await page.getByLabel("Explanation Issuer").fill("https://idp.example");
    await page.getByLabel("Explanation subject").fill("alice");
    await page
      .getByLabel("Logical key", { exact: true })
      .fill("tenant:private:one");
    await page.getByRole("button", { name: "Ask Server" }).click();
    await expect(page.getByText("Refused · Policy cut 3")).toBeVisible();
    await expect(
      page.getByText("No sufficient matching Allow under the current policy."),
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
    page.getByText("New authorizations using earlier policy have stopped", {
      exact: false,
    }),
  ).toBeVisible();
  await expect(page.getByText("Original commit 11 · 1: APPLIED")).toBeVisible();
  expect(
    fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
  ).toHaveLength(1);
  expect(
    fixture.calls.find((call) => call.method === "GetSecurityChangeStatus")!
      .body.currentChangeId,
  ).toEqual(
    (
      fixture.calls.find((call) => call.method === "ApplySecurityChanges")!.body
        .currentReview as Record<string, unknown>
    ).changeId,
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
      page.getByText("The original change applied.", { exact: false }),
    ).toBeVisible();
    await expect(
      page.getByText("Original commit 11 · 1: APPLIED"),
    ).toBeVisible();
    await page
      .getByRole("navigation", { name: "Security", exact: true })
      .getByRole("link", { name: "Roles", exact: true })
      .click();
    await expect(
      page.getByText("Original commit 11 · 1: APPLIED"),
    ).toBeVisible();
    await page
      .getByRole("button", { name: "Check original change status" })
      .click();
    await expect(
      page.getByText("New authorizations using earlier policy have stopped", {
        exact: false,
      }),
    ).toBeVisible();
    await expect(
      page.getByText("Original commit 11 · 1: APPLIED"),
    ).toBeVisible();
    expect(
      fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
    ).toHaveLength(1);
    const statuses = fixture.calls.filter(
      (call) => call.method === "GetSecurityChangeStatus",
    );
    expect(statuses).toHaveLength(2);
    expect(
      statuses.every(
        (call) =>
          JSON.stringify(call.body.currentChangeId) ===
          JSON.stringify(
            (sent.body.currentReview as Record<string, unknown>).changeId,
          ),
      ),
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
  await expect(page.getByText("Original commit", { exact: false })).toHaveCount(
    0,
  );
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
  await expect(page.getByText("Original commit 11 · 1: APPLIED")).toBeVisible();
  await expect(
    page.getByText("New authorizations using earlier policy have stopped", {
      exact: false,
    }),
  ).toHaveCount(0);
  await expect(
    page.getByRole("region", { name: "Change status" }),
  ).toContainText("applied");
});
test("revision conflict requires reload and another review", async ({
  page,
}) => {
  const fixture = await securityUI(page, { apply: "conflict" });
  await page.goto(`${fixture.primary}/security/roles`);
  await reviewRole(page);
  await page.getByRole("button", { name: "Apply reviewed change" }).click();
  await expect(
    page.getByText("The original operation was rejected: REJECTED_CAS.", {
      exact: false,
    }),
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
  await expect(popup).toHaveURL(fixture.authorizationUrl);
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
  )!.body.currentReview as Record<string, unknown>;
  const applied = fixture.calls.find(
    (call) => call.method === "ApplySecurityChanges",
  )!.body;
  expect((applied.currentReview as Record<string, unknown>).changes).toEqual(
    review.changes,
  );
  expect(
    (applied.currentReview as Record<string, unknown>).changeId,
  ).toBeDefined();
  expect(review.changeId).toBeUndefined();
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
    .getByRole("button", { name: "alice@example.test", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Manage assigned Roles", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Remove reader" }),
  ).toBeDisabled();
  await page.getByLabel("Existing Role ID").selectOption("cdc-identity");
  await page.getByRole("button", { name: "Review Role assignment" }).click();
  await page.getByRole("button", { name: "Apply reviewed change" }).click();
  expect(
    (
      fixture.calls.find((call) => call.method === "ApplySecurityChanges")!.body
        .currentReview as Record<string, unknown>
    ).changes,
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
for (const profile of ["browser", "API"] as const) {
  test(`Issuer editor reviews a ${profile}-only profile`, async ({ page }) => {
    const fixture = await securityUI(page);
    await page.goto(`${fixture.primary}/security/issuers`);
    await page.getByRole("button", { name: "Add Issuer", exact: true }).click();
    await page
      .getByRole("textbox", { name: "Issuer URL", exact: true })
      .fill("https://new-idp.example");
    await page
      .getByRole("textbox", { name: "Signing algorithms", exact: true })
      .fill("EdDSA");
    if (profile === "browser") {
      await page
        .getByRole("textbox", { name: "Browser client ID", exact: true })
        .fill("browser-client");
      await page
        .getByRole("textbox", { name: "Browser redirect URI", exact: true })
        .fill(`${fixture.primary}/auth/callback`);
    } else {
      await page
        .getByRole("textbox", { name: "API audience", exact: true })
        .fill("lantern-api");
    }
    await page
      .getByRole("button", { name: "Review Issuer change", exact: true })
      .click();
    await expect(
      page.getByRole("button", { name: "Apply reviewed change", exact: true }),
    ).toBeEnabled();
    const prepared = fixture.calls.find(
      (call) => call.method === "PrepareSecurityChanges",
    )!;
    const changes = (prepared.body.currentReview as Record<string, unknown>)
      .changes as { putIssuer: Record<string, unknown> }[];
    const issuer = changes[0].putIssuer;
    expect(issuer.issuer).toBe("https://new-idp.example");
    if (profile === "browser") {
      expect(issuer.clientId).toBe("browser-client");
      expect(issuer.apiAudience ?? "").toBe("");
    } else {
      expect(issuer.apiAudience).toBe("lantern-api");
      expect(issuer.clientId ?? "").toBe("");
      expect(issuer.redirectUri ?? "").toBe("");
    }
  });
}
test("Issuer editor rejects a partial browser profile before review", async ({
  page,
}) => {
  const fixture = await securityUI(page);
  await page.goto(`${fixture.primary}/security/issuers`);
  await page.getByRole("button", { name: "Add Issuer", exact: true }).click();
  await page
    .getByRole("textbox", { name: "Issuer URL", exact: true })
    .fill("https://new-idp.example");
  await page
    .getByRole("textbox", { name: "API audience", exact: true })
    .fill("lantern-api");
  await page
    .getByRole("textbox", { name: "Browser client ID", exact: true })
    .fill("partial-browser");
  await page
    .getByRole("button", { name: "Review Issuer change", exact: true })
    .click();
  await expect(
    page.getByText(
      "Browser redirect URI is required and must not have surrounding spaces.",
      { exact: true },
    ),
  ).toBeVisible();
  expect(
    fixture.calls.filter((call) => call.method === "PrepareSecurityChanges"),
  ).toHaveLength(0);
});
test("env Issuer configuration is locked", async ({ page }) => {
  const fixture = await securityUI(page);
  await page.goto(`${fixture.primary}/security/issuers`);
  await page
    .getByRole("button", {
      name: "https://idp.example",
      exact: true,
    })
    .click();
  await expect(
    page.getByRole("button", { name: "Edit Issuer", exact: true }),
  ).toBeDisabled();
  await expect(
    page.getByText("Contact the operator to change declarative configuration."),
  ).toBeVisible();
});
test("denied management cannot enable an editor", async ({ page }) => {
  const fixture = await securityUI(page, { denied: true });
  await page.goto(`${fixture.primary}/security/users`);
  await expect(
    page.getByText("Security management permission is required."),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Register user", exact: true }),
  ).toBeDisabled();
});

test("environment Role policy locks and paginated member inspection", async ({
  page,
}) => {
  const fixture = await securityUI(page);
  await page.goto(`${fixture.primary}/security/roles`);
  await page
    .getByRole("button", {
      name: "Security administrator (security_admin)",
      exact: true,
    })
    .click();
  await expect(
    page.getByRole("button", { name: "Edit permissions", exact: true }),
  ).toBeDisabled();
  await page
    .getByRole("button", { name: "Reader (reader)", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Assign members", exact: true })
    .click();
  await page.getByRole("button", { name: "Inspect first user page" }).click();
  await expect(
    page
      .getByRole("region", { name: "Role members" })
      .getByText("alice@example.test · https://idp.example · assigned", {
        exact: false,
      }),
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
      page
        .getByTestId("edge-form-add")
        .getByText("Request handled.", { exact: false }),
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

test("original rejection allows correction only after a fresh review with another origin-minted ID", async ({
  page,
}) => {
  const fixture = await securityUI(page, { apply: "unknown-role-once" });
  await page.goto(`${fixture.primary}/security/roles`);
  await reviewRole(page);
  await page
    .getByRole("button", { name: "Apply reviewed change", exact: true })
    .click();
  await expect(
    page.getByText("The original operation was rejected: REJECTED_INVARIANT.", {
      exact: false,
    }),
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
    page.getByText("The original change applied.", { exact: false }),
  ).toBeVisible();
  const attempts = fixture.calls.filter(
    (call) => call.method === "ApplySecurityChanges",
  );
  expect(attempts).toHaveLength(2);
  expect(
    (attempts[1].body.currentReview as Record<string, unknown>).changeId,
  ).not.toEqual((first.body.currentReview as Record<string, unknown>).changeId);
  const corrected = (attempts[1].body.currentReview as Record<string, unknown>)
    .changes as { putRole: { name: string } }[];
  expect(corrected[0].putRole.name).toBe("Corrected Role");
});

for (const width of [1280, 390]) {
  test(`document replacement preserves the original and gates cached status at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 });
    const fixture = await securityUI(page, { apply: "lost" });
    await page.goto(`${fixture.primary}/security/roles`);
    await reviewRole(page);
    await page
      .getByRole("button", { name: "Apply reviewed change", exact: true })
      .click();
    await expect(
      page.getByText("The response was not confirmed.", { exact: false }),
    ).toBeVisible();
    const sent = fixture.calls.find(
      (call) => call.method === "ApplySecurityChanges",
    )!.body.currentReview as Record<string, unknown>;
    for (let reload = 0; reload < 2; reload++) {
      await page.reload();
      await expect(
        page.getByText("A prior control change is retained.", { exact: false }),
      ).toBeVisible();
      await expect(
        page.getByRole("button", { name: "Create Role", exact: true }),
      ).toBeDisabled();
      expect(
        fixture.calls.filter(
          (call) => call.method === "GetSecurityChangeStatus",
        ),
      ).toHaveLength(reload);
      await page
        .getByRole("button", {
          name: "Check original change status",
          exact: true,
        })
        .click();
      await expect(
        page.getByText("Original commit 11 · 1: APPLIED"),
      ).toBeVisible();
    }
    expect(
      fixture.calls.filter((call) => call.method === "PrepareSecurityChanges"),
    ).toHaveLength(1);
    expect(
      fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
    ).toHaveLength(1);
    const statuses = fixture.calls.filter(
      (call) => call.method === "GetSecurityChangeStatus",
    );
    expect(statuses).toHaveLength(2);
    for (const { body } of statuses) {
      expect(body.currentProfile).toEqual(sent.profile);
      expect(body.currentChangeId).toEqual(sent.changeId);
      expect(body.currentIntentDigest).toEqual(sent.intentDigest);
    }
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    ).toBe(true);
  });
}

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
    page.getByRole("button", { name: "Create Role", exact: true }),
  ).toBeDisabled();
  expect(
    fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
  ).toHaveLength(1);
});

for (const width of [1280, 390]) {
  test(`UID-bound Role stays scoped through inspection and editing at ${width}px`, async ({
    page,
  }) => {
    const fixture = await securityUI(page, { uidRole: true });
    await page.setViewportSize({ width, height: 900 });
    await page.goto(`${fixture.primary}/security/roles`);
    await page
      .getByRole("button", {
        name: "Personal user (personal_user)",
        exact: true,
      })
      .click();
    await expect(
      page.getByText("read: Allow Read on Logical data · users:<trusted UID>:"),
    ).toBeVisible();
    await page
      .getByRole("button", { name: "Edit permissions", exact: true })
      .click();
    await expect(
      page.getByLabel("Rule 1 data scope", { exact: true }),
    ).toHaveValue("personal");
    await expect(
      page.getByLabel("Rule 1 logical prefix", { exact: true }),
    ).toHaveCount(0);
    expect(
      fixture.calls.some((call) => call.method === "PrepareSecurityChanges"),
    ).toBe(false);
  });
}

test("Role template selection creates an unsaved draft, then saved details and explicit membership", async ({
  page,
}) => {
  const fixture = await securityUI(page);
  await page.goto(`${fixture.primary}/security/roles`);
  await expect(
    page.getByRole("textbox", { name: "Role ID", exact: true }),
  ).toHaveCount(0);
  const create = page.getByRole("button", { name: "Create Role", exact: true });
  await create.focus();
  await expect(create).toBeFocused();
  await page.keyboard.press("Enter");
  await page
    .getByLabel("Role starting point", { exact: true })
    .selectOption("template");
  await page.getByLabel("Template logical prefix").fill("projects:demo:");
  await page
    .getByRole("button", { name: "Load templates", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Use Reader template", exact: true })
    .click();
  await expect(
    page.getByText("Unsaved draft · From Reader template", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByLabel("Rule 1 logical prefix", { exact: true }),
  ).toHaveValue("projects:demo:");
  const ruleHelp = page
    .getByRole("region", { name: "Rule 1", exact: true })
    .getByText("Allowed values and details", { exact: true })
    .first();
  await ruleHelp.focus();
  await page.keyboard.press("Enter");
  await expect(ruleHelp.locator("..")).toHaveAttribute("open", "");
  expect(
    fixture.calls.some((call) => call.method === "ApplySecurityChanges"),
  ).toBe(false);
  await page
    .getByRole("textbox", { name: "Role ID", exact: true })
    .fill("project-reader");
  await page
    .getByRole("button", { name: "Review Role change", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Apply reviewed change", exact: true })
    .click();
  await expect(
    page
      .getByRole("region", { name: "Saved record details" })
      .getByText("Reader template (project-reader)", { exact: true }),
  ).toBeVisible();
  expect(
    fixture.calls
      .filter((call) => call.method === "GetRole")
      .map((call) => call.body.id),
  ).toEqual(["project-reader", "project-reader"]);
  expect(
    fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
  ).toHaveLength(1);
  await page
    .getByRole("button", { name: "Assign members", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Inspect first user page", exact: true })
    .click();
  await page
    .getByRole("button", {
      name: "Manage Roles for alice@example.test",
      exact: true,
    })
    .click();
  await page
    .getByRole("button", { name: "Manage assigned Roles", exact: true })
    .click();
  await page.getByLabel("Existing Role ID").selectOption("project-reader");
  await page
    .getByRole("button", { name: "Review Role assignment", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Apply reviewed change", exact: true })
    .click();
  await expect(
    page
      .getByRole("region", { name: "Saved record details" })
      .getByText("project-reader", { exact: true }),
  ).toBeVisible();
});

test("a new Role draft cannot overwrite an existing Role ID", async ({
  page,
}) => {
  const fixture = await securityUI(page);
  await page.goto(`${fixture.primary}/security/roles`);
  await reviewRole(page);
  await page
    .getByRole("textbox", { name: "Role ID", exact: true })
    .fill("reader");
  await page
    .getByRole("button", { name: "Review Role change", exact: true })
    .click();
  await expect(
    page.getByText("That Role ID already exists.", { exact: false }),
  ).toBeVisible();
  expect(
    fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
  ).toHaveLength(0);
});

for (const width of [1280, 390]) {
  test(`shared Edge and registration policies stage, review and read back at ${width}px`, async ({
    page,
  }) => {
    const fixture = await securityUI(page, {
      roles: [
        {
          id: "personal_user",
          name: "Personal user",
          rules:
            width === 1280
              ? [
                  {
                    id: "read-notes",
                    permission: "SECURITY_PERMISSION_READ",
                    resourceKind: "SECURITY_RESOURCE_DATA",
                    principalUidPrefix: {
                      literalBefore: "users:",
                      literalAfter: ":notes:",
                    },
                  },
                ]
              : [],
        },
      ],
    });
    await page.setViewportSize({ width, height: 900 });
    await page.goto(`${fixture.primary}/security/roles`);
    await page
      .getByRole("button", {
        name: "Load shared Security settings",
        exact: true,
      })
      .click();
    const settings = page.getByRole("region", {
      name: "Shared Security settings",
      exact: true,
    });
    const joining = settings.getByRole("region", {
      name: "Saved joining Role permissions",
      exact: true,
    });
    await expect(
      joining.getByText(
        width === 1280
          ? "read-notes: Allow Read on Logical data · users:<trusted UID>:notes:"
          : "No saved permissions; this Role adds no access.",
        { exact: true },
      ),
    ).toBeVisible();
    await expect(joining.getByText(/Allow Write/)).toHaveCount(0);
    await expect(
      settings.getByText("Current: readable → readable", { exact: false }),
    ).toBeVisible();
    expect(
      await settings
        .getByLabel("Shared Edge editing rule", { exact: true })
        .locator("option")
        .allTextContents(),
    ).toEqual([
      "readable → readable",
      "writable → writable",
      "readable → writable",
      "writable → readable",
    ]);
    await settings
      .getByLabel("Shared Edge editing rule", { exact: true })
      .selectOption({ label: "writable → readable" });
    await expect(
      settings.getByText("Unsaved Edge policy draft", { exact: false }),
    ).toBeVisible();
    expect(
      fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
    ).toHaveLength(0);
    await settings
      .getByRole("button", { name: "Review shared Edge rule", exact: true })
      .click();
    await page
      .getByRole("button", { name: "Apply reviewed change", exact: true })
      .click();
    await page
      .getByRole("button", {
        name: "Load shared Security settings",
        exact: true,
      })
      .click();
    await expect(
      settings.getByText("Current: writable → readable", { exact: false }),
    ).toBeVisible();
    await settings
      .getByRole("checkbox", {
        name: "Allow new-user registration",
        exact: true,
      })
      .check();
    await settings
      .getByRole("checkbox", {
        name: "https://idp.example · Browser (browser)",
        exact: true,
      })
      .check();
    await settings
      .getByRole("button", { name: "Review registration policy", exact: true })
      .click();
    await page
      .getByRole("button", { name: "Apply reviewed change", exact: true })
      .click();
    await page
      .getByRole("button", {
        name: "Load shared Security settings",
        exact: true,
      })
      .click();
    await expect(
      settings.getByText("Current: ON.", { exact: false }),
    ).toBeVisible();
    const changes = fixture.calls
      .filter((call) => call.method === "ApplySecurityChanges")
      .map((call) => (call.body.currentReview as { changes: unknown }).changes);
    expect(changes).toEqual([
      [
        {
          putEdgePolicy: { version: 1, mode: "EDGE_ENDPOINT_MODE_WRITE_READ" },
        },
      ],
      [
        {
          putRegistrationPolicy: {
            version: 2,
            enabled: true,
            roleId: "personal_user",
            profiles: [
              {
                issuer: "https://idp.example",
                clientProfileId: "browser",
                kind: "REGISTRATION_PROFILE_KIND_BROWSER",
              },
            ],
          },
        },
      ],
    ]);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    ).toBe(true);
  });
}

test("read-only users can inspect saved records and verified email is rendered as text", async ({
  page,
}) => {
  const fixture = await securityUI(page, {
    readOnly: true,
    verifiedEmail: '<img src=x onerror="alert(1)">@example.test',
  });
  await page.goto(`${fixture.primary}/security/users`);
  const display = page.getByRole("button", {
    name: '<img src=x onerror="alert(1)">@example.test',
    exact: true,
  });
  await display.click();
  await expect(
    page
      .getByRole("region", { name: "Saved record details" })
      .getByText('<img src=x onerror="alert(1)">@example.test', {
        exact: true,
      }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Edit user state", exact: true }),
  ).toBeDisabled();
  await expect(page.locator('img[src="x"]')).toHaveCount(0);
  expect(
    fixture.calls.some((call) => call.method === "ApplySecurityChanges"),
  ).toBe(false);
});
