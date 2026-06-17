import { expect, test, type Page } from "@playwright/test";

import { CONNECT_URL, STORAGE_KEY, putEdges, putVertices } from "./helpers";

/**
 * `/cli` canvas regression guards.
 *
 * #651 folded the standalone `/illuminate` route into `/cli` and deleted the
 * page-accumulator UI (seed prompt, toolbar, neighbour table, lineage chips,
 * Clear/Expand). The KEPT IlluminateCanvas component now renders inside the CLI
 * canvas panel (`cli-canvas-panel`), reached by running a graph-producing
 * command.
 *
 * This file used to be `illuminate.spec.ts`; its accumulator-UI tests were
 * removed and the canvas-only guards that survive the model change were
 * re-pointed at the CLI bootstrap. `illuminate <hub> 2 5` reproduces the exact
 * 2-hop frontier the old `/illuminate?seed=hub` initial render produced, so the
 * ported guards assert the same initial canvas frame.
 *
 * The remaining canvas guards (seed-anchor pinning, hover focus, TTL decay,
 * directed arrows, label-chip contrast, hop-ring colouring) were coupled to the
 * deleted accumulator UI / multi-source expansion model and are tracked for
 * CLI-native restoration in #664. `cli.spec.ts` already covers canvas mount,
 * persistence, the axis picker, and the splitter on `/cli`.
 */

/**
 * Seeds a small hub chain so the canvas has a multi-ring neighbourhood to
 * render:
 *
 *    hub --(1)--> left --(1)--> leftleft --(1)--> leftleftleft
 *    hub --(3)--> right --(2)--> rightright
 *
 * `illuminate e2e:illum:hub 2 5` (step 2, k 5 — the canonical click form)
 * brings in the full 2-hop frontier — {hub, left, right, leftleft, rightright}
 * + 4 edges — which is exactly what the canvas-only guards below assert on.
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

/**
 * Boots the CLI explorer and renders the hub neighbourhood onto the canvas.
 * #651 folded the standalone `/illuminate` route into `/cli`, so the canvas
 * (the KEPT IlluminateCanvas component) is now reached by running a
 * graph-producing command rather than by navigating to a dedicated route.
 */
async function renderHub(page: Page): Promise<void> {
  await page.goto("/cli");
  const input = page.getByTestId("cli-input");
  await input.fill("illuminate e2e:illum:hub 2 5");
  await input.press("Enter");
  await expect(page.getByTestId("cli-canvas-panel")).toBeVisible();
  await expect(page.getByTestId("illuminate-canvas")).toBeVisible();
}

test.describe("/cli canvas", () => {
  test("Canvas labels reach WCAG AA contrast against the canvas background (#453)", async ({
    page,
  }) => {
    await renderHub(page);

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

  // #517 — the operator can independently hide vertex and edge labels.
  // The toggles drive Sigma's renderLabels / renderEdgeLabels settings,
  // surfaced for assertion through the canvas bridge.
  test("label toggles hide and restore vertex and edge labels (#517)", async ({
    page,
  }) => {
    await renderHub(page);

    type LabelBridge = {
      getLabelVisibility?: () => { vertex: boolean; edge: boolean };
    };
    // The label-visibility bridge is installed in the sigma mount effect.
    await page.waitForFunction(() => {
      const win = window as Window & { __illuminateCanvas?: LabelBridge };
      return !!win.__illuminateCanvas?.getLabelVisibility;
    });
    const vis = (): Promise<{ vertex: boolean; edge: boolean }> =>
      page.evaluate(() => {
        const win = window as Window & { __illuminateCanvas?: LabelBridge };
        return (
          win.__illuminateCanvas?.getLabelVisibility?.() ?? {
            vertex: false,
            edge: false,
          }
        );
      });

    // Both kinds of labels are on by default.
    expect(await vis()).toEqual({ vertex: true, edge: true });

    // Toggling vertex labels off hides only the vertex labels.
    await page.getByTestId("illuminate-toggle-node-labels").click();
    await expect.poll(async () => (await vis()).vertex).toBe(false);
    expect((await vis()).edge).toBe(true);

    // Toggling edge labels off hides them too, independently.
    await page.getByTestId("illuminate-toggle-edge-labels").click();
    await expect.poll(async () => (await vis()).edge).toBe(false);

    // Toggling vertex labels back on restores only them.
    await page.getByTestId("illuminate-toggle-node-labels").click();
    await expect.poll(async () => (await vis()).vertex).toBe(true);
    expect((await vis()).edge).toBe(false);
  });

  test("Drag releases the node without pinning it; physics reclaims it (#491)", async ({
    page,
  }) => {
    await renderHub(page);

    type Pos = { x: number; y: number };
    type Bridge = {
      getNodePosition: (k: string) => Pos | null;
      isNodeFixed: (k: string) => boolean;
      dragStats: () => { downNode: number; moveBody: number; mouseUp: number };
      simulateDrag: (k: string, dx: number, dy: number) => boolean;
      setLayoutPaused: (paused: boolean) => void;
      stepLayout: (ticks: number) => number;
    };
    await page.waitForFunction(() => {
      const win = window as Window & { __illuminateCanvas?: Bridge };
      return !!win.__illuminateCanvas?.setLayoutPaused;
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
    const pause = (p: boolean): Promise<void> =>
      page.evaluate((paused) => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        win.__illuminateCanvas?.setLayoutPaused(paused);
      }, p);
    const step = (n: number): Promise<number> =>
      page.evaluate((ticks) => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        return win.__illuminateCanvas?.stepLayout(ticks) ?? 0;
      }, n);

    // Pause the continuous layout BEFORE the gesture so the reheat that
    // finishDrag triggers (#491) builds a fresh simulation but does NOT
    // tick it. That makes the immediate post-drag position deterministic
    // (no rAF race), so we can assert the exact drag delta, then step the
    // simulation by hand to prove the node is NOT pinned.
    await pause(true);

    // Sanity: before the gesture, the node exists, has a graph position,
    // and is not pinned.
    const before = await readGraphPos(targetKey);
    expect(before).not.toBeNull();
    expect(await isFixed(targetKey)).toBe(false);

    // #651: on /cli a node click dispatches `illuminate <key> 2 5` into the
    // scrollback (onNodeClick → runRaw), so a stray click would grow the
    // OK-entry count. Capture it now to prove the drag below is NOT misread
    // as a click — the CLI analog of the old accumulator counter holding at
    // "1 expansion".
    const okEntriesBefore = await page.getByTestId("cli-entry-ok").count();

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
    // so we cover the position-write + release + dragStats accounting
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

    // Immediately after release (layout paused, sim not ticked): the node
    // sits at exactly before+delta and is NOT pinned (#491 — drag-to-pin
    // was removed). simulateDrag bypasses sigma's viewport↔graph
    // projection so the graph-space delta is what we put in.
    const afterDrag = await readGraphPos(targetKey);
    expect(afterDrag).not.toBeNull();
    expect(await isFixed(targetKey)).toBe(false);
    expect(afterDrag!.x - before!.x).toBeCloseTo(deltaX, 6);
    expect(afterDrag!.y - before!.y).toBeCloseTo(deltaY, 6);

    // No pin: the reheated simulation is free to relax the dropped node.
    // Stepping the sim by hand must MOVE `left` away from where the drag
    // left it — proving physics reclaims it instead of freezing it at the
    // cursor (the old drag-to-pin behaviour held it exactly in place).
    const ticked = await step(40);
    expect(ticked, "stepLayout should advance the simulation").toBeGreaterThan(
      0,
    );
    const afterTicks = await readGraphPos(targetKey);
    expect(afterTicks).not.toBeNull();
    const reclaimed = Math.hypot(
      afterTicks!.x - afterDrag!.x,
      afterTicks!.y - afterDrag!.y,
    );
    expect(
      reclaimed,
      `unpinned node should be moved by the simulation, but only drifted ${reclaimed.toFixed(4)} units`,
    ).toBeGreaterThan(1);

    // The drag must NOT have been interpreted as a click — if a future
    // refactor accidentally routed finishDrag through onNodeClick, a new
    // `illuminate …` command would have landed in the scrollback. Asserted
    // last, after several async bridge round-trips, so any stray click's
    // dispatch has had time to surface. The canvas also still shows the hub
    // command as its source (a click would re-source it to `left`).
    expect(await page.getByTestId("cli-entry-ok").count()).toBe(
      okEntriesBefore,
    );
    await expect(page.getByTestId("cli-canvas-panel")).toContainText(
      "illuminate e2e:illum:hub 2 5",
    );
  });
});
