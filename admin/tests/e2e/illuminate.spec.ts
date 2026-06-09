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

  // #517 — the operator can independently hide vertex and edge labels.
  // The toggles drive Sigma's renderLabels / renderEdgeLabels settings,
  // surfaced for assertion through the canvas bridge.
  test("label toggles hide and restore vertex and edge labels (#517)", async ({
    page,
  }) => {
    const seed = encodeURIComponent("e2e:illum:hub");
    await page.goto(`/illuminate?seed=${seed}`);
    await expect(page.getByTestId("illuminate-counter")).toContainText(
      "5 vertices",
    );

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

  test("No positional snap at t=0 after a click, then gradual non-overlapping easing (#483)", async ({
    page,
  }) => {
    const seed = encodeURIComponent("e2e:illum:hub");
    await page.goto(`/illuminate?seed=${seed}`);
    await expect(page.getByTestId("illuminate-toolbar")).toBeVisible();
    await expect(page.getByTestId("illuminate-counter")).toContainText(
      "5 vertices",
    );

    type Pos = { x: number; y: number };
    type Bridge = {
      getNodePosition: (k: string) => Pos | null;
      setLayoutPaused: (paused: boolean) => void;
      stepLayout: (ticks: number) => number;
      settleLayout: (maxTicks?: number) => number;
      layoutRunning: () => boolean;
    };
    // The #483 layout bridge is installed in the sigma mount effect.
    await page.waitForFunction(() => {
      const win = window as Window & { __illuminateCanvas?: Bridge };
      return !!win.__illuminateCanvas?.setLayoutPaused;
    });

    const readPos = (k: string): Promise<Pos | null> =>
      page.evaluate((key) => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        return win.__illuminateCanvas?.getNodePosition(key) ?? null;
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
    const settle = (): Promise<number> =>
      page.evaluate(() => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        return win.__illuminateCanvas?.settleLayout() ?? 0;
      });
    const layoutRunning = (): Promise<boolean> =>
      page.evaluate(() => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        return win.__illuminateCanvas?.layoutRunning() ?? false;
      });

    const seedKeys = [
      "e2e:illum:hub",
      "e2e:illum:left",
      "e2e:illum:right",
      "e2e:illum:leftleft",
      "e2e:illum:rightright",
    ] as const;

    // The cold-start layout (empty → 5 nodes) settles synchronously, so
    // by the time the counter reads "5 vertices" the seed frame is
    // already spread out. Snapshot the five seed nodes' coordinates.
    const cold = new Map<string, Pos>();
    for (const k of seedKeys) {
      const p = await readPos(k);
      expect(p, `seed node ${k} should have a position`).not.toBeNull();
      cold.set(k, p!);
    }

    // Requirement B (spacing): the cold layout must not clump — every
    // pair of seed nodes sits a comfortable distance apart. The pre-#483
    // clumping bug left nodes < 1 unit apart; a generous floor cleanly
    // distinguishes a real spread from a knot.
    const SPACING_FLOOR = 10;
    for (let i = 0; i < seedKeys.length; i++) {
      for (let j = i + 1; j < seedKeys.length; j++) {
        const a = cold.get(seedKeys[i])!;
        const b = cold.get(seedKeys[j])!;
        const dist = Math.hypot(a.x - b.x, a.y - b.y);
        expect(
          dist,
          `${seedKeys[i]} and ${seedKeys[j]} are clumped (${dist.toFixed(2)} < ${SPACING_FLOOR})`,
        ).toBeGreaterThanOrEqual(SPACING_FLOOR);
      }
    }

    // Requirement A (no snap): pause the continuous layout BEFORE the
    // click so the post-click reconcile builds the simulation but never
    // ticks it. The node that SURVIVES into the latest result (leftleft,
    // the expansion origin) must hold its EXACT pre-click coordinates at
    // t=0 (the first rendered frame); the brand-new node lands at fresh
    // coordinates, and every node outside the latest result is DELETED
    // (#491) — readPos returns null for it.
    await pause(true);

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

    // t=0: the surviving node (leftleft) holds its exact pre-click
    // coordinates — no snap. Every other seed node falls outside the
    // latest result and is therefore DELETED (#491), so readPos is null.
    const survivorKey = "e2e:illum:leftleft";
    for (const k of seedKeys) {
      const now = await readPos(k);
      if (k === survivorKey) {
        const was = cold.get(k)!;
        expect(now, `survivor ${k} vanished after the click`).not.toBeNull();
        expect(now!.x, `${k} x snapped at t=0`).toBe(was.x);
        expect(now!.y, `${k} y snapped at t=0`).toBe(was.y);
      } else {
        expect(
          now,
          `${k} should be deleted (outside the latest result)`,
        ).toBeNull();
      }
    }
    // The newcomer is seeded a position near its parent's neighbourhood.
    const newcomer = await readPos("e2e:illum:leftleftleft");
    expect(newcomer, "leftleftleft should be seeded a position").not.toBeNull();

    // Gradual motion (no instant snap): the newcomer is seeded a couple
    // of units from its parent, so the compressed spring eases the
    // affected nodes toward equilibrium over many frames. A single tick
    // must MOVE a relaxing node (the animation is live) yet must NOT
    // carry it all the way to its final settled position (no
    // teleport-to-final like the old synchronous solve). We compare the
    // t=0, one-tick, and fully-settled positions to assert exactly that,
    // which is robust to the spring's magnitude and the random seed.
    const animatedKeys = [...seedKeys, "e2e:illum:leftleftleft"];
    const atT0 = new Map<string, Pos>();
    for (const k of animatedKeys) {
      const p = await readPos(k);
      if (p) atT0.set(k, p);
    }
    const ticked = await step(1);
    expect(ticked, "stepLayout should advance exactly one tick").toBe(1);
    const afterOneTick = new Map<string, Pos>();
    for (const k of animatedKeys) {
      const p = await readPos(k);
      if (p) afterOneTick.set(k, p);
    }
    await settle();
    const settled = new Map<string, Pos>();
    for (const k of animatedKeys) {
      const p = await readPos(k);
      if (p) settled.set(k, p);
    }

    const MOVE_EPS = 5; // total journey that counts a node as "relaxing"
    const SETTLE_EPS = 2; // still-this-far-from-final ⇒ not yet settled
    let movers = 0;
    for (const k of animatedKeys) {
      const p0 = atT0.get(k);
      const p1 = afterOneTick.get(k);
      const pf = settled.get(k);
      if (!p0 || !p1 || !pf) continue;
      const journey = Math.hypot(pf.x - p0.x, pf.y - p0.y);
      if (journey <= MOVE_EPS) continue; // (near-)static node — skip
      movers += 1;
      const firstStep = Math.hypot(p1.x - p0.x, p1.y - p0.y);
      const remaining = Math.hypot(p1.x - pf.x, p1.y - pf.y);
      expect(firstStep, `${k} did not move on the first tick`).toBeGreaterThan(
        0,
      );
      expect(
        remaining,
        `${k} snapped to its final position in a single tick (${remaining.toFixed(2)} ≤ ${SETTLE_EPS})`,
      ).toBeGreaterThan(SETTLE_EPS);
    }
    expect(
      movers,
      "expected at least one node to ease toward equilibrium",
    ).toBeGreaterThan(0);

    // The layout is already at rest after settle(): the simulation must
    // stop (no perpetual motion / battery drain), satisfying the "stop
    // once alpha cools" acceptance criterion.
    expect(await layoutRunning()).toBe(false);
  });

  test("Deletes nodes and edges outside the latest result (#491)", async ({
    page,
  }) => {
    const seed = encodeURIComponent("e2e:illum:hub");
    await page.goto(`/illuminate?seed=${seed}`);
    await expect(page.getByTestId("illuminate-toolbar")).toBeVisible();
    const counter = page.getByTestId("illuminate-counter");
    await expect(counter).toContainText("5 vertices");

    type Pos = { x: number; y: number };
    type Bridge = {
      getNodePosition: (k: string) => Pos | null;
      hasEdge: (k: string) => boolean;
    };
    await page.waitForFunction(() => {
      const win = window as Window & { __illuminateCanvas?: Bridge };
      return !!win.__illuminateCanvas?.hasEdge;
    });

    const readPos = (k: string): Promise<Pos | null> =>
      page.evaluate((key) => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        return win.__illuminateCanvas?.getNodePosition(key) ?? null;
      }, k);
    const hasEdge = (k: string): Promise<boolean> =>
      page.evaluate((key) => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        return win.__illuminateCanvas?.hasEdge(key) ?? false;
      }, k);

    // After the seed load (the only expansion so far) its result IS the
    // whole frame, so every seed node is rendered.
    for (const k of [
      "e2e:illum:hub",
      "e2e:illum:left",
      "e2e:illum:right",
      "e2e:illum:leftleft",
      "e2e:illum:rightright",
    ]) {
      expect(
        await readPos(k),
        `${k} should be rendered after seed`,
      ).not.toBeNull();
    }

    // Expand from `leftleft`. The latest result is now the leftleft
    // neighbourhood {leftleft, leftleftleft}, so every node outside it —
    // hub, left, right, rightright — is DELETED (#491), while the freshly
    // illuminated leftleftleft and its origin leftleft are rendered.
    await page
      .getByRole("group")
      .getByText(/List view \(5 vertices/)
      .click();
    const table = page.getByTestId("illuminate-table");
    await table
      .getByRole("button", { name: "Expand from e2e:illum:leftleft" })
      .click();
    await expect(counter).toContainText("6 vertices");

    // In the latest result → rendered.
    expect(await readPos("e2e:illum:leftleftleft")).not.toBeNull();
    expect(await readPos("e2e:illum:leftleft")).not.toBeNull();
    // Outside the latest result → deleted (not hidden).
    for (const k of [
      "e2e:illum:hub",
      "e2e:illum:left",
      "e2e:illum:right",
      "e2e:illum:rightright",
    ]) {
      expect(
        await readPos(k),
        `${k} should be deleted (outside the latest result)`,
      ).toBeNull();
    }

    // Edge delete-not-hide: the edge inside the latest result is present;
    // an edge on the deleted right branch is gone (graphology drops the
    // edges of any dropped node). Edge ids are `${tail}→${head}` (see
    // edgeIdOf in app/lib/client/usecase/illuminate/state.ts).
    expect(await hasEdge("e2e:illum:leftleft→e2e:illum:leftleftleft")).toBe(
      true,
    );
    expect(await hasEdge("e2e:illum:right→e2e:illum:rightright")).toBe(false);
  });

  test("Drag releases the node without pinning it; physics reclaims it (#491)", async ({
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

    // Sanity: the drag must NOT have been interpreted as a click — if
    // a future refactor accidentally calls expandFrom from finishDrag,
    // the counter would tick up before we ever click the disclosure.
    await expect(page.getByTestId("illuminate-counter")).toContainText(
      "5 vertices",
    );
    await expect(page.getByTestId("illuminate-counter")).toContainText(
      "1 expansion",
    );

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
  });

  test("Seed anchor is pinned, enlarged, and hop-coloured while other nodes relayout; edges show their weight (#500)", async ({
    page,
  }) => {
    const seed = encodeURIComponent("e2e:illum:hub");
    await page.goto(`/illuminate?seed=${seed}`);
    await expect(page.getByTestId("illuminate-toolbar")).toBeVisible();
    const counter = page.getByTestId("illuminate-counter");
    await expect(counter).toContainText("5 vertices");

    type Pos = { x: number; y: number };
    type Bridge = {
      getNodePosition: (k: string) => Pos | null;
      getRenderedNodeColor: (k: string) => string | null;
      getNodeSize: (k: string) => number | null;
      getEdgeLabel: (k: string) => string | null;
      isNodeFixed: (k: string) => boolean;
      setLayoutPaused: (paused: boolean) => void;
      settleLayout: (maxTicks?: number) => number;
    };
    // `getNodeSize` + `getEdgeLabel` are the #500 additions to the
    // bridge; waiting on the former proves the new bridge is installed.
    await page.waitForFunction(() => {
      const win = window as Window & { __illuminateCanvas?: Bridge };
      return !!win.__illuminateCanvas?.getNodeSize;
    });

    const readPos = (k: string): Promise<Pos | null> =>
      page.evaluate((key) => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        return win.__illuminateCanvas?.getNodePosition(key) ?? null;
      }, k);
    const readColor = (k: string): Promise<string | null> =>
      page.evaluate((key) => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        return win.__illuminateCanvas?.getRenderedNodeColor(key) ?? null;
      }, k);
    const readSize = (k: string): Promise<number | null> =>
      page.evaluate((key) => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        return win.__illuminateCanvas?.getNodeSize(key) ?? null;
      }, k);
    const readEdgeLabel = (k: string): Promise<string | null> =>
      page.evaluate((key) => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        return win.__illuminateCanvas?.getEdgeLabel(key) ?? null;
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
    const settle = (): Promise<number> =>
      page.evaluate(() => {
        const win = window as Window & { __illuminateCanvas?: Bridge };
        return win.__illuminateCanvas?.settleLayout() ?? 0;
      });

    // The seed echoed by `?seed=` is the latest expansion origin, so it
    // is THE seed anchor (都度Seed). These fixtures carry no TTL and we
    // never hover, so the node reducer is an identity pass-through —
    // `getRenderedNodeColor` returns the stamped colour verbatim.
    const SEED = "e2e:illum:hub";

    // Feature B: the seed is the ONLY pinned node …
    expect(await isFixed(SEED), "seed must be pinned").toBe(true);
    // … and, as the hop-0 origin, renders on the warm red end of the
    // #500 red→blue hop ramp. Dropping the separate orange accent, this
    // is now what colour-distinguishes the seed (plus its size + pin).
    const HOP0_RED = "#d13438";
    const HOP1_VIOLET = "#8764b8";
    const HOP2_BLUE = "#0078d4";
    expect(
      await readColor(SEED),
      "seed must render as the hop-0 red origin",
    ).toBe(HOP0_RED);

    // Feature C: the seed is strictly larger than every other node.
    const seedSize = await readSize(SEED);
    expect(seedSize).not.toBeNull();

    const others = [
      "e2e:illum:left",
      "e2e:illum:right",
      "e2e:illum:leftleft",
      "e2e:illum:rightright",
    ] as const;
    const otherSizes: number[] = [];
    for (const k of others) {
      expect(await isFixed(k), `${k} must not be pinned`).toBe(false);
      expect(
        await readColor(k),
        `${k} must not render as the hop-0 red origin`,
      ).not.toBe(HOP0_RED);
      const s = await readSize(k);
      expect(s, `${k} should have a size`).not.toBeNull();
      otherSizes.push(s!);
    }
    for (const s of otherSizes) {
      expect(
        seedSize!,
        "seed must be larger than every other node",
      ).toBeGreaterThan(s);
    }
    // Feature C (uniformity): all non-seed nodes share ONE size.
    expect(
      new Set(otherSizes).size,
      "non-seed nodes must all be the same size",
    ).toBe(1);

    // The core #500 colour acceptance: origin / 1 hop / 2 hops are three
    // obviously-different colours (red → violet → blue), not three shades
    // of one blue. left/right are 1 hop from the hub; leftleft/rightright
    // are 2 hops.
    expect(await readColor("e2e:illum:left")).toBe(HOP1_VIOLET);
    expect(await readColor("e2e:illum:right")).toBe(HOP1_VIOLET);
    expect(await readColor("e2e:illum:leftleft")).toBe(HOP2_BLUE);
    expect(await readColor("e2e:illum:rightright")).toBe(HOP2_BLUE);
    expect(
      new Set([HOP0_RED, HOP1_VIOLET, HOP2_BLUE]).size,
      "origin/1hop/2hop must be three distinct hues",
    ).toBe(3);

    // Feature D: each edge renders its weight as the on-edge label.
    // Edge ids follow `${tail}→${head}` (edgeIdOf); integer weights
    // render with no decimal point (see formatEdgeWeight).
    expect(await readEdgeLabel("e2e:illum:hub→e2e:illum:left")).toBe("1");
    expect(await readEdgeLabel("e2e:illum:hub→e2e:illum:right")).toBe("3");
    expect(await readEdgeLabel("e2e:illum:right→e2e:illum:rightright")).toBe(
      "2",
    );

    // Features A + B in motion. Expanding from `left` makes `left` the
    // new seed; its 2-hop result is {left, leftleft, leftleftleft}.
    // `leftleft` SURVIVES the frame but is NOT the new seed, so it is
    // free to move (Feature A — 既存ノードも動いていい); `left` is the
    // new seed, so it is pinned and must not move (Feature B —
    // これだけは動かない).
    await pause(true);
    await page
      .getByRole("group")
      .getByText(/List view \(5 vertices/)
      .click();
    const table = page.getByTestId("illuminate-table");
    await table
      .getByRole("button", { name: "Expand from e2e:illum:left", exact: true })
      .click();
    await expect(counter).toContainText("6 vertices");

    const NEW_SEED = "e2e:illum:left";
    const SURVIVOR = "e2e:illum:leftleft";

    // The accent + pin + size move with the seed identity to `left`.
    expect(await isFixed(NEW_SEED), "new seed must be pinned").toBe(true);
    expect(await isFixed(SURVIVOR), "non-seed survivor must be free").toBe(
      false,
    );
    // The red hop-0 origin colour follows the seed identity to `left`;
    // the survivor `leftleft` is now 1 hop from the new seed, so it
    // recolours to the violet 1-hop tier (and is NOT the red origin).
    expect(await readColor(NEW_SEED), "new seed must be the hop-0 red").toBe(
      HOP0_RED,
    );
    expect(
      await readColor(SURVIVOR),
      "survivor must not wear the hop-0 red origin colour",
    ).not.toBe(HOP0_RED);
    const newSeedSize = await readSize(NEW_SEED);
    const survivorSize = await readSize(SURVIVOR);
    expect(newSeedSize).not.toBeNull();
    expect(survivorSize).not.toBeNull();
    expect(newSeedSize!).toBeGreaterThan(survivorSize!);

    // Drive the (paused) simulation to rest by hand: the pinned seed
    // holds its EXACT coordinates while the springs relax the non-seed
    // survivor away from where it started.
    const seedBefore = await readPos(NEW_SEED);
    const survivorBefore = await readPos(SURVIVOR);
    expect(seedBefore, "new seed should have a position").not.toBeNull();
    expect(survivorBefore, "survivor should have a position").not.toBeNull();
    await settle();
    const seedAfter = await readPos(NEW_SEED);
    const survivorAfter = await readPos(SURVIVOR);
    expect(seedAfter, "new seed vanished").not.toBeNull();
    expect(survivorAfter, "survivor vanished").not.toBeNull();

    // Feature B: pinned seed is immovable — exact equality after settle
    // (d3-force overwrites x/y with fx/fy every tick).
    expect(seedAfter!.x, "pinned seed x drifted").toBe(seedBefore!.x);
    expect(seedAfter!.y, "pinned seed y drifted").toBe(seedBefore!.y);

    // Feature A: the non-seed survivor is repositioned by the layout.
    const survivorMove = Math.hypot(
      survivorAfter!.x - survivorBefore!.x,
      survivorAfter!.y - survivorBefore!.y,
    );
    expect(
      survivorMove,
      "non-seed survivor should be relaxed by the layout, not frozen",
    ).toBeGreaterThan(1);
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

  test("Hop-distance coloring separates each ring, then recolors to the latest result after an expansion (#460, #491)", async ({
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

    // === Phase 2: expand from leftleft — render only the latest result ===
    // Per #491 the canvas renders ONLY the most recent expansion's
    // result and DELETES everything else. Expanding from leftleft
    // (step=2) yields the result {leftleft, leftleftleft}; the previous
    // frame's hub/left/right/rightright are dropped from graphology even
    // though they stay in the accumulator (the counter still totals 6).
    //
    // Hop distances are computed over the full accumulator with both
    // origins (hub via the URL anchor + leftleft as the new expansion),
    // but only the rendered survivors expose a distance through the
    // bridge:
    //   - leftleft is now an expansion origin → hop 0.
    //   - leftleftleft is one edge from it → hop 1.
    //   - hub/left/right/rightright are deleted → no hop (null).
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

    // The latest result renders with its own ring colours.
    expect(await readHop(leftLeftKey)).toBe(0);
    expect(await readHop(leftLeftLeftKey)).toBe(1);

    // Everything outside the latest result is gone from the canvas.
    expect(await readHop(hubKey)).toBeNull();
    expect(await readHop(leftKey)).toBeNull();
    expect(await readHop(rightKey)).toBeNull();
    expect(await readHop(rightRightKey)).toBeNull();

    // The new origin paints with the origin colour and its single
    // neighbour with the 1-hop colour — the same buckets phase 1 used.
    expect(await readNode(leftLeftKey)).toBe(hubColor);
    expect(await readNode(leftLeftLeftKey)).toBe(leftColor);

    // The legend recomputes to the rendered set: origin + 1-hop only.
    // The earlier 2-hop row disappears because its only member
    // (rightright) was deleted from the canvas.
    await expect(page.getByTestId("illuminate-legend-origin")).toBeVisible();
    await expect(page.getByTestId("illuminate-legend-1hop")).toBeVisible();
    await expect(page.getByTestId("illuminate-legend-2hop")).toHaveCount(0);
    await expect(page.getByTestId("illuminate-legend-far")).toHaveCount(0);
    await expect(page.getByTestId("illuminate-legend-unreachable")).toHaveCount(
      0,
    );
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
      settleLayout: (maxTicks?: number) => number;
    };
    await page.waitForFunction(() => {
      const win = window as Window & { __illuminateCanvas?: Bridge };
      return !!win.__illuminateCanvas?.cameraState;
    });

    // #483: the additive expansion kicks off a continuous d3-force
    // layout animation. Settle it synchronously so `leftleft` stops
    // moving before we pan — otherwise the camera animates toward a
    // position the still-easing node has already left and never lands
    // within tolerance. In real use the layout cools within ~1s; the
    // test just races it.
    await page.evaluate(() => {
      const win = window as Window & { __illuminateCanvas?: Bridge };
      win.__illuminateCanvas?.settleLayout();
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
