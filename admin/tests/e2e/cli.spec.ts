import { expect, test, type Page } from "@playwright/test";

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
    { key: "cli:quoted key", string: "quoted seed" },
    { key: "cli:quoted target", string: "reachable only from quoted seed" },
  ]);
  // Edge so bfs / get edge happy-paths in the canvas spec
  // below have something to render.
  await putEdges([{ tail: "cli:alpha", head: "cli:beta", weight: 2 }]);
  await putEdges([
    { tail: "cli:alpha", head: "cli:quoted key", weight: 1 },
    { tail: "cli:quoted key", head: "cli:quoted target", weight: 1 },
  ]);

  // #942 — a barbell so `community <seed>` has a
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

// #989 — below the desktop split breakpoint, terminal + graph become a page
// scrollable stack. The canvas still needs room for its graph, hop legend, and
// label controls at both a phone portrait and a short landscape viewport.
test.describe("narrow CLI explorer remains operable (#989)", () => {
  test.describe("portrait 390×844", () => {
    test.use({ viewport: { width: 390, height: 844 } });

    test("preserves a practical graph body", async ({ page }) => {
      await assertNarrowExplorer(page);
    });
  });

  test.describe("short landscape 844×390", () => {
    test.use({ viewport: { width: 844, height: 390 } });

    test("preserves a practical graph body", async ({ page }) => {
      await assertNarrowExplorer(page);
    });
  });
});

async function assertNarrowExplorer(page: Page): Promise<void> {
  await page.addInitScript(
    ({ key, value }) => window.localStorage.setItem(key, value),
    { key: STORAGE_KEY, value: CONNECT_URL },
  );
  await page.goto("/cli");
  const input = page.getByTestId("cli-input");
  await input.fill("bfs cli:alpha 2 5");
  await input.press("Enter");

  const canvasBody = page.locator('[class*="canvasBody"]');
  await expect(canvasBody).toBeVisible();
  const bodyBox = await canvasBody.boundingBox();
  if (!bodyBox) throw new Error("canvas body has no bounding box");
  expect(bodyBox.height).toBeGreaterThanOrEqual(360);

  const legend = page.getByTestId("illuminate-legend");
  const labelControls = page.getByTestId("illuminate-label-controls");
  await expect(legend).toBeVisible();
  await expect(labelControls).toBeVisible();
  const legendBox = await legend.boundingBox();
  const controlsBox = await labelControls.boundingBox();
  if (!legendBox || !controlsBox) {
    throw new Error("canvas overlays have no bounding boxes");
  }
  const overlaps =
    legendBox.x < controlsBox.x + controlsBox.width &&
    legendBox.x + legendBox.width > controlsBox.x &&
    legendBox.y < controlsBox.y + controlsBox.height &&
    legendBox.y + legendBox.height > controlsBox.y;
  expect(overlaps).toBe(false);

  // The labels remain keyboard-operable after the stacked layout has pushed
  // the canvas below the fold. Focus scrolls the control into view; Space
  // exercises the same native button path that touch/click uses.
  const nodeLabels = page.getByTestId("illuminate-toggle-node-labels");
  await nodeLabels.focus();
  await expect(nodeLabels).toBeFocused();
  await expect(nodeLabels).toHaveAttribute("aria-pressed", "true");
  await nodeLabels.press("Space");
  await expect(nodeLabels).toHaveAttribute("aria-pressed", "false");

  const layout = await page.evaluate(() => ({
    pageScrolls: document.documentElement.scrollHeight > window.innerHeight,
    noHorizontalOverflow:
      document.documentElement.scrollWidth <= window.innerWidth + 1,
  }));
  expect(layout.pageScrolls).toBe(true);
  expect(layout.noHorizontalOverflow).toBe(true);
}

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

  test("bfs persists across non-graph commands", async ({ page }) => {
    await page.goto("/cli");
    const input = page.getByTestId("cli-input");
    // Render a graph first.
    await input.fill("bfs cli:alpha 2 5");
    await input.press("Enter");
    await expect(page.getByTestId("cli-canvas-panel")).toBeVisible();
    await expect(page.getByTestId("cli-canvas-panel")).toContainText(
      "bfs cli:alpha 2 5",
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
      "bfs cli:alpha 2 5",
    );
  });

  test("canvas node click quotes an arbitrary key before reaching the RPC (#988)", async ({
    page,
  }) => {
    await page.goto("/cli");
    const input = page.getByTestId("cli-input");
    // One hop exposes the space-containing node but not its unique target.
    await input.fill("bfs cli:alpha 1 5");
    await input.press("Enter");
    const panel = page.getByTestId("cli-canvas-panel");
    await expect(panel).toBeVisible();

    const clicked = await page.evaluate(() => {
      const win = window as Window & {
        __illuminateCanvas?: { clickNode: (key: string) => boolean };
      };
      return win.__illuminateCanvas?.clickNode("cli:quoted key") ?? false;
    });
    expect(clicked).toBe(true);

    // The source records the exact command submitted by the canvas callback;
    // the depth-two-only target proves the RPC received `cli:quoted key`, not
    // the two tokens that an unquoted space would have produced.
    await expect(panel).toContainText('bfs "cli:quoted key" 2 5');
    await expect
      .poll(() =>
        page.evaluate(() => {
          const win = window as Window & {
            __illuminateCanvas?: {
              getNodePosition: (key: string) => unknown;
            };
          };
          return (
            win.__illuminateCanvas?.getNodePosition("cli:quoted target") != null
          );
        }),
      )
      .toBe(true);
  });

  // #942 — the `community` verb must reach the LocalCommunity (#845)
  // family, not silently fall back to a BFS walk. Regression guard: before
  // the fix, dispatcher.ts's `ALGORITHM_TO_API` lacked a `community` entry
  // (typed `Record<string, …>`, so the miss was invisible) and the wire
  // adapter built `opts.bfs` instead of `opts.community`. On the barbell
  // seeded above, that BFS walk from a1 crosses the weak bridge and pulls in
  // b1 at hop 1; the real community extraction cuts the bridge and returns
  // exactly the seed's triangle {a1,a2,a3}. We assert on the rendered canvas
  // membership via the test bridge (present/absent), which the CLI reconcile
  // overwrites per frame (graph-view.ts), so stale nodes cannot leak in.
  test("community extracts the seed cluster, not a BFS frontier (#942)", async ({
    page,
  }) => {
    await page.goto("/cli");
    const input = page.getByTestId("cli-input");
    await input.fill("community cli:cmty:a1 5");
    await input.press("Enter");
    await expect(page.getByTestId("cli-canvas-panel")).toBeVisible();
    await expect(page.getByTestId("cli-canvas-panel")).toContainText(
      "community cli:cmty:a1 5",
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

  // #953 — `add decaying-edge` runs the geometric decay staircase entirely
  // through the workspace-linked lantern-sdk (one staggered-TTL AddEdges
  // batch). The echo surfaces the SDK's returned live-sum total, and the
  // additive edge lands on the canvas at its initial weight.
  test("add decaying-edge writes an additive edge and echoes its total (#953)", async ({
    page,
  }) => {
    await page.goto("/cli");
    await expect(page.getByTestId("cli-canvas-panel")).toHaveCount(0);
    const input = page.getByTestId("cli-input");
    await input.fill("add decaying-edge cli:dk-a cli:dk-b 16 0.5 5 1");
    await input.press("Enter");
    // The write dispatches straight through and echoes an ok chip carrying
    // the decay params and the returned live-sum total.
    const ok = page.getByTestId("cli-entry-ok").last();
    await expect(ok).toBeVisible();
    await expect(ok).toContainText("total");
    await expect(ok).toContainText("16");
    // The canvas opens with the command as its source label and the edge
    // between the two endpoints.
    await expect(page.getByTestId("cli-canvas-panel")).toBeVisible();
    await expect(page.getByTestId("cli-canvas-panel")).toContainText(
      "add decaying-edge cli:dk-a cli:dk-b 16 0.5 5 1",
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
  //      truth via `formatFamilyClick`).
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
    // Defaults: step=2, k=5, algorithm=bfs, reduction=none, objective=max,
    // weighting=raw → short form `bfs <key> 2 5`.
    await expect(page.getByTestId("cli-axis-preview")).toHaveText(
      "bfs <key> 2 5",
    );
    await expect(page.getByTestId("cli-axis-step")).toHaveValue("2");
    await expect(page.getByTestId("cli-axis-k")).toHaveValue("5");
    await expect(page.getByTestId("cli-axis-weighting")).toBeVisible();
    // Canvas-header hint mirrors the picker.
    await expect(page.getByTestId("cli-click-hint")).toHaveText(
      "bfs <key> 2 5",
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
    await expect(preview).toHaveText("bfs <key> 2 5");

    // Bump step and k to long-form values.
    const step = page.getByTestId("cli-axis-step");
    await step.fill("3");
    await expect(preview).toHaveText("bfs <key> 3 5");
    const k = page.getByTestId("cli-axis-k");
    await k.fill("10");
    await expect(preview).toHaveText("bfs <key> 3 10");

    // Pick the Local Community family via the algorithm Dropdown. community
    // takes no step — the single positional after the seed is max_size, so
    // the preview drops the step and echoes `community <key> <max_size>`.
    await page.getByTestId("cli-axis-algorithm").click();
    await page.getByRole("option", { name: "Local community" }).click();
    await expect(preview).toHaveText("community <key> 10");

    // Pick reduction=spt via the reduction Dropdown (#961). The reduction
    // axis is orthogonal to the family and slots in right after the
    // max_size positional.
    await page.getByTestId("cli-axis-reduction").click();
    await page.getByRole("option", { name: "Shortest-path tree" }).click();
    await expect(preview).toHaveText("community <key> 10 reduction=spt");

    // Pick objective=min (max is the default, so it would be omitted). For a
    // reduction this steers the tree direction (#961).
    await page.getByTestId("cli-axis-objective").click();
    await page.getByRole("option", { name: /Minimize/ }).click();
    await expect(preview).toHaveText(
      "community <key> 10 reduction=spt objective=min",
    );

    // Pick weighting=bm25 via the Dropdown. Token order must be
    // reduction → objective → weighting.
    await page.getByTestId("cli-axis-weighting").click();
    await page.getByRole("option", { name: "BM25" }).click();
    await expect(preview).toHaveText(
      "community <key> 10 reduction=spt objective=min weighting=bm25",
    );

    // The header hint tracks the picker.
    await expect(page.getByTestId("cli-click-hint")).toHaveText(
      "community <key> 10 reduction=spt objective=min weighting=bm25",
    );
  });

  // #801 — Personalized PageRank knobs (restart_prob / epsilon) only
  // surface when the pagerank family is selected, and feed the same single
  // source of truth (`formatFamilyClick`) as every other axis.
  test("pagerank knobs appear only for the pagerank family and tune the preview (#801)", async ({
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
    await expect(preview).toHaveText("bfs <key> 2 5");

    // The knobs are hidden until the pagerank family is active.
    await expect(page.getByTestId("cli-axis-restart-prob")).toHaveCount(0);
    await expect(page.getByTestId("cli-axis-epsilon")).toHaveCount(0);
    // The reduction axis is shown for the tree-producing families (#961);
    // bfs is the default so it is visible here.
    await expect(page.getByTestId("cli-axis-reduction")).toBeVisible();

    // Select the pagerank family — the two knob inputs appear. pagerank takes
    // no step, so the preview drops it and the single positional becomes
    // top_n; the knobs default to 0 = server default, so they are omitted
    // from the click string until set.
    await page.getByTestId("cli-axis-algorithm").click();
    await page.getByRole("option", { name: "Personalized PageRank" }).click();
    await expect(preview).toHaveText("pagerank <key> 5");
    const restartProb = page.getByTestId("cli-axis-restart-prob");
    const epsilon = page.getByTestId("cli-axis-epsilon");
    await expect(restartProb).toBeVisible();
    await expect(epsilon).toBeVisible();
    // pagerank renders a ranked vertex set, not a tree, so the reduction axis
    // is hidden while it is active (#961).
    await expect(page.getByTestId("cli-axis-reduction")).toHaveCount(0);

    // Tuning a knob appends it after the positional, in fixed order
    // restart_prob → epsilon.
    await restartProb.fill("0.25");
    await expect(preview).toHaveText("pagerank <key> 5 restart_prob=0.25");
    await epsilon.fill("0.001");
    await expect(preview).toHaveText(
      "pagerank <key> 5 restart_prob=0.25 epsilon=0.001",
    );

    // Switching back to the default bfs family hides the knobs again and
    // drops them from the click string (the stored values are simply not
    // echoed), and the reduction axis reappears.
    await page.getByTestId("cli-axis-algorithm").click();
    await page.getByRole("option", { name: "BFS (per-hop top-k)" }).click();
    await expect(page.getByTestId("cli-axis-restart-prob")).toHaveCount(0);
    await expect(page.getByTestId("cli-axis-epsilon")).toHaveCount(0);
    await expect(page.getByTestId("cli-axis-reduction")).toBeVisible();
    await expect(preview).toHaveText("bfs <key> 2 5");
  });

  // #964 — the α/ε knobs must accept floats typed a keystroke at a time, not
  // just Playwright's atomic .fill(). The pre-fix inputs were `type="number"`
  // whose `.value` goes empty for an in-progress "0." / "1e-", and the field
  // was re-derived from the numeric axis (0 → blank), so a human typing "0.15"
  // saw every keystroke erased and could only enter integers.
  test("pagerank knobs accept floats typed character-by-character (#964)", async ({
    page,
  }) => {
    await page.goto("/cli");
    const input = page.getByTestId("cli-input");
    await input.fill("get vertex cli:alpha");
    await input.press("Enter");
    await expect(page.getByTestId("cli-canvas-panel")).toBeVisible();
    const preview = page.getByTestId("cli-axis-preview");

    await page.getByTestId("cli-axis-algorithm").click();
    await page.getByRole("option", { name: "Personalized PageRank" }).click();
    await expect(preview).toHaveText("pagerank <key> 5");

    // Type a leading-zero decimal one key at a time: the field must retain each
    // keystroke (no blanking on the intermediate "0" / "0.") and the preview
    // must build up the full float.
    const restartProb = page.getByTestId("cli-axis-restart-prob");
    await restartProb.click();
    await restartProb.pressSequentially("0.15");
    await expect(restartProb).toHaveValue("0.15");
    await expect(preview).toHaveText("pagerank <key> 5 restart_prob=0.15");

    // Scientific notation must survive too — a `type="number"` field reports an
    // empty value for the intermediate "1e" / "1e-". The raw text is echoed
    // verbatim while the committed number normalises to decimal in the command.
    const epsilon = page.getByTestId("cli-axis-epsilon");
    await epsilon.click();
    await epsilon.pressSequentially("1e-4");
    await expect(epsilon).toHaveValue("1e-4");
    await expect(preview).toHaveText(
      "pagerank <key> 5 restart_prob=0.15 epsilon=0.0001",
    );
  });

  // #987 — raw α/ε drafts must obey the same strict float32 domains as the
  // parser before the picker can generate a click command. The test covers
  // persisted corruption, complete-but-invalid values, suffix garbage,
  // float32 rounding/underflow, blank defaults, and scientific notation.
  test("push-knob validation blocks invalid click commands (#987)", async ({
    page,
  }) => {
    await page.addInitScript(() => {
      localStorage.setItem("cli.click.restart_prob", "1.5");
      localStorage.setItem("cli.click.epsilon", "0.25suffix");
    });
    await page.goto("/cli");
    const input = page.getByTestId("cli-input");
    await input.fill("get vertex cli:alpha");
    await input.press("Enter");
    await expect(page.getByTestId("cli-canvas-panel")).toBeVisible();

    await page.getByTestId("cli-axis-algorithm").click();
    await page.getByRole("option", { name: "Personalized PageRank" }).click();
    const restartProb = page.getByTestId("cli-axis-restart-prob");
    const epsilon = page.getByTestId("cli-axis-epsilon");
    const preview = page.getByTestId("cli-axis-preview");
    const hint = page.getByTestId("cli-click-hint");

    // Invalid persisted values hydrate to blank/server-default drafts.
    await expect(restartProb).toHaveValue("");
    await expect(epsilon).toHaveValue("");
    await expect(preview).toHaveText("pagerank <key> 5");

    for (const raw of [
      "0",
      "1",
      "1.5",
      "-0.1",
      "0.25suffix",
      "0.99999999", // float32 rounds to 1
      "1e-50", // float32 underflows to 0
    ]) {
      await restartProb.fill(raw);
      await expect(restartProb).toHaveValue(raw);
      await expect(
        page.getByTestId("cli-axis-restart-prob-field"),
      ).toContainText("restart_prob must be a float32 in (0, 1)");
      // Neither preview nor canvas hint carries an executable command while
      // the parent click handler is gated on this invalid raw draft.
      await expect(preview).toHaveText(
        "Fix push-knob validation errors before clicking a node.",
      );
      await expect(hint).toHaveText(
        "Fix push-knob validation errors before clicking a node.",
      );
      await expect(page.getByTestId("cli-axis-command-blocked")).toBeVisible();
    }

    await restartProb.fill("");
    await expect(preview).toHaveText("pagerank <key> 5");

    for (const raw of ["0", "-0.1", "0.25suffix", "1e-50"]) {
      await epsilon.fill(raw);
      await expect(epsilon).toHaveValue(raw);
      await expect(page.getByTestId("cli-axis-epsilon-field")).toContainText(
        "epsilon must be a positive float32",
      );
      await expect(preview).toHaveText(
        "Fix push-knob validation errors before clicking a node.",
      );
    }

    // Blank is the only default spelling. A complete valid scientific value
    // then commits and produces the same parser-accepted command text.
    await epsilon.fill("");
    await expect(preview).toHaveText("pagerank <key> 5");
    await epsilon.fill("1e-4");
    await expect(epsilon).toHaveValue("1e-4");
    await expect(preview).toHaveText("pagerank <key> 5 epsilon=0.0001");
    await expect(hint).toHaveText("pagerank <key> 5 epsilon=0.0001");
    await expect(page.getByTestId("cli-axis-command-blocked")).toHaveCount(0);
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
    await page.getByTestId("cli-axis-reduction").click();
    await page.getByRole("option", { name: "Spanning tree" }).click();
    await expect(page.getByTestId("cli-axis-preview")).toHaveText(
      "bfs <key> 4 12 reduction=mst",
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
      "bfs <key> 4 12 reduction=mst",
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
    // Single-candidate completion: `commu` → `community `.
    await input.click();
    await input.fill("commu");
    await input.press("Tab");
    await expect(input).toHaveValue("community ");
    await expect(input).toBeFocused();
    // Caret sits at the end of the completed token, ready for the key.
    expect(
      await input.evaluate((el: HTMLInputElement) => el.selectionStart),
    ).toBe("community ".length);

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
    // It opens with the grouped reference, including the bfs verb
    // and at least one runnable example row.
    const drawer = page.getByTestId("cli-command-reference");
    await expect(drawer).toBeVisible();
    await expect(drawer).toContainText("bfs");
    await expect(drawer).toContainText("add decaying-edge");
    await expect(page.getByTestId("cli-command-row").first()).toBeVisible();
    // The dismiss button closes it again.
    await page.getByTestId("cli-command-reference-close").click();
    await expect(page.getByTestId("cli-command-reference")).toHaveCount(0);
  });

  test("scoped family help renders only the selected reference (#995)", async ({
    page,
  }) => {
    await page.goto("/cli");
    const input = page.getByTestId("cli-input");
    await input.fill("help bfs");
    await input.press("Enter");
    const entry = page.getByTestId("cli-entry-info").last();
    await expect(entry).toContainText("Signature");
    await expect(entry).toContainText("bfs <seed>");
    await expect(entry).toContainText("Defaults");
    await expect(entry).toContainText("Domains");
    await expect(entry).toContainText("Examples");
    await expect(entry).not.toContainText("Lantern CLI grammar:");
    await expect(entry).not.toContainText("pagerank <seed>");
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
      "bfs cli:alpha 2 5",
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
    await expect(ok.nth(2)).toContainText("bfs cli:alpha 2 5");
    // The canvas reflects the last graph-carrying command in the script.
    await expect(page.getByTestId("cli-canvas-panel")).toContainText(
      "bfs cli:alpha 2 5",
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
