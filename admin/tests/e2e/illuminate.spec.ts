import { expect, test } from "@playwright/test";

import { CONNECT_URL, STORAGE_KEY, putEdges, putVertices } from "./helpers";

/**
 * Seeds a small chain so the additive expansion model has multiple
 * neighbourhoods to walk through:
 *
 *    hub --(1)--> left --(1)--> leftleft --(1)--> leftleftleft
 *    hub --(3)--> right --(2)--> rightright
 *
 * The default IlluminateControls step is 2 (see
 * `DEFAULT_ILLUMINATE_CONTROLS` in `app/lib/client/usecase/illuminate/state.ts`),
 * so the first Illuminate from `hub` brings in the full 2-hop
 * frontier — {hub, left, right, leftleft, rightright} + 4 edges.
 * Clicking Expand on `leftleft` then brings in `leftleftleft` (a fresh
 * vertex outside the initial frame) without removing the previous
 * nodes (#466 additive invariant).
 */
test.beforeAll(async () => {
  await putVertices([
    { key: "e2e:illum:hub", string: "hub" },
    { key: "e2e:illum:left", int32: 1 },
    { key: "e2e:illum:right", int32: 2 },
    { key: "e2e:illum:leftleft", string: "leftleft" },
    { key: "e2e:illum:rightright", string: "rightright" },
    { key: "e2e:illum:leftleftleft", string: "leftleftleft" },
  ]);
  await putEdges([
    { tail: "e2e:illum:hub", head: "e2e:illum:left", weight: 1 },
    { tail: "e2e:illum:hub", head: "e2e:illum:right", weight: 3 },
    { tail: "e2e:illum:left", head: "e2e:illum:leftleft", weight: 1 },
    { tail: "e2e:illum:right", head: "e2e:illum:rightright", weight: 2 },
    { tail: "e2e:illum:leftleft", head: "e2e:illum:leftleftleft", weight: 1 },
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

test.describe("/illuminate", () => {
  test("shows the seed prompt when no ?seed= is present", async ({ page }) => {
    await page.goto("/illuminate");
    await expect(page.getByTestId("illuminate-seed-prompt")).toBeVisible();
    await expect(page.getByTestId("illuminate-open")).toBeDisabled();
  });

  test("Seed prompt suggests keys as you type and opens on commit (#457)", async ({
    page,
  }) => {
    await page.goto("/illuminate");
    await expect(page.getByTestId("illuminate-seed-prompt")).toBeVisible();

    // Empty prefix shows the placeholder caption, not a count.
    await expect(page.getByTestId("illuminate-seed-matches")).toContainText(
      /at least/i,
    );

    const input = page.getByTestId("illuminate-seed-input");
    await input.click();
    await input.pressSequentially("e2e:illum:left");

    // CountVerticesByPrefix surfaces a match tally beneath the field.
    await expect(page.getByTestId("illuminate-seed-matches")).toContainText(
      /\d+ match/,
    );

    // ScanVertices feeds the Combobox listbox; the three `left*` keys all
    // share the typed prefix, so the exact `leftleft` option is offered.
    const option = page.getByRole("option", {
      name: "e2e:illum:leftleft",
      exact: true,
    });
    await expect(option).toBeVisible();
    await option.click();

    // Committing a suggestion opens that neighbourhood (no Browse detour).
    await expect(page.getByTestId("illuminate-toolbar")).toBeVisible();
    await expect(page).toHaveURL(/\?seed=e2e%3Aillum%3Aleftleft/);
  });

  test("renders the canvas and neighbour table for a seed", async ({
    page,
  }) => {
    const seed = encodeURIComponent("e2e:illum:hub");
    await page.goto(`/illuminate?seed=${seed}`);

    await expect(page.getByTestId("illuminate-toolbar")).toBeVisible();
    // #456: the seed echo is now the leading lineage chip (chip 0).
    await expect(page.getByTestId("illuminate-chip-0")).toHaveAttribute(
      "data-chip-origin",
      "e2e:illum:hub",
    );
    await expect(page.getByTestId("illuminate-chip-0")).toHaveAttribute(
      "data-chip-is-seed",
      "true",
    );
    await expect(page.getByTestId("illuminate-canvas")).toBeVisible();

    // Refresh should be enabled once the seed is set.
    await expect(page.getByTestId("illuminate-refresh")).toBeEnabled();

    // Counter reflects the live accumulator: the default step is 2, so
    // the initial fetch from `hub` returns the full 2-hop frontier —
    // 5 vertices, 4 edges, 1 expansion.
    const counter = page.getByTestId("illuminate-counter");
    await expect(counter).toContainText("5 vertices");
    await expect(counter).toContainText("4 edges");
    await expect(counter).toContainText("1 expansion");

    // The disclosure summary reflects the live accumulator counts.
    const summary = page.getByRole("group").getByText(/List view \(5 vertices/);
    await expect(summary).toBeVisible();
    await summary.click();

    const table = page.getByTestId("illuminate-table");
    await expect(table).toBeVisible();
    await expect(
      table.getByRole("link", { name: "e2e:illum:hub", exact: true }),
    ).toBeVisible();
    await expect(
      table.getByRole("link", { name: "e2e:illum:left", exact: true }),
    ).toBeVisible();
    await expect(
      table.getByRole("link", { name: "e2e:illum:right", exact: true }),
    ).toBeVisible();
    await expect(
      table.getByRole("link", { name: "e2e:illum:leftleft", exact: true }),
    ).toBeVisible();
    await expect(
      table.getByRole("link", { name: "e2e:illum:rightright", exact: true }),
    ).toBeVisible();
  });

  test("Expand from the list view ADDS new neighbours without removing existing ones", async ({
    page,
  }) => {
    const seed = encodeURIComponent("e2e:illum:hub");
    await page.goto(`/illuminate?seed=${seed}`);
    await expect(page.getByTestId("illuminate-toolbar")).toBeVisible();

    // Wait for the initial fetch to land (full 2-hop frontier from hub).
    const counter = page.getByTestId("illuminate-counter");
    await expect(counter).toContainText("5 vertices");

    // Open the list view to access per-row Expand buttons.
    await page
      .getByRole("group")
      .getByText(/List view \(5 vertices/)
      .click();

    const table = page.getByTestId("illuminate-table");
    // Expand from `leftleft` — it sits on the edge of the initial frame,
    // so its outgoing edge to `leftleftleft` (3 hops from hub) is the
    // first vertex we genuinely bring in via an additive expansion.
    await table
      .getByRole("button", { name: "Expand from e2e:illum:leftleft" })
      .click();

    // The accumulator must GROW from 5 → 6 vertices. The URL stays
    // anchored on the initial seed so deep links remain stable.
    await expect(counter).toContainText("6 vertices");
    await expect(counter).toContainText("2 expansions");
    await expect(page).toHaveURL(/\?seed=e2e%3Aillum%3Ahub/);
    // #456: the seed remains the leading lineage chip across an expansion.
    await expect(page.getByTestId("illuminate-chip-0")).toHaveAttribute(
      "data-chip-origin",
      "e2e:illum:hub",
    );

    // The existing nodes survive across the expansion.
    await expect(
      table.getByRole("link", { name: "e2e:illum:hub", exact: true }),
    ).toBeVisible();
    await expect(
      table.getByRole("link", { name: "e2e:illum:right", exact: true }),
    ).toBeVisible();
    await expect(
      table.getByRole("link", { name: "e2e:illum:leftleft", exact: true }),
    ).toBeVisible();
    // And the newcomer is present too.
    await expect(
      table.getByRole("link", { name: "e2e:illum:leftleftleft", exact: true }),
    ).toBeVisible();
  });

  test("Expand from key (toolbar typeahead) ADDS a neighbourhood in place (#457)", async ({
    page,
  }) => {
    const seed = encodeURIComponent("e2e:illum:hub");
    await page.goto(`/illuminate?seed=${seed}`);
    await expect(page.getByTestId("illuminate-toolbar")).toBeVisible();

    const counter = page.getByTestId("illuminate-counter");
    await expect(counter).toContainText("5 vertices");

    // Open the inline Expand-from-key picker from the toolbar.
    await page.getByTestId("illuminate-expand-toggle").click();
    const input = page.getByTestId("illuminate-expand-input");
    await expect(input).toBeVisible();
    await input.click();
    await input.pressSequentially("e2e:illum:leftleft");

    // Commit the suggestion that bridges to a vertex outside the initial
    // 2-hop frame (`leftleftleft` is 3 hops from hub).
    const option = page.getByRole("option", {
      name: "e2e:illum:leftleft",
      exact: true,
    });
    await expect(option).toBeVisible();
    await option.click();

    // The accumulator grows additively (5 → 6) and the deep-link URL stays
    // anchored on the original seed (the picker dispatches `ill.expand`,
    // not a fresh navigation).
    await expect(counter).toContainText("6 vertices");
    await expect(counter).toContainText("2 expansions");
    await expect(page).toHaveURL(/\?seed=e2e%3Aillum%3Ahub/);
    // The picker collapses once a key is committed.
    await expect(input).toHaveCount(0);
  });

  test("Clear empties the accumulator and returns to the seed prompt", async ({
    page,
  }) => {
    const seed = encodeURIComponent("e2e:illum:hub");
    await page.goto(`/illuminate?seed=${seed}`);
    await expect(page.getByTestId("illuminate-toolbar")).toBeVisible();
    await expect(page.getByTestId("illuminate-counter")).toContainText(
      "5 vertices",
    );

    await page.getByTestId("illuminate-clear").click();

    // The URL drops the seed and the prompt comes back.
    await expect(page).toHaveURL(/\/illuminate$/);
    await expect(page.getByTestId("illuminate-seed-prompt")).toBeVisible();
  });

  test("Re-expanding the same node is idempotent (no crash, accumulator stable)", async ({
    page,
  }) => {
    const seed = encodeURIComponent("e2e:illum:hub");
    await page.goto(`/illuminate?seed=${seed}`);
    await expect(page.getByTestId("illuminate-toolbar")).toBeVisible();

    const counter = page.getByTestId("illuminate-counter");
    await expect(counter).toContainText("5 vertices");

    await page
      .getByRole("group")
      .getByText(/List view \(5 vertices/)
      .click();

    const table = page.getByTestId("illuminate-table");
    // Click Expand on the initial seed itself (#466 D11 — even the seed
    // is meaningful to re-expand because the server graph decays).
    await table
      .getByRole("button", { name: "Expand from e2e:illum:hub" })
      .click();
    // Wait for the expansion to register (counter ticks even if vertices
    // stay the same).
    await expect(counter).toContainText("2 expansion");
    // Accumulator must still hold all five vertices; no error appears.
    await expect(counter).toContainText("5 vertices");
    await expect(page.getByTestId("illuminate-error")).toHaveCount(0);
  });

  test("Canvas labels reach WCAG AA contrast against the canvas background (#453)", async ({
    page,
  }) => {
    const seed = encodeURIComponent("e2e:illum:hub");
    await page.goto(`/illuminate?seed=${seed}`);
    await expect(page.getByTestId("illuminate-toolbar")).toBeVisible();
    await expect(page.getByTestId("illuminate-counter")).toContainText(
      "5 vertices",
    );

    // Read the resolved label colour from FluentProvider's CSS variables
    // and the canvas background, then assert WCAG AA contrast
    // (≥ 4.5:1 for normal text). The canvas itself paints labels via
    // WebGL/Canvas2D so we can't sample pixels reliably; the contract
    // is that the Sigma renderer was passed the same colour we read
    // out of the cascade.
    const contrastReport = await page.evaluate(() => {
      const wrapper = document.querySelector(
        '[data-testid="illuminate-canvas"]',
      );
      if (!wrapper) {
        return { error: "canvas wrapper not found" };
      }
      const cs = getComputedStyle(wrapper);
      const labelColour = (
        cs.getPropertyValue("--colorNeutralForeground1").trim() || "#242424"
      ).toLowerCase();
      const background = cs.backgroundColor;

      const parse = (raw: string): [number, number, number] | null => {
        const hexMatch = raw.match(/^#([0-9a-f]{6})$/i);
        if (hexMatch) {
          const n = parseInt(hexMatch[1], 16);
          return [(n >> 16) & 0xff, (n >> 8) & 0xff, n & 0xff];
        }
        const rgbMatch = raw.match(/^rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)/i);
        if (rgbMatch) {
          return [
            parseInt(rgbMatch[1], 10),
            parseInt(rgbMatch[2], 10),
            parseInt(rgbMatch[3], 10),
          ];
        }
        return null;
      };

      const lum = ([r, g, b]: [number, number, number]) => {
        const chan = (v: number) => {
          const x = v / 255;
          return x <= 0.03928 ? x / 12.92 : Math.pow((x + 0.055) / 1.055, 2.4);
        };
        return 0.2126 * chan(r) + 0.7152 * chan(g) + 0.0722 * chan(b);
      };

      const fg = parse(labelColour);
      const bg = parse(background);
      if (!fg || !bg) {
        return {
          error: "could not parse colours",
          labelColour,
          background,
        };
      }
      const l1 = lum(fg);
      const l2 = lum(bg);
      const lighter = Math.max(l1, l2);
      const darker = Math.min(l1, l2);
      const ratio = (lighter + 0.05) / (darker + 0.05);
      return { ratio, labelColour, background };
    });

    expect(contrastReport.error).toBeUndefined();
    expect(contrastReport.ratio).toBeGreaterThanOrEqual(4.5);
  });

  test("Additive expansion keeps surviving nodes within layout tolerance (#454)", async ({
    page,
  }) => {
    const seed = encodeURIComponent("e2e:illum:hub");
    await page.goto(`/illuminate?seed=${seed}`);
    await expect(page.getByTestId("illuminate-toolbar")).toBeVisible();
    await expect(page.getByTestId("illuminate-counter")).toContainText(
      "5 vertices",
    );

    // Wait for the canvas test bridge to be installed (it is set in the
    // Sigma mount effect, after the renderer is created).
    await page.waitForFunction(() => {
      const win = window as Window & {
        __illuminateCanvas?: {
          getNodePosition: (key: string) => { x: number; y: number } | null;
        };
      };
      return !!win.__illuminateCanvas;
    });

    // Capture positions of two surviving nodes (a 1-hop neighbour of
    // the seed, and a 2-hop "leaf" on the right branch) before the
    // additive expansion fires. Both should hold steady — only the
    // newly-added `leftleftleft` should land at fresh coordinates.
    type Pos = { x: number; y: number };
    const sampleKeys = ["e2e:illum:right", "e2e:illum:rightright"] as const;
    const readPositions = (): Promise<(Pos | null)[]> =>
      page.evaluate(
        (keys) => {
          const win = window as Window & {
            __illuminateCanvas?: {
              getNodePosition: (k: string) => Pos | null;
            };
          };
          const bridge = win.__illuminateCanvas;
          return keys.map((k) => (bridge ? bridge.getNodePosition(k) : null));
        },
        sampleKeys as unknown as string[],
      );

    const before = await readPositions();
    expect(before.every((p) => p !== null)).toBe(true);

    // Trigger an additive expansion from `leftleft` (adds
    // `leftleftleft` — one fresh node, no drops).
    await page
      .getByRole("group")
      .getByText(/List view \(5 vertices/)
      .click();
    const table = page.getByTestId("illuminate-table");
    await table
      .getByRole("button", { name: "Expand from e2e:illum:leftleft" })
      .click();
    await expect(page.getByTestId("illuminate-counter")).toContainText(
      "6 vertices",
    );

    const after = await readPositions();

    // Tolerance: with the additive-relax path running only 5 FA2
    // iterations from the prior equilibrium, surviving nodes must not
    // drift by more than a couple of graphology units. The pre-#454
    // behaviour (80 iterations) would routinely move them tens of
    // units; this guard ensures we don't regress to that.
    const TOLERANCE = 2.0;
    for (let i = 0; i < sampleKeys.length; i++) {
      const a = before[i];
      const b = after[i];
      expect(
        a,
        `expected position for ${sampleKeys[i]} before expansion`,
      ).not.toBeNull();
      expect(
        b,
        `expected position for ${sampleKeys[i]} after expansion`,
      ).not.toBeNull();
      if (!a || !b) continue;
      const dx = Math.abs(b.x - a.x);
      const dy = Math.abs(b.y - a.y);
      expect(
        dx,
        `${sampleKeys[i]} drifted by Δx=${dx.toFixed(2)} (tolerance ${TOLERANCE})`,
      ).toBeLessThanOrEqual(TOLERANCE);
      expect(
        dy,
        `${sampleKeys[i]} drifted by Δy=${dy.toFixed(2)} (tolerance ${TOLERANCE})`,
      ).toBeLessThanOrEqual(TOLERANCE);
    }
  });

  test("Drag-to-pin: drag moves the node, releases pinned, and survives a subsequent expansion (#455)", async ({
    page,
  }) => {
    const seed = encodeURIComponent("e2e:illum:hub");
    await page.goto(`/illuminate?seed=${seed}`);
    await expect(page.getByTestId("illuminate-counter")).toContainText(
      "5 vertices",
    );

    type Pos = { x: number; y: number };
    type Bridge = {
      getNodePosition: (k: string) => Pos | null;
      isNodeFixed: (k: string) => boolean;
      dragStats: () => { downNode: number; moveBody: number; mouseUp: number };
      simulateDrag: (k: string, dx: number, dy: number) => boolean;
    };
    await page.waitForFunction(() => {
      const win = window as Window & { __illuminateCanvas?: Bridge };
      return !!win.__illuminateCanvas;
    });

    const targetKey = "e2e:illum:left";

    const readGraphPos = (k: string): Promise<Pos | null> =>
      page.evaluate((key) => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        return win.__illuminateCanvas?.getNodePosition(key) ?? null;
      }, k);
    const isFixed = (k: string): Promise<boolean> =>
      page.evaluate((key) => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        return win.__illuminateCanvas?.isNodeFixed(key) ?? false;
      }, k);

    // Sanity: before the gesture, the node exists, has a graph position,
    // and is not pinned.
    const before = await readGraphPos(targetKey);
    expect(before).not.toBeNull();
    expect(await isFixed(targetKey)).toBe(false);

    // Use a substantial delta so sigma's `draggedEventsTolerance` does
    // NOT classify the gesture as a click (which would otherwise fire
    // `clickNode` → re-expand).
    const deltaX = 120;
    const deltaY = 90;

    // Drive the drag via the test bridge. Sigma's `downNode` hit-test
    // (`getNodeAtPosition`) reads from a WebGL picking framebuffer
    // that headless chromium populates only intermittently across
    // serial test runs, making real-mouse synthesis unreliable. The
    // bridge invokes the SAME closure-local handlers the real sigma
    // events fire (downNode → mousemovebody → mouseup → finishDrag),
    // so we cover the position-write + pin + dragStats accounting
    // without re-testing sigma's WebGL plumbing.
    const dragResult = await page.evaluate(
      ({ key, dx, dy }) => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        return win.__illuminateCanvas?.simulateDrag(key, dx, dy) ?? false;
      },
      { key: targetKey, dx: deltaX, dy: deltaY },
    );
    expect(dragResult).toBe(true);

    // Diagnostic guard — surfaces a single-line failure if a future
    // refactor breaks the wiring instead of leaving us with a 30-second
    // waitForFunction timeout.
    const stats = await page.evaluate(() => {
      const win = window as Window & { __illuminateCanvas?: Bridge };
      return win.__illuminateCanvas?.dragStats() ?? null;
    });
    expect(stats, "drag bridge counters were not exposed").not.toBeNull();
    expect(
      stats!.downNode,
      "drag bridge never registered a downNode",
    ).toBeGreaterThanOrEqual(1);
    expect(
      stats!.moveBody,
      "drag bridge never registered mousemovebody",
    ).toBeGreaterThan(0);
    expect(
      stats!.mouseUp,
      "drag bridge never registered mouseup",
    ).toBeGreaterThan(0);

    const afterDrag = await readGraphPos(targetKey);
    expect(afterDrag).not.toBeNull();
    expect(await isFixed(targetKey)).toBe(true);
    // The position must have moved by exactly the requested delta —
    // simulateDrag bypasses sigma's viewport↔graph projection so the
    // graph-space delta is what we put in.
    expect(afterDrag!.x - before!.x).toBeCloseTo(deltaX, 6);
    expect(afterDrag!.y - before!.y).toBeCloseTo(deltaY, 6);

    // Sanity: the drag must NOT have been interpreted as a click — if
    // a future refactor accidentally calls expandFrom from finishDrag,
    // the counter would tick up before we ever click the disclosure.
    await expect(page.getByTestId("illuminate-counter")).toContainText(
      "5 vertices",
    );
    await expect(page.getByTestId("illuminate-counter")).toContainText(
      "1 expansion",
    );

    // Trigger an additive expansion. Despite the per-#454 5-iter FA2
    // relax, the pinned `left` node must stay exactly where the user
    // dropped it — graphology FA2 skips position updates for nodes with
    // `fixed: true`.
    await page
      .getByRole("group")
      .getByText(/List view \(5 vertices/)
      .click();
    const table = page.getByTestId("illuminate-table");
    await table
      .getByRole("button", { name: "Expand from e2e:illum:leftleft" })
      .click();
    await expect(page.getByTestId("illuminate-counter")).toContainText(
      "6 vertices",
    );

    const afterExpansion = await readGraphPos(targetKey);
    expect(afterExpansion).not.toBeNull();
    // FA2 with `fixed: true` produces zero displacement — allow only
    // floating-point noise.
    const drift = Math.hypot(
      afterExpansion!.x - afterDrag!.x,
      afterExpansion!.y - afterDrag!.y,
    );
    expect(
      drift,
      `pinned node drifted by ${drift.toFixed(6)} graph units across an additive expansion`,
    ).toBeLessThan(0.001);
  });

  test("Hover focus mode dims non-neighbours and keeps incident edges saturated (#458)", async ({
    page,
  }) => {
    const seed = encodeURIComponent("e2e:illum:hub");
    await page.goto(`/illuminate?seed=${seed}`);
    await expect(page.getByTestId("illuminate-counter")).toContainText(
      "5 vertices",
    );

    type Bridge = {
      setHoveredNode: (k: string | null) => boolean;
      getRenderedNodeColor: (k: string) => string | null;
      getRenderedEdgeColor: (k: string) => string | null;
      hoveredNode: () => string | null;
    };
    await page.waitForFunction(() => {
      const win = window as Window & { __illuminateCanvas?: Bridge };
      return !!win.__illuminateCanvas;
    });

    const readNode = (k: string): Promise<string | null> =>
      page.evaluate((key) => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        return win.__illuminateCanvas?.getRenderedNodeColor(key) ?? null;
      }, k);
    const readEdge = (k: string): Promise<string | null> =>
      page.evaluate((key) => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        return win.__illuminateCanvas?.getRenderedEdgeColor(key) ?? null;
      }, k);
    const setHover = (k: string | null): Promise<boolean> =>
      page.evaluate((key) => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        return win.__illuminateCanvas?.setHoveredNode(key) ?? false;
      }, k);
    const hoveredNow = (): Promise<string | null> =>
      page.evaluate(() => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        return win.__illuminateCanvas?.hoveredNode() ?? null;
      });

    // Layout (see beforeAll seeder):
    //
    //    hub --(1)--> left      --(1)--> leftleft  --(1)--> leftleftleft
    //    hub --(3)--> right     --(2)--> rightright
    //
    // From `hub` the 2-hop frontier is {left, right, leftleft,
    // rightright} but only `left` + `right` are direct neighbours,
    // so `leftleft` and `rightright` MUST dim when hub is focused.
    const hubKey = "e2e:illum:hub";
    const leftKey = "e2e:illum:left";
    const rightKey = "e2e:illum:right";
    const leftLeftKey = "e2e:illum:leftleft";
    const rightRightKey = "e2e:illum:rightright";
    // Edge ids follow `${tail}→${head}` (see edgeIdOf in
    // app/lib/client/usecase/illuminate/state.ts).
    const hubToLeftEdge = `${hubKey}→${leftKey}`;
    const leftToLeftLeftEdge = `${leftKey}→e2e:illum:leftleft`;

    const DIM_NODE = "#3f3f4626";
    const DIM_EDGE = "#bdbdbd26";

    // Sanity: baseline colours BEFORE any hover. The reducer returns
    // `data` unchanged when nothing is focused, so every node renders
    // at whatever colour the data layer wrote into graphology.
    expect(await hoveredNow()).toBeNull();
    const baselineHub = await readNode(hubKey);
    const baselineLeft = await readNode(leftKey);
    const baselineRight = await readNode(rightKey);
    const baselineLeftLeft = await readNode(leftLeftKey);
    const baselineRightRight = await readNode(rightRightKey);
    const baselineHubLeft = await readEdge(hubToLeftEdge);
    const baselineLeftLeftLeft = await readEdge(leftToLeftLeftEdge);
    for (const c of [
      baselineHub,
      baselineLeft,
      baselineRight,
      baselineLeftLeft,
      baselineRightRight,
      baselineHubLeft,
      baselineLeftLeftLeft,
    ]) {
      expect(c).not.toBeNull();
      // Dim swatches end in `26` (alpha 0x26 ≈ 0.15). The baseline
      // must NOT use either dim swatch.
      expect(c).not.toBe(DIM_NODE);
      expect(c).not.toBe(DIM_EDGE);
    }

    // Focus hub: hub + neighbours stay saturated, 2-hop dims.
    expect(await setHover(hubKey)).toBe(true);
    expect(await hoveredNow()).toBe(hubKey);
    expect(await readNode(hubKey)).toBe(baselineHub);
    expect(await readNode(leftKey)).toBe(baselineLeft);
    expect(await readNode(rightKey)).toBe(baselineRight);
    expect(await readNode(leftLeftKey)).toBe(DIM_NODE);
    expect(await readNode(rightRightKey)).toBe(DIM_NODE);

    // Edges incident to hub keep their base colour; edges not touching
    // hub get the dim edge swatch.
    expect(await readEdge(hubToLeftEdge)).toBe(baselineHubLeft);
    expect(await readEdge(leftToLeftLeftEdge)).toBe(DIM_EDGE);

    // Switching focus to `left` shifts the focus set: `hub` AND
    // `leftleft` are now neighbours of `left`, while `right` +
    // `rightright` dim instead. This proves the reducer recomputes
    // the focus set per hover (not just the first time).
    expect(await setHover(leftKey)).toBe(true);
    expect(await readNode(hubKey)).toBe(baselineHub);
    expect(await readNode(leftLeftKey)).toBe(baselineLeftLeft);
    expect(await readNode(rightKey)).toBe(DIM_NODE);
    expect(await readNode(rightRightKey)).toBe(DIM_NODE);

    // Clear the hover → every colour returns to its baseline.
    expect(await setHover(null)).toBe(true);
    expect(await hoveredNow()).toBeNull();
    expect(await readNode(hubKey)).toBe(baselineHub);
    expect(await readNode(leftKey)).toBe(baselineLeft);
    expect(await readNode(rightKey)).toBe(baselineRight);
    expect(await readNode(leftLeftKey)).toBe(baselineLeftLeft);
    expect(await readNode(rightRightKey)).toBe(baselineRightRight);
    expect(await readEdge(hubToLeftEdge)).toBe(baselineHubLeft);
    expect(await readEdge(leftToLeftLeftEdge)).toBe(baselineLeftLeftLeft);

    // Unknown node id: the bridge must reject without throwing AND
    // must NOT mutate the hover state.
    expect(await setHover("e2e:illum:does-not-exist")).toBe(false);
    expect(await hoveredNow()).toBeNull();
  });

  test("TTL decay fades vertex opacity and pauses the tick on hidden tabs (#459)", async ({
    page,
  }) => {
    // Seed a tiny graph specifically for #459 so the TTL fixtures
    // don't interfere with the rest of the spec's neighbourhoods.
    // The expiration is set ~10 minutes in the future so on the first
    // render `computeTtlFraction \u2248 1.0` (full alpha), then we use
    // the test bridge's `setNow` to fast-forward without waiting on
    // a real wall clock.
    //
    // LIFETIME_BUDGET_MS = 600_000 ms (see ttl-decay.ts). At T+0 the
    // fraction is 1; at T+5min it's 0.5; at T+10min it's 0; past that
    // the selector drops the vertex on the next refetch (we don't
    // exercise that path here \u2014 it has a unit test in selectors).
    const ttlSeed = "e2e:illum:ttl-seed";
    const ttlEdge = "e2e:illum:ttl-edge";
    const baseNow = Date.now();
    const tenMinutes = 10 * 60_000;
    const expirationIso = new Date(baseNow + tenMinutes).toISOString();
    await putVertices([
      { key: ttlSeed, string: "ttl-seed", expiration: expirationIso },
      { key: ttlEdge, string: "ttl-edge", expiration: expirationIso },
    ]);
    await putEdges([
      {
        tail: ttlSeed,
        head: ttlEdge,
        weight: 1,
        expiration: expirationIso,
      },
    ]);

    const seedParam = encodeURIComponent(ttlSeed);
    await page.goto(`/illuminate?seed=${seedParam}`);
    await expect(page.getByTestId("illuminate-canvas")).toBeVisible();
    await expect(page.getByTestId("illuminate-counter")).toContainText(
      "2 vertices",
    );

    type Bridge = {
      getRenderedNodeColor: (k: string) => string | null;
      getRenderedEdgeColor: (k: string) => string | null;
      tickCount: () => number;
      setNow: (ms: number | null) => void;
      forceTick: () => void;
    };
    await page.waitForFunction(() => {
      const win = window as Window & { __illuminateCanvas?: Bridge };
      return !!win.__illuminateCanvas;
    });

    const readNode = (k: string): Promise<string | null> =>
      page.evaluate((key) => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        return win.__illuminateCanvas?.getRenderedNodeColor(key) ?? null;
      }, k);
    const tickCount = (): Promise<number> =>
      page.evaluate(() => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        return win.__illuminateCanvas?.tickCount() ?? -1;
      });
    const setNow = (ms: number | null): Promise<void> =>
      page.evaluate((value) => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        win.__illuminateCanvas?.setNow(value);
      }, ms);
    const forceTick = (): Promise<void> =>
      page.evaluate(() => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        win.__illuminateCanvas?.forceTick();
      });

    // --- Part 1: opacity fades as time advances --------------------------
    //
    // Pin "now" to T0 (the baseline we sent to the server) so the
    // first tick reports `fraction \u2248 1.0` and the color carries an
    // alpha byte near `ff`.
    await setNow(baseNow);
    await forceTick();
    const colorAtT0 = await readNode(ttlSeed);
    expect(
      colorAtT0,
      `expected a rendered color for ${ttlSeed} at T0`,
    ).not.toBeNull();
    // applyTtlFade always returns 9 chars ('#' + RRGGBBAA) when the
    // vertex has an expiration. The alpha byte (last two chars) MUST
    // be high (>= 0xf0) at T0 because remaining lifetime is the full
    // budget.
    expect(colorAtT0!).toMatch(/^#[0-9a-f]{8}$/i);
    const alphaT0 = parseInt(colorAtT0!.slice(7, 9), 16);
    expect(alphaT0).toBeGreaterThanOrEqual(0xf0);

    // Fast-forward to T+5min: half the budget is gone, so the alpha
    // byte should drop into the (0.25 + 0.5 * 0.75) * 255 \u2248 159
    // territory. We assert a generous window (130..200) to tolerate
    // tiny clock skew and rounding.
    await setNow(baseNow + 5 * 60_000);
    await forceTick();
    const colorAtT5 = await readNode(ttlSeed);
    expect(colorAtT5).not.toBeNull();
    expect(colorAtT5!).toMatch(/^#[0-9a-f]{8}$/i);
    const alphaT5 = parseInt(colorAtT5!.slice(7, 9), 16);
    expect(alphaT5).toBeLessThan(alphaT0);
    expect(alphaT5).toBeGreaterThanOrEqual(130);
    expect(alphaT5).toBeLessThanOrEqual(200);

    // Fast-forward to T+10min: zero budget remaining \u2192 fraction = 0,
    // alpha clamps to MIN_ALPHA (0.25 * 255 \u2248 64). Past expiry the
    // selector would drop the node on the next fetch, but the
    // reducer is the only layer running here so it pins at MIN_ALPHA.
    await setNow(baseNow + tenMinutes);
    await forceTick();
    const colorAtTExpiry = await readNode(ttlSeed);
    expect(colorAtTExpiry).not.toBeNull();
    expect(colorAtTExpiry!).toMatch(/^#[0-9a-f]{8}$/i);
    const alphaTExpiry = parseInt(colorAtTExpiry!.slice(7, 9), 16);
    expect(alphaTExpiry).toBeLessThan(alphaT5);
    // MIN_ALPHA rounds to 64. Allow \u00b12 for rounding.
    expect(alphaTExpiry).toBeGreaterThanOrEqual(62);
    expect(alphaTExpiry).toBeLessThanOrEqual(66);

    // --- Part 2: hidden tab pauses the tick ------------------------------
    //
    // Release the clock override so the production tick path is
    // fully exercised, then wait for the interval to fire at least
    // once.
    await setNow(null);
    const tickBefore = await tickCount();
    // The 1Hz interval starts on mount with an immediate baseline
    // tick (see `start()` in IlluminateCanvas.tsx). Wait long enough
    // for at least one scheduled tick so our "before" sample is
    // stable. 1500ms allows for 1\u20132 ticks (1 scheduled + maybe a
    // visibilitychange-induced start).
    await page.waitForTimeout(1500);
    const tickAfterVisible = await tickCount();
    expect(
      tickAfterVisible,
      `expected the tick counter to advance while the tab is visible (before=${tickBefore} after=${tickAfterVisible})`,
    ).toBeGreaterThan(tickBefore);

    // Flip visibility to hidden. The component listens on
    // `visibilitychange` and calls `clearInterval` so no more ticks
    // should fire. We can't actually hide the page (Playwright keeps
    // it foregrounded), so we monkey-patch `document.visibilityState`
    // to report "hidden" AND dispatch the event manually \u2014 the
    // production code only reads `document.visibilityState` from
    // inside the handler so the spoof is observationally
    // indistinguishable from a real hide.
    await page.evaluate(() => {
      Object.defineProperty(document, "visibilityState", {
        configurable: true,
        get: () => "hidden",
      });
      document.dispatchEvent(new Event("visibilitychange"));
    });
    const tickAtHide = await tickCount();
    // Wait long enough for a real tick to have fired had we NOT
    // stopped the interval.
    await page.waitForTimeout(1500);
    const tickAfterHidden = await tickCount();
    expect(
      tickAfterHidden,
      `tick counter MUST NOT advance while the tab is hidden (atHide=${tickAtHide} afterHidden=${tickAfterHidden})`,
    ).toBe(tickAtHide);

    // Restore visibility and confirm the tick resumes. The
    // visibilitychange handler runs `start()` which immediately
    // ticks once (the "baseline" tick), so the count must advance.
    await page.evaluate(() => {
      Object.defineProperty(document, "visibilityState", {
        configurable: true,
        get: () => "visible",
      });
      document.dispatchEvent(new Event("visibilitychange"));
    });
    const tickAfterRestore = await tickCount();
    expect(
      tickAfterRestore,
      "expected the immediate baseline tick on visibilitychange to advance the counter",
    ).toBeGreaterThan(tickAfterHidden);
  });

  test("Hop-distance coloring separates each ring and never grows existing distances across an additive expansion (#460)", async ({
    page,
  }) => {
    const seed = encodeURIComponent("e2e:illum:hub");
    await page.goto(`/illuminate?seed=${seed}`);
    await expect(page.getByTestId("illuminate-counter")).toContainText(
      "5 vertices",
    );

    type Bridge = {
      getRenderedNodeColor: (k: string) => string | null;
      getNodeHopDistance: (k: string) => number | null;
    };
    await page.waitForFunction(() => {
      const win = window as Window & { __illuminateCanvas?: Bridge };
      return !!win.__illuminateCanvas;
    });

    const readNode = (k: string): Promise<string | null> =>
      page.evaluate((key) => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        return win.__illuminateCanvas?.getRenderedNodeColor(key) ?? null;
      }, k);
    const readHop = (k: string): Promise<number | null> =>
      page.evaluate((key) => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        return win.__illuminateCanvas?.getNodeHopDistance(key) ?? null;
      }, k);

    const hubKey = "e2e:illum:hub";
    const leftKey = "e2e:illum:left";
    const rightKey = "e2e:illum:right";
    const leftLeftKey = "e2e:illum:leftleft";
    const rightRightKey = "e2e:illum:rightright";
    const leftLeftLeftKey = "e2e:illum:leftleftleft";

    // === Phase 1: initial seed from hub ==================================
    // The default step=2 brings the full 2-hop frontier in one shot, so
    // the selector sees one expansion with origin=hub. Hop distances:
    //   hub=0, left=1, right=1, leftleft=2, rightright=2.
    expect(await readHop(hubKey)).toBe(0);
    expect(await readHop(leftKey)).toBe(1);
    expect(await readHop(rightKey)).toBe(1);
    expect(await readHop(leftLeftKey)).toBe(2);
    expect(await readHop(rightRightKey)).toBe(2);

    // Rendered colours must be distinct across hop buckets: that's the
    // whole point of the encoding. Nodes WITHIN a bucket share the
    // same colour (left vs right, leftleft vs rightright).
    const hubColor = await readNode(hubKey);
    const leftColor = await readNode(leftKey);
    const leftLeftColor = await readNode(leftLeftKey);
    expect(hubColor).not.toBeNull();
    expect(leftColor).not.toBeNull();
    expect(leftLeftColor).not.toBeNull();
    expect(hubColor).not.toBe(leftColor);
    expect(leftColor).not.toBe(leftLeftColor);
    expect(hubColor).not.toBe(leftLeftColor);
    // Same-bucket nodes match.
    expect(await readNode(rightKey)).toBe(leftColor);
    expect(await readNode(rightRightKey)).toBe(leftLeftColor);

    // Legend is visible and surfaces the three populated buckets.
    const legend = page.getByTestId("illuminate-legend");
    await expect(legend).toBeVisible();
    await expect(page.getByTestId("illuminate-legend-origin")).toBeVisible();
    await expect(page.getByTestId("illuminate-legend-1hop")).toBeVisible();
    await expect(page.getByTestId("illuminate-legend-2hop")).toBeVisible();
    // No 3+ or unreachable bucket yet — those rows are hidden when
    // empty so the legend stays compact.
    await expect(page.getByTestId("illuminate-legend-far")).toHaveCount(0);
    await expect(page.getByTestId("illuminate-legend-unreachable")).toHaveCount(
      0,
    );

    // === Phase 2: additive expansion from leftleft =======================
    // Adds leftleftleft (a fresh vertex) AND a second expansion origin
    // (leftleft). After this:
    //   - hub stays at hop 0 (still the original seed AND still an
    //     expansion origin via the URL anchor; multi-source BFS keeps
    //     it at 0).
    //   - leftleft drops from hop 2 → hop 0 (it's now an expansion
    //     origin itself).
    //   - left drops from hop 1 → hop 1 (already at 1 from hub; can't
    //     get lower since the chain hub→left→leftleft is 1 hop apart).
    //   - leftleftleft enters at hop 1 (one edge from the new origin).
    //
    // The monotonic-shrink invariant is the key thing this test
    // protects: every existing vertex's hop distance must stay the
    // same or shrink, NEVER grow.
    await page
      .getByRole("group")
      .getByText(/List view \(5 vertices/)
      .click();
    const table = page.getByTestId("illuminate-table");
    await table
      .getByRole("button", { name: "Expand from e2e:illum:leftleft" })
      .click();
    await expect(page.getByTestId("illuminate-counter")).toContainText(
      "6 vertices",
    );

    const newHubHop = await readHop(hubKey);
    const newLeftHop = await readHop(leftKey);
    const newRightHop = await readHop(rightKey);
    const newLeftLeftHop = await readHop(leftLeftKey);
    const newRightRightHop = await readHop(rightRightKey);
    const newLeftLeftLeftHop = await readHop(leftLeftLeftKey);

    // The new origin and the vertex one edge from it.
    expect(newLeftLeftHop).toBe(0);
    expect(newLeftLeftLeftHop).toBe(1);

    // Monotonic shrink: every previous distance MUST be ≤ what it was
    // in phase 1.
    expect(newHubHop).not.toBeNull();
    expect(newLeftHop).not.toBeNull();
    expect(newRightHop).not.toBeNull();
    expect(newRightRightHop).not.toBeNull();
    expect(newHubHop).toBeLessThanOrEqual(0);
    expect(newLeftHop).toBeLessThanOrEqual(1);
    expect(newRightHop).toBeLessThanOrEqual(1);
    expect(newLeftLeftHop).toBeLessThanOrEqual(2);
    expect(newRightRightHop).toBeLessThanOrEqual(2);

    // Concretely: hub stays at 0 (still an origin), leftleft is now
    // also at 0 (new origin), and the previously-2-hop leftleft is
    // now a brand colour, not a far colour.
    expect(newHubHop).toBe(0);
    expect(await readNode(leftLeftKey)).toBe(hubColor);
  });

  test("Lineage chip strip scrolls the camera back to an expansion origin without mutating state (#456)", async ({
    page,
  }) => {
    const seed = encodeURIComponent("e2e:illum:hub");
    await page.goto(`/illuminate?seed=${seed}`);
    await expect(page.getByTestId("illuminate-toolbar")).toBeVisible();

    const counter = page.getByTestId("illuminate-counter");
    await expect(counter).toContainText("5 vertices");
    await expect(counter).toContainText("1 expansion");

    // The strip starts with a single seed chip carrying the seed marker.
    await expect(page.getByTestId("illuminate-expansion-chips")).toBeVisible();
    const seedChip = page.getByTestId("illuminate-chip-0");
    await expect(seedChip).toHaveAttribute("data-chip-origin", "e2e:illum:hub");
    await expect(seedChip).toHaveAttribute("data-chip-is-seed", "true");

    // Grow the lineage with an additive expansion from `leftleft` so a
    // SECOND, non-seed chip appears.
    await page
      .getByRole("group")
      .getByText(/List view \(5 vertices/)
      .click();
    const table = page.getByTestId("illuminate-table");
    await table
      .getByRole("button", { name: "Expand from e2e:illum:leftleft" })
      .click();
    await expect(counter).toContainText("6 vertices");
    await expect(counter).toContainText("2 expansions");

    // Now there are two chips: the seed (chip 0) and the leftleft
    // expansion (chip 1). The second is NOT marked as the seed.
    const expansionChip = page.getByTestId("illuminate-chip-1");
    await expect(expansionChip).toHaveAttribute(
      "data-chip-origin",
      "e2e:illum:leftleft",
    );
    await expect(expansionChip).toHaveAttribute("data-chip-is-seed", "false");

    // Wait for the camera test bridge.
    type Camera = { x: number; y: number; ratio: number; angle: number };
    type Pos = { x: number; y: number };
    type Bridge = {
      cameraState: () => Camera;
      getNodeDisplayPosition: (k: string) => Pos | null;
      isNodeHighlighted: (k: string) => boolean;
    };
    await page.waitForFunction(() => {
      const win = window as Window & { __illuminateCanvas?: Bridge };
      return !!win.__illuminateCanvas?.cameraState;
    });

    const readCamera = (): Promise<Camera> =>
      page.evaluate(() => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        return win.__illuminateCanvas!.cameraState();
      });
    const readDisplay = (k: string): Promise<Pos | null> =>
      page.evaluate((key) => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        return win.__illuminateCanvas?.getNodeDisplayPosition(key) ?? null;
      }, k);
    const isHighlighted = (k: string): Promise<boolean> =>
      page.evaluate((key) => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        return win.__illuminateCanvas?.isNodeHighlighted(key) ?? false;
      }, k);

    // Snapshot the camera, then click the leftleft chip. Its display
    // position is off-centre from the seed-anchored view, so the camera
    // must move toward it.
    const before = await readCamera();
    const target = await readDisplay("e2e:illum:leftleft");
    expect(target).not.toBeNull();

    await expansionChip.click();

    // The pan animates over ~600 ms — wait until the camera arrives at
    // the leftleft display coordinates (within a small tolerance).
    await page.waitForFunction(
      ({ key }) => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        const b = win.__illuminateCanvas;
        if (!b) return false;
        const cam = b.cameraState();
        const dp = b.getNodeDisplayPosition(key);
        if (!dp) return false;
        return Math.abs(cam.x - dp.x) < 0.02 && Math.abs(cam.y - dp.y) < 0.02;
      },
      { key: "e2e:illum:leftleft" },
      { timeout: 4000 },
    );

    const after = await readCamera();
    // The camera genuinely moved (the click was not a no-op).
    const moved =
      Math.abs(after.x - before.x) > 1e-6 ||
      Math.abs(after.y - before.y) > 1e-6;
    expect(moved).toBe(true);

    // The transient highlight pulse fired on the target.
    expect(await isHighlighted("e2e:illum:leftleft")).toBe(true);

    // Crucially the click is UI-only: it does NOT re-fetch, push a new
    // expansion, or change the accumulator. Counts are unchanged.
    await expect(counter).toContainText("6 vertices");
    await expect(counter).toContainText("2 expansions");
    await expect(page).toHaveURL(/\?seed=e2e%3Aillum%3Ahub/);

    // The highlight reverts after the pulse window (~600 ms) elapses.
    await page.waitForFunction(
      ({ key }) => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        return win.__illuminateCanvas?.isNodeHighlighted(key) === false;
      },
      { key: "e2e:illum:leftleft" },
      { timeout: 4000 },
    );
  });

  test("Info icon opens a read-only detail Drawer; Expand from here grows the lineage (#461)", async ({
    page,
  }) => {
    const seed = encodeURIComponent("e2e:illum:hub");
    await page.goto(`/illuminate?seed=${seed}`);
    await expect(page.getByTestId("illuminate-toolbar")).toBeVisible();

    const counter = page.getByTestId("illuminate-counter");
    await expect(counter).toContainText("5 vertices");
    await expect(counter).toContainText("1 expansion");

    // Wait for the #461 test-bridge surface, then surface the info icon
    // for `left` exactly as a real hover would. The WebGL hover hit-test
    // is flaky under headless chromium, so we drive the icon through the
    // bridge — the icon it renders and the click path are the real ones.
    type Bridge = {
      showInfoIcon: (k: string) => boolean;
      infoIconNode: () => string | null;
      inspectNode: (k: string) => boolean;
    };
    await page.waitForFunction(() => {
      const win = window as Window & { __illuminateCanvas?: Bridge };
      return !!win.__illuminateCanvas?.showInfoIcon;
    });
    const shown = await page.evaluate(() => {
      const win = window as Window & { __illuminateCanvas?: Bridge };
      return win.__illuminateCanvas!.showInfoIcon("e2e:illum:left");
    });
    expect(shown).toBe(true);

    // The icon is the one canvas overlay that takes pointer events.
    const infoIcon = page.getByTestId("illuminate-info-icon");
    await expect(infoIcon).toBeVisible();
    await expect(infoIcon).toHaveAttribute("aria-label", /e2e:illum:left/);

    // Clicking it opens the detail Drawer for that vertex.
    await infoIcon.click();
    const panel = page.getByTestId("illuminate-node-detail");
    await expect(panel).toBeVisible();
    await expect(page.getByTestId("illuminate-detail-key")).toHaveText(
      "e2e:illum:left",
    );

    // Inspecting is read-only: it must NOT expand or refetch. The
    // accumulator counts stay exactly where they were.
    await expect(counter).toContainText("5 vertices");
    await expect(counter).toContainText("1 expansion");

    // The accumulator already holds the outgoing edge left -> leftleft,
    // so the outgoing list renders without any RPC.
    await expect(page.getByTestId("illuminate-detail-outgoing")).toContainText(
      "e2e:illum:leftleft",
    );

    // "Show inbound edges" issues a fresh prefix scan. The wire scan is a
    // prefix match, so headPrefix "e2e:illum:left" over-matches
    // `leftleft`/`leftleftleft`; the panel must filter to the exact
    // inbound edge hub -> left only.
    await page.getByTestId("illuminate-detail-inbound-toggle").click();
    const inbound = page.getByTestId("illuminate-detail-inbound");
    await expect(inbound).toBeVisible();
    await expect(inbound).toContainText("e2e:illum:hub");
    await expect(inbound.getByRole("listitem")).toHaveCount(1);

    // "Expand from here" fires an additive expansion from `left` and
    // closes the Drawer. `leftleftleft` is the one vertex outside the
    // current 2-hop frame, so the accumulator grows by exactly one.
    await page.getByTestId("illuminate-detail-expand").click();
    await expect(panel).toBeHidden();
    await expect(counter).toContainText("6 vertices");
    await expect(counter).toContainText("2 expansions");

    // A new, non-seed lineage chip records the `left` expansion (#456).
    const expansionChip = page.getByTestId("illuminate-chip-1");
    await expect(expansionChip).toHaveAttribute(
      "data-chip-origin",
      "e2e:illum:left",
    );
    await expect(expansionChip).toHaveAttribute("data-chip-is-seed", "false");
  });

  test("Directed edges render with the arrow program (#485)", async ({
    page,
  }) => {
    const seed = encodeURIComponent("e2e:illum:hub");
    await page.goto(`/illuminate?seed=${seed}`);
    await expect(page.getByTestId("illuminate-counter")).toContainText(
      "5 vertices",
    );

    type Bridge = {
      getDefaultEdgeType: () => string;
      getRenderedEdgeColor: (k: string) => string | null;
    };
    await page.waitForFunction(() => {
      const win = window as Window & { __illuminateCanvas?: Bridge };
      return !!win.__illuminateCanvas;
    });

    // The graphology graph is directed (tail → head). Sigma's default
    // edge program draws plain undirected bars, so the canvas must opt
    // into the arrow program to make direction legible. No edge sets a
    // per-edge `type`, so the resolved `defaultEdgeType` governs the
    // whole graph — asserting it is `"arrow"` proves every edge renders
    // an arrowhead.
    const defaultEdgeType = await page.evaluate(() => {
      const win = window as Window & { __illuminateCanvas?: Bridge };
      return win.__illuminateCanvas?.getDefaultEdgeType() ?? null;
    });
    expect(defaultEdgeType).toBe("arrow");

    // Sanity: an actual edge from the seeded graph carries rendered
    // display data, so the arrow program governs a live edge rather than
    // an empty registry. Edge ids follow `${tail}→${head}` (see edgeIdOf
    // in app/lib/client/usecase/illuminate/state.ts).
    const hubToLeftEdge = "e2e:illum:hub→e2e:illum:left";
    const renderedColor = await page.evaluate((edgeId) => {
      const win = window as Window & { __illuminateCanvas?: Bridge };
      return win.__illuminateCanvas?.getRenderedEdgeColor(edgeId) ?? null;
    }, hubToLeftEdge);
    expect(renderedColor).not.toBeNull();
  });

  test("Hovered label chip contrasts with its text in both themes (#484)", async ({
    page,
  }) => {
    const seed = encodeURIComponent("e2e:illum:hub");
    await page.goto(`/illuminate?seed=${seed}`);
    await expect(page.getByTestId("illuminate-counter")).toContainText(
      "5 vertices",
    );

    type Bridge = {
      getHoverLabelColors: () => {
        background: string;
        stroke: string;
        text: string;
      };
    };
    await page.waitForFunction(() => {
      const win = window as Window & { __illuminateCanvas?: Bridge };
      return !!win.__illuminateCanvas?.getHoverLabelColors;
    });

    // #484: sigma's default hover renderer painted a near-white box and
    // drew the label in `--colorNeutralForeground1`, which is also
    // near-white in dark theme — an unreadable white-on-white collision.
    // The custom renderer skins the chip from `--colorNeutralBackground1`
    // (+ a 1px `--colorNeutralStroke1` border) so the box always
    // contrasts with the text. Asserting the resolved tokens differ in
    // the *live* theme proves the wiring resolves real Fluent variables
    // (not just the fallback literals) and that they never collide.
    const colors = await page.evaluate(() => {
      const win = window as Window & { __illuminateCanvas?: Bridge };
      return win.__illuminateCanvas?.getHoverLabelColors() ?? null;
    });
    expect(colors).not.toBeNull();
    expect(colors?.background).toBeTruthy();
    expect(colors?.stroke).toBeTruthy();
    expect(colors?.text).toBeTruthy();
    // The chip background and the label text must be different colours —
    // the exact collision #484 fixes.
    expect(colors?.background).not.toBe(colors?.text);
  });
});
