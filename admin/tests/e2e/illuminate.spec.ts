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
});
