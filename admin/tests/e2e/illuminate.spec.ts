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

  test("renders the canvas and neighbour table for a seed", async ({
    page,
  }) => {
    const seed = encodeURIComponent("e2e:illum:hub");
    await page.goto(`/illuminate?seed=${seed}`);

    await expect(page.getByTestId("illuminate-toolbar")).toBeVisible();
    await expect(page.getByTestId("illuminate-seed")).toHaveText(
      "e2e:illum:hub",
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
    await expect(page.getByTestId("illuminate-seed")).toHaveText(
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
});
