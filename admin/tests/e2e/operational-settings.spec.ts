import { expect, test, type Page } from "@playwright/test";
import { create, fromJson, toJson, type JsonValue } from "@bufbuild/protobuf";
import { securityUI } from "./helpers";
import { version, review as makeReview } from "../../test/current-security";
import {
  GetOperationalSettingsResponseSchema,
  CurrentSecurityReviewSchema,
  PreviewOperationalPolicyRequestSchema,
  PreviewOperationalPolicyResponseSchema,
  LogPolicyAdoptionState,
  OperationalSettingsApplicationState,
  OperationalSettingsChangeMode,
  OperationalSettingsError,
  OperationalSettingsSource,
  type CurrentSecurityReview,
} from "../../../sdks/node/src/gen/graph/v1/security_pb";
async function operationalUI(
  page: Page,
  lost = false,
  unavailableSearch = false,
) {
  const fixture = await securityUI(page, lost ? { apply: "lost" } : {});
  let committed: CurrentSecurityReview | undefined;
  await page.route(
    `${fixture.primary}/**/GetOperationalSettings`,
    async (route) => {
      const response = create(GetOperationalSettingsResponseSchema, {
        schemaVersion: 1,
        member: 1,
        version,
        groups: Array.from({ length: 8 }, (_, i) => ({
          group: i + 1,
          adoption: LogPolicyAdoptionState.UNADOPTED,
          changeMode: OperationalSettingsChangeMode.UNAVAILABLE,
          application: OperationalSettingsApplicationState.UNAVAILABLE,
          error: OperationalSettingsError.OWNER_UNAVAILABLE,
        })),
      });
      const group = response.groups[0]!;
      group.effective = {
        $typeName: "graph.v1.OperationalPolicyValues",
        group: {
          case: "slowRpc",
          value: {
            $typeName: "graph.v1.OperationalSlowRPCValues",
            thresholdMs: 500n,
          },
        },
      };
      group.provenance = [
        {
          $typeName: "graph.v1.OperationalSettingProvenance",
          setting: 1,
          source: OperationalSettingsSource.ENVIRONMENT,
        },
      ];
      group.changeMode = OperationalSettingsChangeMode.HOT;
      group.application = OperationalSettingsApplicationState.APPLIED;
      group.error = OperationalSettingsError.NONE;
      const capacity = response.groups[4]!;
      capacity.effective = {
        $typeName: "graph.v1.OperationalPolicyValues",
        group: {
          case: "capacity",
          value: {
            $typeName: "graph.v1.OperationalCapacityValues",
            gcEdgeBudget: 0n,
            gcIntervalSeconds: 60n,
            maxVertices: 1n,
            maxEdges: 0n,
            maxVertexCausalEntries: 1n,
            maxEdgeCausalEntries: 0n,
          },
        },
      };
      capacity.changeMode = OperationalSettingsChangeMode.WORKER_RESTART;
      capacity.application = OperationalSettingsApplicationState.APPLIED;
      capacity.error = OperationalSettingsError.NONE;
      capacity.provenance = [19, 20, 21, 22, 23, 24].map((setting) => ({
        $typeName: "graph.v1.OperationalSettingProvenance",
        setting,
        source: OperationalSettingsSource.DEFAULT,
      }));
      const operation = committed?.changes[0]?.operation;
      if (operation?.case === "putOperationalPolicy") {
        group.desired = {
          $typeName: "graph.v1.ManagedOperationalPolicy",
          version: 1,
          generation: 1n,
          values: operation.value.values,
          compatibilityFingerprint: "",
        };
        group.adoption = LogPolicyAdoptionState.MANAGED;
        group.application = OperationalSettingsApplicationState.PENDING;
        group.preparation = {
          $typeName: "graph.v1.LogPolicyPreparationReference",
          changeId: committed!.changeId,
          intentDigest: committed!.intentDigest,
          actorDigest: new Uint8Array(32).fill(2),
        };
      }
      if (unavailableSearch) {
        const search = response.groups[6]!,
          original = makeReview();
        search.desired = {
          $typeName: "graph.v1.ManagedOperationalPolicy",
          version: 1,
          generation: 5n,
          compatibilityFingerprint: "b".repeat(64),
          values: {
            $typeName: "graph.v1.OperationalPolicyValues",
            group: {
              case: "search",
              value: {
                $typeName: "graph.v1.OperationalSearchValues",
                defaultLimit: 100n,
                maxLimit: 1000n,
                defaultMode: 1,
                defaultMinShould: 1n,
                timeoutMs: 5000n,
                maxQueryBytes: 1000n,
                maxQueryTerms: 100n,
                maxDictionaryVisits: 100n,
                maxPostingVisits: 100n,
                maxPositionVisits: 100n,
                maxExpirationVisits: 100n,
              },
            },
          },
        };
        search.adoption = LogPolicyAdoptionState.MANAGED;
        search.preparation = {
          $typeName: "graph.v1.LogPolicyPreparationReference",
          changeId: original.changeId,
          intentDigest: original.intentDigest,
          actorDigest: new Uint8Array(32).fill(2),
        };
      }
      await route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(
          toJson(GetOperationalSettingsResponseSchema, response),
        ),
      });
    },
  );
  await page.route(
    `${fixture.primary}/**/PreviewOperationalPolicy`,
    async (route) => {
      const request = fromJson(
        PreviewOperationalPolicyRequestSchema,
        route.request().postDataJSON() as JsonValue,
      );
      const candidate = request.values?.group;
      if (candidate?.case !== "capacity")
        throw new Error("Unexpected preview group.");
      const over = (usage: bigint, cap: bigint) => cap > 0n && usage > cap;
      const response = create(PreviewOperationalPolicyResponseSchema, {
        schemaVersion: 1,
        group: 5,
        version,
        capacityUsage: {
          member: 1,
          effectiveGeneration: 0n,
          vertexFootprint: 3n,
          edgeFootprint: 2n,
          vertexCausalEntries: 3n,
          edgeCausalEntries: 2n,
          verticesOverLimit: over(3n, candidate.value.maxVertices),
          edgesOverLimit: over(2n, candidate.value.maxEdges),
          vertexCausalOverLimit: over(
            3n,
            candidate.value.maxVertexCausalEntries,
          ),
          edgeCausalOverLimit: over(2n, candidate.value.maxEdgeCausalEntries),
        },
      });
      await route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(
          toJson(PreviewOperationalPolicyResponseSchema, response),
        ),
      });
    },
  );
  await page.route(
    `${fixture.primary}/**/ApplySecurityChanges`,
    async (route) => {
      const body = route.request().postDataJSON();
      const reviewed = fromJson(
        CurrentSecurityReviewSchema,
        body.currentReview as JsonValue,
      );
      if (reviewed.changes[0]?.operation.case === "putOperationalPolicy")
        committed = reviewed;
      await route.fallback();
    },
  );
  return fixture;
}
async function reviewSlow(page: Page) {
  const card = page.getByRole("region", {
    name: "Slow RPC diagnostics",
    exact: true,
  });
  await card
    .getByRole("button", { name: "Edit slow rpc diagnostics", exact: true })
    .click();
  await card
    .getByRole("button", {
      name: "Use this node’s values for adoption",
      exact: true,
    })
    .click();
  await card.getByLabel("Threshold (ms)", { exact: true }).fill("125");
  await card
    .getByRole("button", {
      name: "Review slow rpc diagnostics change",
      exact: true,
    })
    .click();
  await expect(
    page.getByRole("button", {
      name: "Apply reviewed operational change",
      exact: true,
    }),
  ).toBeEnabled();
}
test.afterEach(async ({ page }) => {
  await page.unrouteAll({ behavior: "wait" });
  await page.context().unrouteAll({ behavior: "wait" });
});
for (const width of [1280, 390])
  test(`operational atomic adoption and this-node pending status at ${width}px`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    const fixture = await operationalUI(page);
    await page.goto(`${fixture.primary}/settings`);
    await expect(
      page.getByRole("heading", { name: "Operational policies", exact: true }),
    ).toBeVisible();
    await expect(
      page
        .getByRole("region", { name: "Request admission", exact: true })
        .getByRole("button", { name: "Edit request admission", exact: true }),
    ).toBeDisabled();
    const capacity = page.getByRole("region", {
      name: "Graph capacity and garbage collection",
      exact: true,
    });
    await capacity
      .getByRole("button", {
        name: "Edit graph capacity and garbage collection",
        exact: true,
      })
      .click();
    await capacity
      .getByRole("button", {
        name: "Use this node’s values for adoption",
        exact: true,
      })
      .click();
    await capacity
      .getByRole("button", {
        name: "Preview connected-node capacity",
        exact: true,
      })
      .click();
    const sample = capacity.getByRole("table", {
      name: "Capacity usage sample",
      exact: true,
    });
    await expect(
      sample.getByRole("row").filter({ hasText: "Vertex admission footprint" }),
    ).toHaveText("Vertex admission footprint3Yes");
    await expect(
      sample.getByRole("row").filter({ hasText: "Edge admission footprint" }),
    ).toHaveText("Edge admission footprint2No");
    await expect(capacity).toContainText("Counters are sampled separately");
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await capacity.screenshot({
      path: testInfo.outputPath(`capacity-preview-${width}.png`),
      animations: "disabled",
    });
    await capacity.getByLabel("Max vertices", { exact: true }).fill("2");
    await expect(sample).toHaveCount(0);
    await expect(
      capacity.getByRole("button", {
        name: "Review graph capacity and garbage collection change",
        exact: true,
      }),
    ).toBeEnabled();
    await reviewSlow(page);
    await expect(
      page.getByRole("region", {
        name: "Review operational policy change",
        exact: true,
      }),
    ).toContainText("Expected generation 0");
    await page
      .getByRole("button", {
        name: "Apply reviewed operational change",
        exact: true,
      })
      .click();
    await page
      .getByRole("button", { name: "Reload operational settings", exact: true })
      .click();
    const card = page.getByRole("region", {
      name: "Slow RPC diagnostics",
      exact: true,
    });
    await expect(
      card.getByText("Pending on this node", { exact: true }),
    ).toBeVisible();
    await expect(card.getByText("1 / 0", { exact: true })).toBeVisible();
    await expect(card.getByText("125", { exact: true })).toBeVisible();
    expect(
      fixture.calls.filter((c) => c.method === "ApplySecurityChanges"),
    ).toHaveLength(1);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await card.screenshot({
      path: testInfo.outputPath(`operational-policy-${width}.png`),
      animations: "disabled",
    });
  });
test("operational response loss reload recovers exact original without another Apply", async ({
  page,
}) => {
  const fixture = await operationalUI(page, true);
  await page.goto(`${fixture.primary}/settings`);
  await reviewSlow(page);
  await page
    .getByRole("button", {
      name: "Apply reviewed operational change",
      exact: true,
    })
    .click();
  await expect(
    page.getByRole("region", { name: "Operational policies", exact: true }),
  ).toContainText("The response was not confirmed.");
  const first = fixture.calls.find((c) => c.method === "ApplySecurityChanges")!;
  await page.reload();
  const card = page.getByRole("region", {
    name: "Slow RPC diagnostics",
    exact: true,
  });
  await expect(
    card.getByRole("button", {
      name: "Edit slow rpc diagnostics",
      exact: true,
    }),
  ).toBeDisabled();
  await page
    .getByRole("button", {
      name: "Check original operational change status",
      exact: true,
    })
    .click();
  const status = fixture.calls
    .filter((c) => c.method === "GetSecurityChangeStatus")
    .at(-1)!;
  const original = first.body.currentReview as {
    changeId: unknown;
    intentDigest: unknown;
  };
  expect(status.body.currentChangeId).toEqual(original.changeId);
  expect(status.body.currentIntentDigest).toEqual(original.intentDigest);
  expect(
    fixture.calls.filter((c) => c.method === "ApplySecurityChanges"),
  ).toHaveLength(1);
});

test("operational unavailable owner preserves readonly desired values and Search fingerprint", async ({
  page,
}) => {
  const fixture = await operationalUI(page, false, true);
  await page.goto(`${fixture.primary}/settings`);
  const search = page.getByRole("region", {
    name: "Search request policy",
    exact: true,
  });
  await expect(search.getByText("5 / 0", { exact: true })).toBeVisible();
  await expect(
    search.getByRole("row").filter({ hasText: "Max limit" }),
  ).toContainText("1000");
  await expect(search).toContainText(
    "Effective Search fingerprint: Unavailable",
  );
  await expect(search).toContainText("b".repeat(64));
  await expect(
    search.getByRole("button", {
      name: "Edit search request policy",
      exact: true,
    }),
  ).toBeDisabled();
});

test("inline drafts, accessible field guidance, invalid values and Cancel preserve node values", async ({
  page,
}) => {
  const fixture = await operationalUI(page);
  await page.setViewportSize({ width: 390, height: 900 });
  await page.goto(`${fixture.primary}/settings`);
  const card = page.getByRole("region", {
    name: "Slow RPC diagnostics",
    exact: true,
  });
  const row = card.getByRole("row").filter({ hasText: "Threshold (ms)" });
  await expect(row.getByLabel("Threshold (ms)", { exact: true })).toHaveCount(
    1,
  );
  await card
    .getByRole("button", { name: "Edit slow rpc diagnostics", exact: true })
    .click();
  await card
    .getByRole("button", {
      name: "Use this node’s values for adoption",
      exact: true,
    })
    .click();
  const input = row.getByLabel("Threshold (ms)", { exact: true });
  await input.fill("-1");
  await expect(input).toHaveAttribute("aria-invalid", "true");
  await expect(row).toContainText(
    "Enter a whole number from 0 to 9223372036854.",
  );
  const help = row.getByText("Allowed values and details", { exact: true });
  await help.focus();
  await page.keyboard.press("Enter");
  await expect(
    row.getByText("0 disables slow RPC diagnostics.", { exact: false }),
  ).toBeVisible();
  await input.fill("125");
  await expect(input).not.toHaveAttribute("aria-invalid", "true");
  await expect(row.locator('[data-label="Effective"]')).toHaveText("500");
  await expect(row.locator('[data-label="Desired"]')).toHaveText("Not adopted");
  await card.getByRole("button", { name: "Cancel draft", exact: true }).click();
  await expect(input).toBeDisabled();
  await expect(input).toHaveValue("500");
  await expect(
    card.getByRole("button", {
      name: "Review slow rpc diagnostics change",
      exact: true,
    }),
  ).toHaveCount(0);
  expect(
    fixture.calls.filter((call) => call.method === "ApplySecurityChanges"),
  ).toHaveLength(0);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
});
