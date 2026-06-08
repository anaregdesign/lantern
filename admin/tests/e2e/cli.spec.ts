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

  // #465 — splitter L/R layout. The splitter is hidden while no graph
  // has been rendered yet; once a graph-producing command lands, the
  // page switches to a two-column grid with the canvas on the left,
  // the toolbar/scrollback/input on the right, and a draggable splitter
  // between them.
  test("splitter is hidden until a graph-producing command lands (#465)", async ({
    page,
  }) => {
    await page.goto("/cli");
    await expect(page.getByTestId("cli-splitter")).toHaveCount(0);
    // data-mode reads "cli" while no graph is present.
    await expect(page.getByTestId("cli-root")).toHaveAttribute(
      "data-mode",
      "cli",
    );
    const input = page.getByTestId("cli-input");
    await input.fill("get vertex cli:alpha");
    await input.press("Enter");
    await expect(page.getByTestId("cli-canvas-panel")).toBeVisible();
    await expect(page.getByTestId("cli-root")).toHaveAttribute(
      "data-mode",
      "split",
    );
    await expect(page.getByTestId("cli-splitter")).toBeVisible();
  });

  test("dragging the splitter changes the column ratio and persists it (#465)", async ({
    page,
  }) => {
    // Playwright's `fullyParallel` runs each test in a fresh browser
    // context, so localStorage starts empty and the splitter takes
    // the default 0.6 ratio without any explicit cleanup.
    await page.goto("/cli");
    const input = page.getByTestId("cli-input");
    await input.fill("get vertex cli:alpha");
    await input.press("Enter");
    const splitter = page.getByTestId("cli-splitter");
    await expect(splitter).toBeVisible();
    // Initial aria-valuenow reflects the 60% default.
    await expect(splitter).toHaveAttribute("aria-valuenow", "60");
    // Drag the handle ~150px to the left and verify the ratio shrank.
    // 150px against the ~1100px-wide root is well inside the 360px
    // min-pane clamp on both sides.
    const box = await splitter.boundingBox();
    if (!box) throw new Error("splitter has no bounding box");
    const startX = box.x + box.width / 2;
    const startY = box.y + box.height / 2;
    await page.mouse.move(startX, startY);
    await page.mouse.down();
    await page.mouse.move(startX - 150, startY, { steps: 12 });
    await page.mouse.up();
    // aria-valuenow should now be noticeably below 60 and well above
    // the min-pane clamp.
    const afterDrag = Number(await splitter.getAttribute("aria-valuenow"));
    expect(afterDrag).toBeLessThan(55);
    expect(afterDrag).toBeGreaterThan(35);
    // localStorage holds the persisted value.
    const stored = await page.evaluate(() =>
      window.localStorage.getItem("cli.splitRatio"),
    );
    expect(stored).not.toBeNull();
    const storedNum = Number.parseFloat(stored ?? "");
    expect(storedNum).toBeGreaterThan(0);
    expect(storedNum).toBeLessThan(1);
    // Reload and verify the splitter starts at the persisted ratio.
    await page.reload();
    await page.getByTestId("cli-input").fill("get vertex cli:alpha");
    await page.getByTestId("cli-input").press("Enter");
    const splitter2 = page.getByTestId("cli-splitter");
    await expect(splitter2).toBeVisible();
    const persisted = Number(await splitter2.getAttribute("aria-valuenow"));
    // Should be within 1pp of the post-drag value (rounded to integer pct).
    expect(Math.abs(persisted - afterDrag)).toBeLessThanOrEqual(1);
  });

  test("double-clicking the splitter resets to the default ratio (#465)", async ({
    page,
  }) => {
    // Pre-seed a non-default ratio so the reset is observable. 0.45
    // sits safely inside the 360px-min clamp at desktop widths.
    await page.addInitScript(() => {
      try {
        window.localStorage.setItem("cli.splitRatio", "0.4500");
      } catch {
        // intentionally empty
      }
    });
    await page.goto("/cli");
    const input = page.getByTestId("cli-input");
    await input.fill("get vertex cli:alpha");
    await input.press("Enter");
    const splitter = page.getByTestId("cli-splitter");
    await expect(splitter).toBeVisible();
    await expect(splitter).toHaveAttribute("aria-valuenow", "45");
    await splitter.dblclick();
    await expect(splitter).toHaveAttribute("aria-valuenow", "60");
    const stored = await page.evaluate(() =>
      window.localStorage.getItem("cli.splitRatio"),
    );
    expect(stored).toBeNull();
  });

  test("auto-scroll keeps the latest scrollback entry visible after a graph command (#465)", async ({
    page,
  }) => {
    await page.goto("/cli");
    const input = page.getByTestId("cli-input");
    // Pile up enough entries that the scrollback would naturally need
    // to scroll for the latest one to be visible.
    for (let i = 0; i < 6; i++) {
      await input.fill("get vertex cli:alpha");
      await input.press("Enter");
      await expect(page.getByTestId("cli-entry-ok").last()).toContainText(
        "first",
      );
    }
    // The most recent ok entry must be in the viewport AND scrolled
    // into view inside the scrollback container.
    const last = page.getByTestId("cli-entry-ok").last();
    await expect(last).toBeInViewport();
  });

  // #464 — the click-to-illuminate axis picker is the operator's
  // primary control for tuning the next click. These tests cover the
  // user-visible contract:
  //   1. Default-state preview equals the canonical short form
  //      (regression guard for #439).
  //   2. Tweaking controls updates the preview deterministically and
  //      the canvas-header hint mirrors the picker (single source of
  //      truth via `formatIlluminateClick`).
  //   3. localStorage round-trips axes across a reload so a tuned
  //      exploration session survives a refresh.
  test("default picker state previews the canonical short-form click (#464)", async ({
    page,
  }) => {
    await page.goto("/cli");
    const picker = page.getByTestId("cli-axis-picker");
    await expect(picker).toBeVisible();
    // Defaults: step=2, k=5, algorithm=none, objective=min, weighting=raw
    // → short form `illuminate <key> 2 5`.
    await expect(page.getByTestId("cli-axis-preview")).toHaveText(
      "illuminate <key> 2 5",
    );
    await expect(page.getByTestId("cli-axis-step")).toHaveValue("2");
    await expect(page.getByTestId("cli-axis-k")).toHaveValue("5");
    await expect(page.getByTestId("cli-axis-tfidf")).not.toBeChecked();
    // Canvas-header hint mirrors the picker; render a graph first so
    // the canvas panel mounts.
    const input = page.getByTestId("cli-input");
    await input.fill("get vertex cli:alpha");
    await input.press("Enter");
    await expect(page.getByTestId("cli-canvas-panel")).toBeVisible();
    await expect(page.getByTestId("cli-click-hint")).toHaveText(
      "illuminate <key> 2 5",
    );
  });

  test("tuning axes updates the picker preview to the long form (#464)", async ({
    page,
  }) => {
    await page.goto("/cli");
    const preview = page.getByTestId("cli-axis-preview");
    await expect(preview).toHaveText("illuminate <key> 2 5");

    // Bump step and k to long-form values.
    const step = page.getByTestId("cli-axis-step");
    await step.fill("3");
    await expect(preview).toHaveText("illuminate <key> 3 5");
    const k = page.getByTestId("cli-axis-k");
    await k.fill("10");
    await expect(preview).toHaveText("illuminate <key> 3 10");

    // Pick algorithm=spt via the Dropdown.
    await page.getByTestId("cli-axis-algorithm").click();
    await page.getByRole("option", { name: "Shortest-path tree" }).click();
    await expect(preview).toHaveText("illuminate <key> 3 10 algorithm=spt");

    // Pick objective=max.
    await page.getByTestId("cli-axis-objective").click();
    await page.getByRole("option", { name: /Maximize/ }).click();
    await expect(preview).toHaveText(
      "illuminate <key> 3 10 algorithm=spt objective=max",
    );

    // Flip TF-IDF on. Token order must be algorithm → objective → weighting.
    await page.getByTestId("cli-axis-tfidf").click();
    await expect(preview).toHaveText(
      "illuminate <key> 3 10 algorithm=spt objective=max weighting=tfidf",
    );

    // Render the canvas so the header hint shows up, and verify it
    // tracks the picker.
    const input = page.getByTestId("cli-input");
    await input.fill("get vertex cli:alpha");
    await input.press("Enter");
    await expect(page.getByTestId("cli-canvas-panel")).toBeVisible();
    await expect(page.getByTestId("cli-click-hint")).toHaveText(
      "illuminate <key> 3 10 algorithm=spt objective=max weighting=tfidf",
    );
  });

  test("picker state persists across a reload (#464)", async ({ page }) => {
    await page.goto("/cli");
    await page.getByTestId("cli-axis-step").fill("4");
    await page.getByTestId("cli-axis-k").fill("12");
    await page.getByTestId("cli-axis-algorithm").click();
    await page.getByRole("option", { name: "Spanning tree" }).click();
    await expect(page.getByTestId("cli-axis-preview")).toHaveText(
      "illuminate <key> 4 12 algorithm=mst",
    );
    // Reload — picker should hydrate from localStorage on mount.
    await page.reload();
    await expect(page.getByTestId("cli-axis-step")).toHaveValue("4");
    await expect(page.getByTestId("cli-axis-k")).toHaveValue("12");
    await expect(page.getByTestId("cli-axis-preview")).toHaveText(
      "illuminate <key> 4 12 algorithm=mst",
    );
  });
});
