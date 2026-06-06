import { expect, test, type Page } from "@playwright/test";

const GATEWAY_URL =
  process.env.LANTERN_E2E_GATEWAY_URL ?? "http://127.0.0.1:6381";
const STORAGE_KEY = "lantern.admin.baseUrl";

const VERTEX_KEY = "e2e:crud:vertex";
const EDGE_TAIL = "e2e:crud:tail";
const EDGE_HEAD = "e2e:crud:head";

/**
 * Clean baseline so re-runs are deterministic — the previous test run's
 * leftovers must not influence behaviour.
 */
test.beforeAll(async () => {
  for (const key of [VERTEX_KEY, EDGE_TAIL, EDGE_HEAD]) {
    await fetch(`${GATEWAY_URL}/v1/vertices/${encodeURIComponent(key)}`, {
      method: "DELETE",
    }).catch(() => undefined);
  }
  await fetch(
    `${GATEWAY_URL}/v1/edges/${encodeURIComponent(EDGE_TAIL)}/${encodeURIComponent(EDGE_HEAD)}`,
    { method: "DELETE" },
  ).catch(() => undefined);

  // Seed a starting vertex + edge so the UI has something to load on the
  // detail pages before the user edits or replaces.
  const put = await fetch(`${GATEWAY_URL}/v1/vertices`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      vertices: [
        { key: VERTEX_KEY, value: { string: "seed" } },
        { key: EDGE_TAIL, value: { string: "tail" } },
        { key: EDGE_HEAD, value: { string: "head" } },
      ],
    }),
  });
  if (!put.ok) {
    throw new Error(`seed vertices failed: ${put.status} ${await put.text()}`);
  }
});

test.beforeEach(async ({ page }) => {
  await page.addInitScript(
    ({ key, value }) => {
      try {
        window.localStorage.setItem(key, value);
      } catch {
        // ignore — storage may be unavailable in private mode
      }
    },
    { key: STORAGE_KEY, value: GATEWAY_URL },
  );
});

test.describe("vertex detail", () => {
  test("loads the seeded vertex and switches kind via save round-trip", async ({
    page,
  }) => {
    await page.goto(`/vertices/${encodeURIComponent(VERTEX_KEY)}`);

    // Read view should mount with kind=string.
    await expect(page.getByTestId("vertex-detail-read")).toBeVisible();
    await expect(page.getByTestId("vertex-detail-key")).toHaveText(VERTEX_KEY);

    // Flip to edit mode and switch the kind to int32.
    await page.getByTestId("vertex-edit-trigger").click();
    const editForm = page.getByTestId("vertex-detail-edit");
    await expect(editForm).toBeVisible();
    await selectKind(page, "int32");
    await page.getByTestId("vertex-editor-int32 value").fill("42");

    // Save — the reducer will re-GET so the read view must reflect int32.
    await page.getByTestId("vertex-save").click();
    await expect(page.getByTestId("vertex-detail-read")).toBeVisible();

    const fetched = await fetch(
      `${GATEWAY_URL}/v1/vertices/${encodeURIComponent(VERTEX_KEY)}`,
    );
    expect(fetched.ok).toBeTruthy();
    const body = (await fetched.json()) as {
      vertex?: { int32?: number; string?: string };
    };
    expect(body.vertex?.int32).toBe(42);
    expect(body.vertex?.string).toBeUndefined();
  });

  test("invalid bytes input disables Save and does not mutate the server", async ({
    page,
  }) => {
    await page.goto(`/vertices/${encodeURIComponent(VERTEX_KEY)}`);
    await page.getByTestId("vertex-edit-trigger").click();
    await selectKind(page, "bytes");
    await page.getByTestId("vertex-editor-bytes").fill("not-hex-data!");

    // The codec invalidates the form so Save is unavailable — guarding
    // the round-trip before it ever reaches the gateway.
    await expect(page.getByTestId("vertex-save")).toBeDisabled();
    await expect(page.getByTestId("vertex-detail-edit")).toBeVisible();
  });

  test("delete removes the vertex and redirects to the listing", async ({
    page,
  }) => {
    // Use a one-shot key so the rest of the suite is unaffected.
    const oneShot = `${VERTEX_KEY}:delete`;
    await fetch(`${GATEWAY_URL}/v1/vertices`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        vertices: [{ key: oneShot, value: { string: "doomed" } }],
      }),
    });

    await page.goto(`/vertices/${encodeURIComponent(oneShot)}`);
    await expect(page.getByTestId("vertex-detail-read")).toBeVisible();
    await page.getByTestId("vertex-delete-trigger").click();
    await page.getByTestId("confirm-delete-vertex").click();

    await expect(page).toHaveURL(/\/vertices$/);

    const recheck = await fetch(
      `${GATEWAY_URL}/v1/vertices/${encodeURIComponent(oneShot)}`,
    );
    expect(recheck.status).toBe(404);
  });
});

test.describe("edge detail", () => {
  test("AddEdge accumulates weight and PutEdge replaces it", async ({
    page,
  }) => {
    // Reset edge state so this test owns the row.
    await fetch(
      `${GATEWAY_URL}/v1/edges/${encodeURIComponent(EDGE_TAIL)}/${encodeURIComponent(EDGE_HEAD)}`,
      { method: "DELETE" },
    ).catch(() => undefined);

    await page.goto(
      `/edges/${encodeURIComponent(EDGE_TAIL)}/${encodeURIComponent(EDGE_HEAD)}`,
    );

    // Either the row is missing (first run) or already exists — both are
    // acceptable starting states for the test. After the DELETE fixture the
    // page renders both `edge-detail-missing` (placeholder card) and
    // `edge-form-add` (the add-weight form) as siblings, so `.or()` must be
    // collapsed with `.first()` to relax Playwright strict-mode (#344).
    await expect(
      page
        .getByTestId("edge-form-add")
        .or(page.getByTestId("edge-detail-missing"))
        .first(),
    ).toBeVisible();

    // Add the same contribution twice. The exact accumulator math is
    // server-side; the test only asserts a write actually happened.
    await page.getByTestId("edge-add-weight").fill("1.5");
    await page.getByTestId("edge-add-submit").click();
    await expect(page.getByTestId("edge-detail-read")).toBeVisible();
    const afterFirst = await fetchEdgeWeight(EDGE_TAIL, EDGE_HEAD);
    expect(afterFirst).toBeGreaterThan(0);

    await page.getByTestId("edge-add-weight").fill("1.5");
    await page.getByTestId("edge-add-submit").click();
    await expect(page.getByTestId("edge-current-weight")).toBeVisible();

    const afterSecond = await fetchEdgeWeight(EDGE_TAIL, EDGE_HEAD);
    // A second AddEdge must accumulate strictly more weight than one.
    expect(afterSecond).toBeGreaterThan(afterFirst);

    // PutEdge collapses the accumulator to an exact value.
    await page.getByTestId("edge-put-weight").fill("7");
    await page.getByTestId("edge-put-submit").click();
    await expect(page.getByTestId("edge-current-weight")).toContainText("7");

    const afterPut = await fetchEdgeWeight(EDGE_TAIL, EDGE_HEAD);
    expect(afterPut).toBeCloseTo(7, 5);
  });

  test("delete removes the edge", async ({ page }) => {
    // Make sure something exists first.
    await fetch(`${GATEWAY_URL}/v1/edges/put`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        edges: [{ tail: EDGE_TAIL, head: EDGE_HEAD, weight: 1 }],
      }),
    });

    await page.goto(
      `/edges/${encodeURIComponent(EDGE_TAIL)}/${encodeURIComponent(EDGE_HEAD)}`,
    );
    await expect(page.getByTestId("edge-detail-read")).toBeVisible();
    await page.getByTestId("edge-delete-trigger").click();
    await page.getByTestId("confirm-delete-edge").click();

    await expect(page).toHaveURL(/\/edges$/);

    const recheck = await fetch(
      `${GATEWAY_URL}/v1/edges/${encodeURIComponent(EDGE_TAIL)}/${encodeURIComponent(EDGE_HEAD)}`,
    );
    expect(recheck.status).toBe(404);
  });
});

async function selectKind(page: Page, kind: string) {
  // Fluent UI Dropdown is a button — click then pick the option.
  await page.getByTestId("vertex-kind-selector").click();
  await page.getByRole("option", { name: kind, exact: true }).click();
}

async function fetchEdgeWeight(tail: string, head: string): Promise<number> {
  const res = await fetch(
    `${GATEWAY_URL}/v1/edges/${encodeURIComponent(tail)}/${encodeURIComponent(head)}`,
  );
  if (!res.ok) {
    throw new Error(`fetchEdgeWeight failed: ${res.status}`);
  }
  const body = (await res.json()) as { edge?: { weight?: number } };
  return body.edge?.weight ?? 0;
}
