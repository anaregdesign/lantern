import { expect, test } from "@playwright/test";

import {
  CONNECT_URL,
  STORAGE_KEY,
  connectCall,
  deleteVerticesByPrefix,
  putEdges,
  putVertices,
} from "./helpers";

/**
 * Seeds a small graph for the Browse screen:
 *
 *   - 3 vertices under prefix `e2e:vertex:`
 *   - 1 vertex under prefix `e2e:other:` (must NOT appear when filtering)
 *   - 3 vertices under prefix `e2e:search:` for the content-search find
 *     mode (#650 folded the former /search screen into the Vertices page)
 *   - 2 edges `e2e:vertex:a → e2e:vertex:b` and `e2e:vertex:a → e2e:vertex:c`
 *
 * The seed runs against the additive Connect listener started by
 * Playwright's webServer (#337 / #339).
 */
test.beforeAll(async () => {
  await seed();
});

async function seed() {
  await putVertices([
    { key: "e2e:vertex:a", string: "alpha" },
    { key: "e2e:vertex:b", int32: 42 },
    { key: "e2e:vertex:c", bool: true },
    { key: "e2e:other:z", string: "ignored" },
    { key: "e2e:literal:%2F", string: "literal percent escape" },
    // Content-search corpus (#650). Deliberately rare tokens so a keyword
    // query matches exactly these rows and nothing else on the shared
    // server instance: `zorptangle` in two docs, `quibblefrost` in one.
    { key: "e2e:search:doc1", string: "zorptangle distributed consensus" },
    { key: "e2e:search:doc2", string: "zorptangle vector clocks" },
    { key: "e2e:search:doc3", string: "quibblefrost unrelated content" },
  ]);
  await putEdges([
    { tail: "e2e:vertex:a", head: "e2e:vertex:b", weight: 1 },
    { tail: "e2e:vertex:a", head: "e2e:vertex:c", weight: 2 },
    { tail: "e2e:other:z", head: "e2e:vertex:b", weight: 9 },
  ]);
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript(
    ({ key, value }) => {
      try {
        window.localStorage.setItem(key, value);
      } catch {
        // ignore — storage may be unavailable in private mode
      }
    },
    { key: STORAGE_KEY, value: CONNECT_URL },
  );
});

test.describe("/vertices", () => {
  test("filters by prefix and renders typed values", async ({ page }) => {
    await page.goto("/vertices");

    await page.getByTestId("vertex-prefix-input").fill("e2e:vertex:");

    const table = page.getByTestId("vertices-table");
    await expect(table).toBeVisible();
    await expect(
      table.getByRole("link", { name: "e2e:vertex:a" }),
    ).toBeVisible();
    await expect(
      table.getByRole("link", { name: "e2e:vertex:b" }),
    ).toBeVisible();
    await expect(
      table.getByRole("link", { name: "e2e:vertex:c" }),
    ).toBeVisible();

    // The ignored prefix bucket MUST NOT leak in.
    await expect(table.getByRole("link", { name: "e2e:other:z" })).toHaveCount(
      0,
    );

    // CountVerticesByPrefix should populate the count badge with at
    // least 3 (the seed) — eventually consistent because the count RPC
    // races the scan.
    await expect(page.getByTestId("vertex-count-badge")).toContainText(/\d/);
  });

  test("Illuminate action navigates to the CLI explorer seeded with the row key", async ({
    page,
  }) => {
    await page.goto("/vertices");
    await page.getByTestId("vertex-prefix-input").fill("e2e:vertex:");

    const table = page.getByTestId("vertices-table");
    const illuminate = table
      .getByRole("button", { name: /Illuminate from e2e:vertex:a/i })
      .first();
    await expect(illuminate).toBeVisible();
    await illuminate.click();
    // #651 folded Illuminate into the CLI: the row action now lands on /cli
    // with the row key as ?seed=, and the seed handoff auto-runs the
    // family-native default `bfs <key> 5 3`, so the canvas opens on that
    // command.
    await expect(page).toHaveURL(/\/cli\?seed=e2e%3Avertex%3Aa/);
    await expect(page.getByTestId("cli-canvas-panel")).toBeVisible();
    await expect(page.getByTestId("cli-canvas-panel")).toContainText(
      "bfs e2e:vertex:a 5 3",
    );
  });

  test("Illuminate handoff preserves a literal percent escape in the row key (#988)", async ({
    page,
  }) => {
    await page.goto("/vertices");
    await page.getByTestId("vertex-prefix-input").fill("e2e:literal:");
    const table = page.getByTestId("vertices-table");
    const illuminate = table.getByRole("button", {
      name: "Illuminate from e2e:literal:%2F",
    });
    await expect(illuminate).toBeVisible();
    await illuminate.click();

    // Navigation percent-encodes the literal `%` as `%25`; URLSearchParams
    // decodes it exactly once before the CLI formatter sends the original key
    // to the server.
    await expect(page).toHaveURL(/\/cli\?seed=e2e%3Aliteral%3A%252F/);
    await expect(page.getByTestId("cli-canvas-panel")).toContainText(
      "bfs e2e:literal:%2F 5 3",
    );
  });

  test("Edit action opens the vertex editor directly (parity with Edges, #652)", async ({
    page,
  }) => {
    await page.goto("/vertices");
    await page.getByTestId("vertex-prefix-input").fill("e2e:vertex:");

    const table = page.getByTestId("vertices-table");
    // A specifically-named Edit button auto-waits for the filtered row; never
    // `.first()` of the testid set, which would race the debounced prefix
    // filter and hit the alphabetically-first unfiltered row.
    const edit = table
      .getByRole("button", { name: /Edit vertex e2e:vertex:a/i })
      .first();
    await expect(edit).toBeVisible();
    await edit.click();

    // One click lands directly on the editor — no intermediate read-only hop,
    // matching the Edges row Edit affordance.
    await expect(page).toHaveURL(/\/vertices\/e2e%3Avertex%3Aa\?edit=1/);
    await expect(page.getByTestId("vertex-detail-edit")).toBeVisible();
    await expect(page.getByTestId("vertex-detail-read")).toHaveCount(0);
  });
});

test.describe("/vertices — content search (#650)", () => {
  // #650 folded the standalone /search screen into the Vertices page as a
  // second "find mode". Switching to the Content search tab swaps the prefix
  // scan for a BM25 keyword query that lands in the same table.
  test("prompts for a query before anything is typed", async ({ page }) => {
    await page.goto("/vertices");
    await page.getByRole("tab", { name: "Content search" }).click();

    await expect(page.getByTestId("search-idle")).toBeVisible();
    await expect(page.getByTestId("search-results-table")).toHaveCount(0);
    await expect(page.getByTestId("search-contract-link")).toHaveAttribute(
      "href",
      "https://github.com/anaregdesign/lantern/blob/main/docs/search.md",
    );
  });

  test("ranks the strongest content matches to the top", async ({ page }) => {
    await page.goto("/vertices");
    await page.getByRole("tab", { name: "Content search" }).click();
    await page.getByTestId("search-query-input").fill("zorptangle");

    const table = page.getByTestId("search-results-table");
    await expect(table).toBeVisible();

    // The index is substring/fuzzy (n-gram BM25), so a vertex can match on a
    // stray shared n-gram. doc1 and doc2 carry the full `zorptangle` token,
    // so they always outrank that noise and occupy the top two ranks.
    await expect(
      table.getByRole("link", { name: "e2e:search:doc1" }),
    ).toBeVisible();
    await expect(
      table.getByRole("link", { name: "e2e:search:doc2" }),
    ).toBeVisible();

    const keyLinks = table.locator("tbody a");
    await expect(keyLinks.nth(0)).toHaveText(/e2e:search:doc[12]/);
    await expect(keyLinks.nth(1)).toHaveText(/e2e:search:doc[12]/);

    // Each ranked hit shows a relevance score, and the caption summarises the
    // match count.
    await expect(page.getByTestId("search-score").first()).toContainText(
      /\d\.\d{3}/,
    );
    await expect(page.getByTestId("search-caption")).toContainText(/result/);

    // A live hit carries the same Edit affordance as a prefix-scan row, so a
    // content search can hand straight off to the editor (parity, #650/#652).
    await expect(
      table.getByRole("button", { name: /Edit vertex e2e:search:doc1/i }),
    ).toBeVisible();
  });

  test("match-mode control narrows a multi-word query from OR to AND (#892)", async ({
    page,
  }) => {
    await page.goto("/vertices");
    await page.getByRole("tab", { name: "Content search" }).click();

    // The relevance controls appear alongside the query box.
    await expect(page.getByTestId("search-options")).toBeVisible();
    await expect(page.getByTestId("search-mode")).toBeVisible();
    await expect(page.getByTestId("search-phrase")).toBeVisible();
    await expect(page.getByTestId("search-fuzzy")).toBeVisible();
    await expect(page.getByTestId("search-prefix")).toBeVisible();
    await expect(page.getByTestId("search-prefix-terms")).toBeVisible();

    // The minimum mode exposes a real positive threshold, then returning to
    // Server default removes the explicit override before the query runs.
    await page.getByTestId("search-mode").click();
    await page.getByRole("option", { name: "At least N words" }).click();
    await expect(page.getByTestId("search-min-should")).toHaveValue("2");
    await page.getByTestId("search-mode").click();
    await page.getByRole("option", { name: "Server default" }).click();

    // "zorptangle consensus": only doc1 carries both words; doc2 has
    // "zorptangle" but not "consensus".
    await page.getByTestId("search-query-input").fill("zorptangle consensus");

    const table = page.getByTestId("search-results-table");
    await expect(table).toBeVisible();
    // The e2e server's configured default is OR, so doc2 rides in on the
    // shared "zorptangle" without the admin forcing an explicit ANY mode.
    await expect(
      table.getByRole("link", { name: "e2e:search:doc2" }),
    ).toBeVisible();

    // Switch to All-words (AND): doc2, missing "consensus", drops out while
    // doc1 (both words) stays. This proves the mode flows through to the
    // server and re-runs the query.
    await page.getByTestId("search-mode").click();
    await page.getByRole("option", { name: "All words (AND)" }).click();

    await expect(
      table.getByRole("link", { name: "e2e:search:doc1" }),
    ).toBeVisible();
    await expect(
      table.getByRole("link", { name: "e2e:search:doc2" }),
    ).toHaveCount(0);
  });

  test("every content-search parameter explains itself by pointer and keyboard (#1129)", async ({
    page,
  }) => {
    await page.goto("/vertices");
    await page.getByRole("tab", { name: "Content search" }).click();

    const guidance = [
      ["search-query-help", "analyzes the words"],
      ["search-mode-help", "query words qualify"],
      ["search-prefix-help", "before ranking"],
      ["search-fuzzy-help", "edit distance"],
      ["search-prefix-terms-help", "extend a query word"],
      ["search-phrase-help", "adjacent, ordered words"],
    ] as const;

    for (const [testId, text] of guidance) {
      const trigger = page.getByTestId(testId);
      await expect(trigger).toBeVisible();
      await trigger.hover();
      await expect(page.getByRole("tooltip")).toContainText(text);
      await page.mouse.move(0, 0);
      await expect(page.getByRole("tooltip")).toHaveCount(0);
    }

    await page.getByTestId("search-mode").click();
    await page.getByRole("option", { name: "At least N words" }).click();
    const minimumHelp = page.getByTestId("search-min-should-help");
    await minimumHelp.focus();
    await expect(page.getByRole("tooltip")).toContainText(
      "distinct analyzed query words",
    );
  });

  test("Illuminate action seeds the CLI explorer with the hit key", async ({
    page,
  }) => {
    await page.goto("/vertices");
    await page.getByRole("tab", { name: "Content search" }).click();
    await page.getByTestId("search-query-input").fill("quibblefrost");

    const table = page.getByTestId("search-results-table");
    const illuminate = table
      .getByRole("button", { name: /Illuminate from e2e:search:doc3/i })
      .first();
    await expect(illuminate).toBeVisible();
    await illuminate.click();
    // #651 — content-search hit hands off to /cli, same as a prefix-scan
    // row: the seed handoff runs the family-native `bfs <key> 5 3` onto the
    // canvas.
    await expect(page).toHaveURL(/\/cli\?seed=e2e%3Asearch%3Adoc3/);
    await expect(page.getByTestId("cli-canvas-panel")).toBeVisible();
    await expect(page.getByTestId("cli-canvas-panel")).toContainText(
      "bfs e2e:search:doc3 5 3",
    );
  });
});

test.describe("/edges", () => {
  test("filters by tail prefix", async ({ page }) => {
    await page.goto("/edges");
    await page.getByTestId("edge-tail-prefix-input").fill("e2e:vertex:");

    const table = page.getByTestId("edges-table");
    await expect(table).toBeVisible();
    // Both seeded edges with tail under `e2e:vertex:` should be present.
    await expect(table.getByRole("link", { name: "e2e:vertex:b" })).toHaveCount(
      1,
    );
    await expect(table.getByRole("link", { name: "e2e:vertex:c" })).toHaveCount(
      1,
    );
    // The edge with tail `e2e:other:z` must NOT leak in.
    await expect(table.getByRole("link", { name: "e2e:other:z" })).toHaveCount(
      0,
    );
  });

  test("filters by combined tail+head prefix", async ({ page }) => {
    await page.goto("/edges");
    await page.getByTestId("edge-tail-prefix-input").fill("e2e:vertex:");
    await page.getByTestId("edge-head-prefix-input").fill("e2e:vertex:b");

    const table = page.getByTestId("edges-table");
    await expect(table).toBeVisible();
    await expect(table.getByRole("link", { name: "e2e:vertex:b" })).toHaveCount(
      1,
    );
    await expect(table.getByRole("link", { name: "e2e:vertex:c" })).toHaveCount(
      0,
    );
  });
});

// #944: with realistic key lengths (spotify-style keys run ~45+ chars) an
// un-clamped Tail cell paints its text straight across the Head column and
// Head bleeds into Weight. The key cells must truncate with an ellipsis and
// keep the full key reachable via a native `title` tooltip.
test.describe("/edges — long keys truncate instead of overlapping (#944)", () => {
  // Both endpoints are well past any column budget. If the fix regresses, the
  // tail text overflows into the Head column and the bounding-box assertion
  // below trips.
  const longTail = `e2e-long:tail:${"t".repeat(60)}`;
  const longHead = `e2e-long:head:${"h".repeat(60)}`;

  test.beforeAll(async () => {
    // Idempotent seed so a re-run (or a prior crash) can't leave a duplicate
    // e2e-long row that would break the single-row locators.
    await connectCall("DeleteEdge", { tail: longTail, head: longHead }).catch(
      () => undefined,
    );
    await putEdges([{ tail: longTail, head: longHead, weight: 1 }]);
  });

  test.afterAll(async () => {
    await connectCall("DeleteEdge", { tail: longTail, head: longHead }).catch(
      () => undefined,
    );
    // PutEdges may materialise the endpoint vertices; clear the whole prefix.
    await deleteVerticesByPrefix("e2e-long:").catch(() => undefined);
  });

  test("clamps the tail/head cells and exposes the full key via title", async ({
    page,
  }) => {
    await page.goto("/edges");
    await page.getByTestId("edge-tail-prefix-input").fill("e2e-long:");

    const table = page.getByTestId("edges-table");
    await expect(table).toBeVisible();

    const tailLink = table.getByRole("link", { name: longTail });
    const headLink = table.getByRole("link", { name: longHead });
    await expect(tailLink).toBeVisible();
    await expect(headLink).toBeVisible();

    // The full key is truncated on screen but stays reachable on hover — the
    // detail page remains the canonical full view.
    await expect(tailLink).toHaveAttribute("title", longTail);
    await expect(headLink).toHaveAttribute("title", longHead);

    // No horizontal overlap: the rendered Tail link must end before the Head
    // link begins (+1px slack for sub-pixel rounding). Without the ellipsis
    // clamp the tail text runs across the Head column and this fails.
    const tailBox = await tailLink.boundingBox();
    const headBox = await headLink.boundingBox();
    expect(tailBox).not.toBeNull();
    expect(headBox).not.toBeNull();
    if (tailBox && headBox) {
      expect(tailBox.x + tailBox.width).toBeLessThanOrEqual(headBox.x + 1);
    }
  });
});

// #657: on a phone-class viewport the fixed-column Browse tables are wider
// than the screen. They must scroll horizontally (advertised by the wrapper's
// scroll-shadow) so the right-hand Actions column stays reachable instead of
// being clipped off-screen with no affordance.
test.describe("mobile (390px) — Browse row actions stay reachable (#657)", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("/vertices Illuminate action is reachable via horizontal scroll", async ({
    page,
  }) => {
    await page.goto("/vertices");
    await page.getByTestId("vertex-prefix-input").fill("e2e:vertex:");

    const table = page.getByTestId("vertices-table");
    await expect(table).toBeVisible();

    // The table is wider than its scroll container, so there IS content
    // hidden to the right that the wrapper scrolls to reveal (and the
    // shadow advertises) — rather than the row being clipped away.
    const overflows = await table.evaluate((el) => {
      const wrap = el.parentElement;
      return wrap ? wrap.scrollWidth > wrap.clientWidth + 1 : false;
    });
    expect(overflows).toBe(true);

    // Reachable: clicking auto-scrolls the off-screen action into view and
    // navigates, proving it is not clipped out of reach.
    const illuminate = table
      .getByRole("button", { name: /Illuminate from e2e:vertex:a/i })
      .first();
    await illuminate.click();
    // #651 — the row action now lands on /cli (Illuminate folded into the
    // CLI); reachability is the point of this test, so the URL hand-off is
    // the assertion.
    await expect(page).toHaveURL(/\/cli\?seed=e2e%3Avertex%3Aa/);
  });

  test("/edges Edit action is reachable via horizontal scroll", async ({
    page,
  }) => {
    await page.goto("/edges");
    await page.getByTestId("edge-tail-prefix-input").fill("e2e:vertex:");

    const table = page.getByTestId("edges-table");
    await expect(table).toBeVisible();

    const overflows = await table.evaluate((el) => {
      const wrap = el.parentElement;
      return wrap ? wrap.scrollWidth > wrap.clientWidth + 1 : false;
    });
    expect(overflows).toBe(true);

    // Click a specific tail's Edit button (auto-waits for the filtered row),
    // not `.first()` of the testid set — the latter would race the debounced
    // filter and hit the alphabetically-first `e2e:other:z` row instead.
    const edit = table
      .getByRole("button", { name: /Edit edge e2e:vertex:a/i })
      .first();
    await edit.click();
    await expect(page).toHaveURL(/\/edges\/e2e%3Avertex%3Aa\//);
  });
});
