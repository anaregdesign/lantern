import { expect, test } from "@playwright/test";

import { CONNECT_URL, STORAGE_KEY, putEdges, putVertices } from "./helpers";

/**
 * Seeds a couple of vertices for the CLI's get / scan happy paths.
 * `cli:` prefix avoids cross-talk with the other e2e specs that
 * share the same gateway.
 */
test.beforeAll(async () => {
  await putVertices([
    { key: "cli:alpha", string: "first" },
    { key: "cli:beta", string: "second" },
  ]);
  // Edge so illuminate / get edge happy-paths in the canvas spec
  // below have something to render.
  await putEdges([{ tail: "cli:alpha", head: "cli:beta", weight: 2 }]);
});

test.beforeEach(async ({ page }) => {
  await page.addInitScript(
    ({ key, value }) => {
      try {
        window.localStorage.setItem(key, value);
      } catch {
        // Storage may be unavailable in some browser modes.
      }
    },
    { key: STORAGE_KEY, value: CONNECT_URL },
  );
});

test.describe("/cli", () => {
  test("renders the prompt + intro banner", async ({ page }) => {
    await page.goto("/cli");
    await expect(page.getByTestId("cli-root")).toBeVisible();
    await expect(page.getByTestId("cli-input")).toBeEnabled();
  });

  test("get vertex round-trips a seeded value", async ({ page }) => {
    await page.goto("/cli");
    const input = page.getByTestId("cli-input");
    await input.fill("get vertex cli:alpha");
    await input.press("Enter");
    const ok = page.getByTestId("cli-entry-ok");
    await expect(ok.last()).toContainText("first");
  });

  test("invalid verb surfaces the parser usage hint", async ({ page }) => {
    await page.goto("/cli");
    const input = page.getByTestId("cli-input");
    await input.fill("nonsense");
    await input.press("Enter");
    const err = page.getByTestId("cli-entry-error");
    await expect(err.last()).toContainText("usage:");
  });

  test("destructive verb shows the confirmation chip", async ({ page }) => {
    await page.goto("/cli");
    const input = page.getByTestId("cli-input");
    await input.fill("delete vertex cli:never-existed");
    await input.press("Enter");
    await expect(page.getByTestId("cli-confirm")).toBeVisible();
    await page.getByTestId("cli-confirm-cancel").click();
    await expect(page.getByTestId("cli-confirm")).toBeHidden();
  });

  // #439 — graph-producing verbs render the IlluminateCanvas above
  // the scrollback so the operator can see what they just touched.
  test("get vertex renders the canvas panel", async ({ page }) => {
    await page.goto("/cli");
    // Canvas is hidden until the first graph-producing command lands.
    await expect(page.getByTestId("cli-canvas-panel")).toHaveCount(0);
    const input = page.getByTestId("cli-input");
    await input.fill("get vertex cli:alpha");
    await input.press("Enter");
    await expect(page.getByTestId("cli-entry-ok").last()).toContainText(
      "first",
    );
    await expect(page.getByTestId("cli-canvas-panel")).toBeVisible();
    await expect(page.getByTestId("cli-canvas-panel")).toContainText(
      "get vertex cli:alpha",
    );
  });

  test("illuminate persists across non-graph commands", async ({ page }) => {
    await page.goto("/cli");
    const input = page.getByTestId("cli-input");
    // Render a graph first.
    await input.fill("illuminate cli:alpha 2 5");
    await input.press("Enter");
    await expect(page.getByTestId("cli-canvas-panel")).toBeVisible();
    await expect(page.getByTestId("cli-canvas-panel")).toContainText(
      "illuminate cli:alpha 2 5",
    );
    // A non-graph command (parser usage hint here) must NOT clear the
    // canvas \u2014 the operator's exploration context survives mistakes.
    await input.fill("nonsense");
    await input.press("Enter");
    await expect(page.getByTestId("cli-entry-error").last()).toContainText(
      "usage:",
    );
    await expect(page.getByTestId("cli-canvas-panel")).toBeVisible();
    await expect(page.getByTestId("cli-canvas-panel")).toContainText(
      "illuminate cli:alpha 2 5",
    );
  });
});
