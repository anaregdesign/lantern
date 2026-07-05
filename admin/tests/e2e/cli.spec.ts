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

  // #942 — a barbell so `illuminate <seed> algorithm=community` has a
  // clean cluster to extract. Two tight triangles (internal weight 5,
  // bidirectional) joined by a single weak bridge (weight 0.1). A
  // LocalCommunity (#845) sweep from a1 cuts the bridge and returns
  // exactly {a1,a2,a3}; a plain BFS walk (the pre-fix regression) would
  // instead cross the bridge and pull in b1 at hop 1. `cli:cmty:` prefix
  // isolates the subgraph from the other seeds sharing this gateway.
  await putVertices([
    { key: "cli:cmty:a1", string: "a1" },
    { key: "cli:cmty:a2", string: "a2" },
    { key: "cli:cmty:a3", string: "a3" },
    { key: "cli:cmty:b1", string: "b1" },
    { key: "cli:cmty:b2", string: "b2" },
    { key: "cli:cmty:b3", string: "b3" },
  ]);
  await putEdges([
    // Triangle A (tight).
    { tail: "cli:cmty:a1", head: "cli:cmty:a2", weight: 5 },
    { tail: "cli:cmty:a2", head: "cli:cmty:a1", weight: 5 },
    { tail: "cli:cmty:a2", head: "cli:cmty:a3", weight: 5 },
    { tail: "cli:cmty:a3", head: "cli:cmty:a2", weight: 5 },
    { tail: "cli:cmty:a1", head: "cli:cmty:a3", weight: 5 },
    { tail: "cli:cmty:a3", head: "cli:cmty:a1", weight: 5 },
    // Triangle B (tight).
    { tail: "cli:cmty:b1", head: "cli:cmty:b2", weight: 5 },
    { tail: "cli:cmty:b2", head: "cli:cmty:b1", weight: 5 },
    { tail: "cli:cmty:b2", head: "cli:cmty:b3", weight: 5 },
    { tail: "cli:cmty:b3", head: "cli:cmty:b2", weight: 5 },
    { tail: "cli:cmty:b1", head: "cli:cmty:b3", weight: 5 },
    { tail: "cli:cmty:b3", head: "cli:cmty:b1", weight: 5 },
    // Weak bridge joining the two triangles.
    { tail: "cli:cmty:a1", head: "cli:cmty:b1", weight: 0.1 },
    { tail: "cli:cmty:b1", head: "cli:cmty:a1", weight: 0.1 },
  ]);
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

  test("destructive verb runs immediately with no confirmation chip", async ({
    page,
  }) => {
    await page.goto("/cli");
    const input = page.getByTestId("cli-input");
    await input.fill("delete vertex cli:never-existed");
    await input.press("Enter");
    // No confirmation chip — the verb dispatches straight through (#521).
    await expect(page.getByTestId("cli-confirm")).toHaveCount(0);
    await expect(page.getByTestId("cli-entry-ok").last()).toBeVisible();
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

  // #942 — `algorithm=community` must reach the LocalCommunity (#845)
  // family, not silently fall back to a BFS walk. Regression guard: before
  // the fix, dispatcher.ts's `ALGORITHM_TO_API` lacked a `community` entry
  // (typed `Record<string, …>`, so the miss was invisible) and the wire
  // adapter built `opts.bfs` instead of `opts.community`. On the barbell
  // seeded above, that BFS walk from a1 crosses the weak bridge and pulls in
  // b1 at hop 1; the real community extraction cuts the bridge and returns
  // exactly the seed's triangle {a1,a2,a3}. We assert on the rendered canvas
  // membership via the test bridge (present/absent), which the CLI reconcile
  // overwrites per frame (graph-view.ts), so stale nodes cannot leak in.
  test("illuminate algorithm=community extracts the seed cluster, not a BFS frontier (#942)", async ({
    page,
  }) => {
    await page.goto("/cli");
    const input = page.getByTestId("cli-input");
    await input.fill("illuminate cli:cmty:a1 2 5 algorithm=community");
    await input.press("Enter");
    await expect(page.getByTestId("cli-canvas-panel")).toBeVisible();
    await expect(page.getByTestId("cli-canvas-panel")).toContainText(
      "illuminate cli:cmty:a1 2 5 algorithm=community",
    );

    // Wait for the canvas bridge, then the post-commit graphology reconcile
    // (the useEffect that adds/drops nodes runs after the label commit above).
    await page.waitForFunction(() => {
      const win = window as Window & {
        __illuminateCanvas?: { getNodePosition: (k: string) => unknown };
      };
      return !!win.__illuminateCanvas?.getNodePosition;
    });
    const present = (key: string): Promise<boolean> =>
      page.evaluate((k) => {
        const win = window as Window & {
          __illuminateCanvas?: { getNodePosition: (key: string) => unknown };
        };
        return win.__illuminateCanvas?.getNodePosition(k) != null;
      }, key);

    // The seed's tight triangle IS the community. Poll a2 to let the
    // reconcile land, then assert the rest of the triangle is present.
    await expect.poll(() => present("cli:cmty:a2")).toBe(true);
    expect(await present("cli:cmty:a1")).toBe(true);
    expect(await present("cli:cmty:a3")).toBe(true);

    // The far triangle across the weak bridge is NOT in the community.
    // `b1` is the load-bearing discriminator: a plain BFS walk (the pre-fix
    // behaviour) would render it at hop 1.
    expect(await present("cli:cmty:b1")).toBe(false);
    expect(await present("cli:cmty:b2")).toBe(false);
    expect(await present("cli:cmty:b3")).toBe(false);
  });

  // #518 — a mutating verb (put/add) folds the new element onto the
  // canvas so the operator sees what they just wrote, instead of the
  // canvas staying blank. The verb dispatches straight through with no
  // confirmation chip (#521). An explicit TTL keeps the expiration inside
  // the server's tombstone window (the default 1 year in the e2e harness); the
  // grammar is `put vertex <key> <value> [ttl_seconds]`.
  test("put vertex adds the new node to the canvas (#518)", async ({
    page,
  }) => {
    await page.goto("/cli");
    // Canvas starts hidden — no graph rendered yet.
    await expect(page.getByTestId("cli-canvas-panel")).toHaveCount(0);
    const input = page.getByTestId("cli-input");
    await input.fill("put vertex cli:gamma fresh 3600");
    await input.press("Enter");
    // Destructive verb dispatches straight through — no confirm chip (#521).
    await expect(page.getByTestId("cli-entry-ok").last()).toBeVisible();
    // The canvas opens with the put as its source label and the node
    // now lives on it.
    await expect(page.getByTestId("cli-canvas-panel")).toBeVisible();
    await expect(page.getByTestId("cli-canvas-panel")).toContainText(
      "put vertex cli:gamma fresh 3600",
    );
  });

  // #433 — Ctrl+L is the editor-conventional clear-screen binding and
  // the only way to clear the scrollback now that the Clear button has
  // been removed (#512). It empties the scrollback in place, leaving the
  // banner, while gateway override and history survive.
  test("Ctrl+L clears the scrollback but keeps history", async ({ page }) => {
    await page.goto("/cli");
    const input = page.getByTestId("cli-input");
    // Initial state: only the banner entry.
    await expect(page.getByTestId("cli-entry-info")).toHaveCount(1);
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
    // Ctrl+L empties the scrollback; only the banner survives.
    await input.focus();
    await input.press("Control+l");
    await expect(page.getByTestId("cli-entry-ok")).toHaveCount(0);
    await expect(page.getByTestId("cli-entry-error")).toHaveCount(0);
    await expect(page.getByTestId("cli-entry-info")).toHaveCount(1);
    // History survives clear: arrow-up restores the last command.
    await input.focus();
    await input.press("ArrowUp");
    await expect(input).toHaveValue("nonsense");
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
    // The prompt stays editable while busy (#945) — only the Cancel
    // button appears to mark the in-flight dispatch.
    await expect(input).toBeEnabled();
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
    // The prompt stays enabled and focused while busy (#945), so Esc
    // reaches the window-level onKeyDown handler either way.
    await input.press("Escape");
    await expect(page.getByTestId("cli-entry-info").last()).toContainText(
      "aborted",
    );
    await expect(input).toBeEnabled();
  });

  // #465 / #512 — splitter L/R layout. The splitter is hidden while no
  // graph has been rendered yet; once a graph-producing command lands,
  // the page switches to a two-column grid with the shell terminal on
  // the left, the axis picker + canvas on the right, and a draggable
  // splitter between them.
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
    // the default 0.4 ratio (terminal share) without any explicit
    // cleanup.
    await page.goto("/cli");
    const input = page.getByTestId("cli-input");
    await input.fill("get vertex cli:alpha");
    await input.press("Enter");
    const splitter = page.getByTestId("cli-splitter");
    await expect(splitter).toBeVisible();
    // Initial aria-valuenow reflects the 40% terminal default.
    await expect(splitter).toHaveAttribute("aria-valuenow", "40");
    // `aria-valuenow` is the LEFT (terminal) column's share, so dragging
    // the handle left SHRINKS the terminal. Drag ~150px left and verify
    // the ratio dropped. 150px against the ~1100px-wide root keeps both
    // panes inside the 360px min-pane clamp.
    const box = await splitter.boundingBox();
    if (!box) throw new Error("splitter has no bounding box");
    const startX = box.x + box.width / 2;
    const startY = box.y + box.height / 2;
    await page.mouse.move(startX, startY);
    await page.mouse.down();
    await page.mouse.move(startX - 150, startY, { steps: 12 });
    await page.mouse.up();
    // aria-valuenow should now be noticeably below 40 and still above
    // the min-pane clamp.
    const afterDrag = Number(await splitter.getAttribute("aria-valuenow"));
    expect(afterDrag).toBeLessThan(40);
    expect(afterDrag).toBeGreaterThan(15);
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
    await expect(splitter).toHaveAttribute("aria-valuenow", "40");
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
    // The picker lives inside the canvas panel, so render a graph first
    // to mount it (#512).
    const input = page.getByTestId("cli-input");
    await input.fill("get vertex cli:alpha");
    await input.press("Enter");
    await expect(page.getByTestId("cli-canvas-panel")).toBeVisible();
    const picker = page.getByTestId("cli-axis-picker");
    await expect(picker).toBeVisible();
    // Defaults: step=2, k=5, algorithm=none, objective=max, weighting=raw
    // → short form `illuminate <key> 2 5`.
    await expect(page.getByTestId("cli-axis-preview")).toHaveText(
      "illuminate <key> 2 5",
    );
    await expect(page.getByTestId("cli-axis-step")).toHaveValue("2");
    await expect(page.getByTestId("cli-axis-k")).toHaveValue("5");
    await expect(page.getByTestId("cli-axis-weighting")).toBeVisible();
    // Canvas-header hint mirrors the picker.
    await expect(page.getByTestId("cli-click-hint")).toHaveText(
      "illuminate <key> 2 5",
    );
  });

  test("tuning axes updates the picker preview to the long form (#464)", async ({
    page,
  }) => {
    await page.goto("/cli");
    // Render a graph first so the canvas panel — and the picker inside
    // it — mounts (#512).
    const input = page.getByTestId("cli-input");
    await input.fill("get vertex cli:alpha");
    await input.press("Enter");
    await expect(page.getByTestId("cli-canvas-panel")).toBeVisible();
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

    // Pick objective=min (max is the default, so it would be omitted).
    await page.getByTestId("cli-axis-objective").click();
    await page.getByRole("option", { name: /Minimize/ }).click();
    await expect(preview).toHaveText(
      "illuminate <key> 3 10 algorithm=spt objective=min",
    );

    // Pick weighting=bm25 via the Dropdown. Token order must be
    // algorithm → objective → weighting.
    await page.getByTestId("cli-axis-weighting").click();
    await page.getByRole("option", { name: "BM25" }).click();
    await expect(preview).toHaveText(
      "illuminate <key> 3 10 algorithm=spt objective=min weighting=bm25",
    );

    // The header hint tracks the picker.
    await expect(page.getByTestId("cli-click-hint")).toHaveText(
      "illuminate <key> 3 10 algorithm=spt objective=min weighting=bm25",
    );
  });

  // #801 — Personalized PageRank knobs (restart_prob / epsilon) only
  // surface when algorithm=ppr is selected, and feed the same single
  // source of truth (`formatIlluminateClick`) as every other axis.
  test("ppr knobs appear only for algorithm=ppr and tune the preview (#801)", async ({
    page,
  }) => {
    await page.goto("/cli");
    // Render a graph first so the canvas panel — and the picker inside
    // it — mounts (#512).
    const input = page.getByTestId("cli-input");
    await input.fill("get vertex cli:alpha");
    await input.press("Enter");
    await expect(page.getByTestId("cli-canvas-panel")).toBeVisible();
    const preview = page.getByTestId("cli-axis-preview");
    await expect(preview).toHaveText("illuminate <key> 2 5");

    // The knobs are hidden until ppr is the active algorithm.
    await expect(page.getByTestId("cli-axis-restart-prob")).toHaveCount(0);
    await expect(page.getByTestId("cli-axis-epsilon")).toHaveCount(0);

    // Select algorithm=ppr — the two knob inputs appear and the preview
    // gains the algorithm token (knobs default to 0 = server default, so
    // they are omitted from the click string until set).
    await page.getByTestId("cli-axis-algorithm").click();
    await page.getByRole("option", { name: "Personalized PageRank" }).click();
    await expect(preview).toHaveText("illuminate <key> 2 5 algorithm=ppr");
    const restartProb = page.getByTestId("cli-axis-restart-prob");
    const epsilon = page.getByTestId("cli-axis-epsilon");
    await expect(restartProb).toBeVisible();
    await expect(epsilon).toBeVisible();

    // Tuning a knob appends it after the algorithm token, in fixed order
    // restart_prob → epsilon.
    await restartProb.fill("0.25");
    await expect(preview).toHaveText(
      "illuminate <key> 2 5 algorithm=ppr restart_prob=0.25",
    );
    await epsilon.fill("0.001");
    await expect(preview).toHaveText(
      "illuminate <key> 2 5 algorithm=ppr restart_prob=0.25 epsilon=0.001",
    );

    // Switching away from ppr hides the knobs again and drops them from
    // the click string (the stored values are simply not echoed).
    await page.getByTestId("cli-axis-algorithm").click();
    await page.getByRole("option", { name: "None (raw subgraph)" }).click();
    await expect(page.getByTestId("cli-axis-restart-prob")).toHaveCount(0);
    await expect(page.getByTestId("cli-axis-epsilon")).toHaveCount(0);
    await expect(preview).toHaveText("illuminate <key> 2 5");
  });

  test("picker state persists across a reload (#464)", async ({ page }) => {
    await page.goto("/cli");
    // Render a graph so the picker mounts (#512).
    const input = page.getByTestId("cli-input");
    await input.fill("get vertex cli:alpha");
    await input.press("Enter");
    await expect(page.getByTestId("cli-canvas-panel")).toBeVisible();
    await page.getByTestId("cli-axis-step").fill("4");
    await page.getByTestId("cli-axis-k").fill("12");
    await page.getByTestId("cli-axis-algorithm").click();
    await page.getByRole("option", { name: "Spanning tree" }).click();
    await expect(page.getByTestId("cli-axis-preview")).toHaveText(
      "illuminate <key> 4 12 algorithm=mst",
    );
    // Reload — picker should hydrate from localStorage on mount. Re-run
    // a graph command so the canvas panel (and picker) mount again.
    await page.reload();
    await page.getByTestId("cli-input").fill("get vertex cli:alpha");
    await page.getByTestId("cli-input").press("Enter");
    await expect(page.getByTestId("cli-canvas-panel")).toBeVisible();
    await expect(page.getByTestId("cli-axis-step")).toHaveValue("4");
    await expect(page.getByTestId("cli-axis-k")).toHaveValue("12");
    await expect(page.getByTestId("cli-axis-preview")).toHaveText(
      "illuminate <key> 4 12 algorithm=mst",
    );
  });

  // #519 — Tab completion must keep the caret in the prompt. Fluent's
  // focus manager (Tabster) moves focus to an invisible boundary
  // sentinel on Tab from a window/document capture-phase handler, so the
  // component restores focus + caret on the next frame. Without the fix
  // the operator is ejected from the input after every completion and has
  // to click back in to keep typing.
  test("Tab completion keeps focus in the prompt (#519)", async ({ page }) => {
    await page.goto("/cli");
    const input = page.getByTestId("cli-input");
    // Single-candidate completion: `illumi` → `illuminate `.
    await input.click();
    await input.fill("illumi");
    await input.press("Tab");
    await expect(input).toHaveValue("illuminate ");
    await expect(input).toBeFocused();
    // Caret sits at the end of the completed token, ready for the key.
    expect(
      await input.evaluate((el: HTMLInputElement) => el.selectionStart),
    ).toBe("illuminate ".length);

    // Ambiguous completion keeps focus while surfacing the hint row.
    await input.fill("get ");
    await input.press("Tab");
    await expect(page.getByTestId("cli-hints")).toBeVisible();
    await expect(input).toBeFocused();
  });

  // #520 / #945 — Enter submits a command and the dispatch goes in flight.
  // The prompt used to be `disabled` while busy, which blurred it to <body>
  // and forced a focus-restore workaround. Since #945 the prompt is never
  // disabled, so it must simply keep focus (and stay editable) for the whole
  // dispatch — no eject-then-restore round trip. Slow-route the RPC so the
  // in-flight window is observable, then assert focus never leaves the prompt.
  test("prompt stays enabled and focused while a command is in flight (#520, #945)", async ({
    page,
  }) => {
    await page.route("**/graph.v1.LanternService/**", async (route) => {
      await new Promise((r) => setTimeout(r, 250));
      await route.continue();
    });
    await page.goto("/cli");
    const input = page.getByTestId("cli-input");
    await input.click();
    await input.fill("get vertex cli:alpha");
    await input.press("Enter");
    // In flight: the Cancel affordance is up, but the prompt is still
    // enabled and still owns focus — no ejection to <body>.
    await expect(page.getByTestId("cli-cancel")).toBeVisible();
    await expect(input).toBeEnabled();
    await expect(input).toBeFocused();
    // After settle the prompt is unchanged — still editable, still focused,
    // ready for the next command without a click.
    await expect(page.getByTestId("cli-entry-ok").last()).toContainText(
      "first",
    );
    await expect(page.getByTestId("cli-cancel")).toHaveCount(0);
    await expect(input).toBeEnabled();
    await expect(input).toBeFocused();
  });

  // #646 — the chrome "Commands" button opens a slide-in reference drawer
  // listing every verb with a signature and a runnable example, so a
  // first-time operator can discover the grammar without already knowing
  // to type `help`. Unlike the intro banner, the toggle never scrolls
  // away, so the reference stays one click away for the whole session.
  test("Commands button opens the CLI reference drawer (#646)", async ({
    page,
  }) => {
    await page.goto("/cli");
    // The toggle is part of the persistent chrome — visible from load.
    const toggle = page.getByTestId("cli-help-toggle");
    await expect(toggle).toBeVisible();
    await expect(toggle).toContainText("Commands");
    // The drawer is closed until the toggle is clicked (modal drawer
    // keeps its content out of the DOM while closed).
    await expect(page.getByTestId("cli-command-reference")).toHaveCount(0);
    await toggle.click();
    // It opens with the grouped reference, including the illuminate verb
    // and at least one runnable example row.
    const drawer = page.getByTestId("cli-command-reference");
    await expect(drawer).toBeVisible();
    await expect(drawer).toContainText("illuminate");
    await expect(page.getByTestId("cli-command-row").first()).toBeVisible();
    // The dismiss button closes it again.
    await page.getByTestId("cli-command-reference-close").click();
    await expect(page.getByTestId("cli-command-reference")).toHaveCount(0);
  });

  // #945 — pasting a multi-line script into the prompt runs each line in
  // order through the pending-command queue instead of flattening the
  // newlines into one uneditable line. Synthesize a real paste event (with
  // multi-line clipboard data) so React's onPaste fires the way a Cmd/Ctrl+V
  // would. Every line lands as an ok chip in submission order, and the canvas
  // reflects the last graph-carrying command.
  test("pasting a multi-line script runs each line in order (#945)", async ({
    page,
  }) => {
    await page.goto("/cli");
    const input = page.getByTestId("cli-input");
    await input.click();
    const script = [
      "get vertex cli:alpha",
      "get vertex cli:beta",
      "illuminate cli:alpha 2 5",
    ].join("\n");
    // Dispatch a paste event carrying the script as text/plain. `bubbles`
    // lets it reach React's root-level paste listener; the DataTransfer is
    // what `e.clipboardData.getData("text")` reads in the handler.
    await input.evaluate((el, text) => {
      const dt = new DataTransfer();
      dt.setData("text/plain", text);
      el.dispatchEvent(
        new ClipboardEvent("paste", {
          clipboardData: dt,
          bubbles: true,
          cancelable: true,
        }),
      );
    }, script);
    // All three lines drain in order — three ok chips, none dropped.
    const ok = page.getByTestId("cli-entry-ok");
    await expect(ok).toHaveCount(3);
    await expect(ok.nth(0)).toContainText("get vertex cli:alpha");
    await expect(ok.nth(0)).toContainText("first");
    await expect(ok.nth(1)).toContainText("get vertex cli:beta");
    await expect(ok.nth(1)).toContainText("second");
    await expect(ok.nth(2)).toContainText("illuminate cli:alpha 2 5");
    // The canvas reflects the last graph-carrying command in the script.
    await expect(page.getByTestId("cli-canvas-panel")).toContainText(
      "illuminate cli:alpha 2 5",
    );
    // The paste never leaked into the editable prompt (preventDefault held).
    await expect(input).toHaveValue("");
  });

  // #945 — the core regression: keystrokes typed while a command is in
  // flight must not be dropped. The prompt used to be `disabled` while busy,
  // so the browser silently discarded keys. Slow-route the RPC, submit a
  // command, then type the next one character-by-character while it's still
  // running and assert every character survived once the dispatch settles.
  test("keystrokes typed while a command is in flight are not dropped (#945)", async ({
    page,
  }) => {
    await page.route("**/graph.v1.LanternService/**", async (route) => {
      await new Promise((r) => setTimeout(r, 1200));
      await route.continue();
    });
    await page.goto("/cli");
    const input = page.getByTestId("cli-input");
    await input.click();
    await input.fill("get vertex cli:alpha");
    await input.press("Enter");
    // In flight — Enter cleared the prompt but it stays editable (#945).
    await expect(page.getByTestId("cli-cancel")).toBeVisible();
    await expect(input).toHaveValue("");
    // Type the next command while the first is still running. Real
    // per-key events, the exact path that used to lose characters.
    await input.pressSequentially("get vertex cli:beta");
    // The characters buffer live in the prompt, none lost.
    await expect(input).toHaveValue("get vertex cli:beta");
    // After the first command settles the typed text is still intact and
    // was NOT auto-submitted (typing never enqueues — only Enter does).
    await expect(page.getByTestId("cli-entry-ok").last()).toContainText(
      "first",
    );
    await expect(page.getByTestId("cli-cancel")).toHaveCount(0);
    await expect(input).toHaveValue("get vertex cli:beta");
  });
});
