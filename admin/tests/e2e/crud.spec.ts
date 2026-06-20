import { expect, test, type Page } from "@playwright/test";

import { CONNECT_URL, STORAGE_KEY, connectCall, putVertices } from "./helpers";

const VERTEX_KEY = "e2e:crud:vertex";
const EDGE_TAIL = "e2e:crud:tail";
const EDGE_HEAD = "e2e:crud:head";

/**
 * Clean baseline so re-runs are deterministic — the previous test run's
 * leftovers must not influence behaviour.
 */
test.beforeAll(async () => {
  for (const key of [VERTEX_KEY, EDGE_TAIL, EDGE_HEAD]) {
    await connectCall("DeleteVertex", { key }).catch(() => undefined);
  }
  await connectCall("DeleteEdge", { tail: EDGE_TAIL, head: EDGE_HEAD }).catch(
    () => undefined,
  );

  // Seed a starting vertex + edge so the UI has something to load on the
  // detail pages before the user edits or replaces.
  await putVertices([
    { key: VERTEX_KEY, string: "seed" },
    { key: EDGE_TAIL, string: "tail" },
    { key: EDGE_HEAD, string: "head" },
  ]);
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
    { key: STORAGE_KEY, value: CONNECT_URL },
  );
});

test.describe("vertex detail", () => {
  test("string values render multi-line with a working Markdown toggle", async ({
    page,
  }) => {
    // Self-contained key so this test is independent of the kind-switch
    // test that mutates VERTEX_KEY.
    const key = `${VERTEX_KEY}:markdown`;
    const markdown = [
      "# Heading One",
      "",
      "This is a **bold** statement with a [link](https://example.com).",
      "",
      "- first item",
      "- second item",
    ].join("\n");
    await putVertices([{ key, string: markdown }]);

    await page.goto(`/vertices/${encodeURIComponent(key)}`);
    await expect(page.getByTestId("vertex-detail-read")).toBeVisible();

    // The detail surface uses the multi-line StringValueView (#644), not
    // the compact 48-char table ValueCell — the whole value is present and
    // newlines are preserved.
    const view = page.getByTestId("vertex-string-view");
    await expect(view).toBeVisible();
    const raw = page.getByTestId("vertex-string-raw");
    await expect(raw).toBeVisible();
    await expect(raw).toContainText("# Heading One");
    await expect(raw).toContainText("second item");

    // Default is Raw, so nothing is rendered as Markdown yet.
    await expect(page.getByTestId("vertex-string-markdown")).toHaveCount(0);

    // Flip the toggle — the raw block is replaced by rendered Markdown.
    const toggle = view.getByRole("switch");
    await toggle.click();
    await expect(toggle).toBeChecked();

    const rendered = page.getByTestId("vertex-string-markdown");
    await expect(rendered).toBeVisible();
    await expect(
      rendered.getByRole("heading", { name: "Heading One" }),
    ).toBeVisible();
    // Links open safely in a new tab (custom anchor renderer).
    await expect(rendered.getByRole("link", { name: "link" })).toHaveAttribute(
      "target",
      "_blank",
    );
    await expect(page.getByTestId("vertex-string-raw")).toHaveCount(0);
  });

  test("JSON string values render linted and syntax-highlighted with a Raw tab (#759)", async ({
    page,
  }) => {
    const key = `${VERTEX_KEY}:json`;
    await putVertices([
      {
        key,
        string: '{"role":"admin","name":"Alice","score":9,"active":true}',
      },
    ]);

    await page.goto(`/vertices/${encodeURIComponent(key)}`);
    await expect(page.getByTestId("vertex-detail-read")).toBeVisible();

    const view = page.getByTestId("vertex-string-view");
    await expect(view).toBeVisible();

    // JSON is detected, so the prose Markdown Switch is replaced by a
    // JSON ⇄ Raw TabList and the highlighted view leads by default.
    await expect(page.getByTestId("vertex-string-markdown-toggle")).toHaveCount(
      0,
    );
    const json = page.getByTestId("vertex-string-json");
    await expect(json).toBeVisible();

    // Linted: the compact input is pretty-printed with indentation.
    await expect(json).toContainText('"role": "admin"');
    await expect(json).toContainText('"score": 9');
    // Syntax-highlighted: tokens are wrapped in coloured spans.
    await expect(
      json.locator("span").filter({ hasText: "admin" }).first(),
    ).toBeVisible();

    // The Raw tab reveals the original, unformatted string.
    await page.getByTestId("vertex-string-raw-tab").click();
    const raw = page.getByTestId("vertex-string-raw");
    await expect(raw).toBeVisible();
    await expect(raw).toContainText(
      '{"role":"admin","name":"Alice","score":9,"active":true}',
    );
    await expect(page.getByTestId("vertex-string-json")).toHaveCount(0);
  });

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

    const body = (await connectCall("GetVertex", { key: VERTEX_KEY })) as {
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
    await putVertices([{ key: oneShot, string: "doomed" }]);

    await page.goto(`/vertices/${encodeURIComponent(oneShot)}`);
    await expect(page.getByTestId("vertex-detail-read")).toBeVisible();
    await page.getByTestId("vertex-delete-trigger").click();
    await page.getByTestId("confirm-delete-vertex").click();

    await expect(page).toHaveURL(/\/vertices$/);

    // GetVertex on a deleted key returns NotFound, which connectCall
    // surfaces as a thrown error — catching it is the assertion.
    let notFound = false;
    try {
      await connectCall("GetVertex", { key: oneShot });
    } catch {
      notFound = true;
    }
    expect(notFound).toBe(true);
  });
});

test.describe("edge detail", () => {
  test("AddEdge accumulates weight and PutEdge replaces it", async ({
    page,
  }) => {
    // Reset edge state so this test owns the row.
    await connectCall("DeleteEdge", {
      tail: EDGE_TAIL,
      head: EDGE_HEAD,
    }).catch(() => undefined);

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
    await connectCall("PutEdges", {
      edges: [{ tail: EDGE_TAIL, head: EDGE_HEAD, weight: 1 }],
    });

    await page.goto(
      `/edges/${encodeURIComponent(EDGE_TAIL)}/${encodeURIComponent(EDGE_HEAD)}`,
    );
    await expect(page.getByTestId("edge-detail-read")).toBeVisible();
    await page.getByTestId("edge-delete-trigger").click();
    await page.getByTestId("confirm-delete-edge").click();

    await expect(page).toHaveURL(/\/edges$/);

    let edgeGone = false;
    try {
      await connectCall("GetEdge", { tail: EDGE_TAIL, head: EDGE_HEAD });
    } catch {
      edgeGone = true;
    }
    expect(edgeGone).toBe(true);
  });
});

async function selectKind(page: Page, kind: string) {
  // Fluent UI Dropdown is a button — click then pick the option.
  await page.getByTestId("vertex-kind-selector").click();
  await page.getByRole("option", { name: kind, exact: true }).click();
}

async function fetchEdgeWeight(tail: string, head: string): Promise<number> {
  const body = (await connectCall("GetEdge", { tail, head })) as {
    edge?: { weight?: number };
  };
  return body.edge?.weight ?? 0;
}
