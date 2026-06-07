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

  // #433 — Clear button empties the scrollback in place, leaving the
  // banner. Gateway override, skipConfirm, and history are preserved.
  test("Clear button empties the scrollback to just the banner", async ({
    page,
  }) => {
    await page.goto("/cli");
    const input = page.getByTestId("cli-input");
    // Initial state: only the banner entry. Clear button starts disabled.
    await expect(page.getByTestId("cli-entry-info")).toHaveCount(1);
    await expect(page.getByTestId("cli-clear")).toBeDisabled();
    // Run a couple of commands to grow the scrollback.
    await input.fill("get vertex cli:alpha");
    await input.press("Enter");
    await expect(page.getByTestId("cli-entry-ok").last()).toContainText(
      "first",
    );
    await input.fill("nonsense");
    await input.press("Enter");
    await expect(page.getByTestId("cli-entry-error").last()).toContainText(
      "usage:",
    );
    // Clear is now enabled. Click it; only the banner survives.
    await expect(page.getByTestId("cli-clear")).toBeEnabled();
    await page.getByTestId("cli-clear").click();
    await expect(page.getByTestId("cli-entry-ok")).toHaveCount(0);
    await expect(page.getByTestId("cli-entry-error")).toHaveCount(0);
    await expect(page.getByTestId("cli-entry-info")).toHaveCount(1);
    await expect(page.getByTestId("cli-clear")).toBeDisabled();
    // History survives clear: arrow-up restores the last command.
    await input.focus();
    await input.press("ArrowUp");
    await expect(input).toHaveValue("nonsense");
  });

  // #433 — Ctrl+L is the editor-conventional clear-screen binding.
  test("Ctrl+L clears the scrollback", async ({ page }) => {
    await page.goto("/cli");
    const input = page.getByTestId("cli-input");
    await input.fill("get vertex cli:alpha");
    await input.press("Enter");
    await expect(page.getByTestId("cli-entry-ok").last()).toContainText(
      "first",
    );
    await input.focus();
    await input.press("Control+l");
    await expect(page.getByTestId("cli-entry-ok")).toHaveCount(0);
    await expect(page.getByTestId("cli-entry-info")).toHaveCount(1);
  });

  // #433 — Cancel aborts the in-flight RPC. We slow-route the Connect
  // call through Playwright's request interception so the dispatch sits
  // long enough for the test to click Cancel before the network resolves.
  test("Cancel button aborts an in-flight RPC", async ({ page }) => {
    // Slow-route any Connect call: respond after ~5s so the test has
    // a generous window to click Cancel. The abort path closes the
    // request before the route handler ever fulfills.
    await page.route("**/graph.v1.LanternService/**", async (route) => {
      await new Promise((r) => setTimeout(r, 5000));
      await route.continue();
    });
    await page.goto("/cli");
    const input = page.getByTestId("cli-input");
    await input.fill("get vertex cli:alpha");
    await input.press("Enter");
    // Input disables while busy; Cancel button appears.
    await expect(input).toBeDisabled();
    await expect(page.getByTestId("cli-cancel")).toBeVisible();
    await page.getByTestId("cli-cancel").click();
    // The dispatch unwinds and the scrollback gains an info chip
    // reading "aborted" (not a red error chip).
    await expect(page.getByTestId("cli-entry-info").last()).toContainText(
      "aborted",
    );
    // Cancel disappears once the abort settles; input is editable
    // again for the next command.
    await expect(page.getByTestId("cli-cancel")).toHaveCount(0);
    await expect(input).toBeEnabled();
  });

  // #433 — Esc is the keyboard counterpart of the Cancel button.
  test("Esc aborts an in-flight RPC", async ({ page }) => {
    await page.route("**/graph.v1.LanternService/**", async (route) => {
      await new Promise((r) => setTimeout(r, 5000));
      await route.continue();
    });
    await page.goto("/cli");
    const input = page.getByTestId("cli-input");
    await input.fill("get vertex cli:alpha");
    await input.press("Enter");
    await expect(page.getByTestId("cli-cancel")).toBeVisible();
    // Input is disabled while busy, but it still owns focus and
    // receives keypresses, so Esc reaches the onKeyDown handler.
    await input.press("Escape");
    await expect(page.getByTestId("cli-entry-info").last()).toContainText(
      "aborted",
    );
    await expect(input).toBeEnabled();
  });
});
