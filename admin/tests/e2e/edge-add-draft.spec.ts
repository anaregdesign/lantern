import { expect, test, type Request } from "@playwright/test";
import { CONNECT_URL, STORAGE_KEY, connectCall, putVertices } from "./helpers";

for (const width of [1280, 390]) {
  for (const completion of ["before Add", "after Add"] as const) {
    test(`an Add draft survives the initial Edge read ${completion} at ${width}px`, async ({
      page,
    }, testInfo) => {
      await page.setViewportSize({ width, height: 900 });
      await page.addInitScript(
        ({ key, value }) => localStorage.setItem(key, value),
        { key: STORAGE_KEY, value: CONNECT_URL },
      );
      const prefix = `e2e:add-draft:${width}:${completion.replaceAll(" ", "-")}`;
      const tail = prefix + ":tail",
        head = prefix + ":head";
      await putVertices([{ key: tail }, { key: head }]);
      await connectCall("PutEdges", { edges: [{ tail, head, weight: 2 }] });

      let initialReady!: () => void, releaseInitial!: () => void;
      const ready = new Promise<void>((resolve) => {
        initialReady = resolve;
      });
      const release = new Promise<void>((resolve) => {
        releaseInitial = resolve;
      });
      let held = false,
        adds = 0,
        initialRequest: Request | undefined;
      page.on("request", (request) => {
        if (request.url().endsWith("/AddEdges") && request.method() === "POST")
          adds++;
      });
      await page.route("**/graph.v1.LanternService/GetEdge", async (route) => {
        if (held) {
          await route.continue();
          return;
        }
        held = true;
        initialRequest = route.request();
        // Deliver the actual Server response later; do not manufacture an Edge.
        const response = await route.fetch();
        expect(response.status()).toBe(200);
        expect((await response.json()).edge.weight).toBe(2);
        initialReady();
        await release;
        await route.fulfill({ response });
      });
      try {
        await page.goto(
          `/edges/${encodeURIComponent(tail)}/${encodeURIComponent(head)}`,
        );
        await ready;
        await expect(page.getByTestId("edge-detail-loading")).toBeVisible();
        const weight = page.getByTestId("edge-add-weight");
        const ttl = page
          .getByTestId("edge-form-add")
          .getByRole("radio", { name: "24 hours", exact: true });
        await weight.fill("7");
        await ttl.check();
        const initialResponse = page.waitForResponse(
          (response) => response.request() === initialRequest,
        );
        if (completion === "before Add") {
          releaseInitial();
          await (await initialResponse).finished();
          await expect(page.getByTestId("edge-detail-loading")).toHaveCount(0);
          await expect(weight).toHaveValue("7");
          await expect(ttl).toBeChecked();
        }
        const sent = page.waitForRequest(
          (request) =>
            request.url().endsWith("/AddEdges") && request.method() === "POST",
        );
        const accepted = page.waitForResponse(
          (response) =>
            response.url().endsWith("/AddEdges") &&
            response.request().method() === "POST",
        );
        const submittedAt = Date.now();
        await page.getByTestId("edge-add-submit").click();
        const request = (await sent).postDataJSON() as {
          edges: Array<{
            tail: string;
            head: string;
            weight: number;
            expiration: string;
          }>;
        };
        expect(request.edges).toHaveLength(1);
        expect(request.edges[0]).toMatchObject({ tail, head, weight: 7 });
        const expiresAt = Date.parse(request.edges[0].expiration);
        expect(expiresAt - submittedAt).toBeGreaterThanOrEqual(
          24 * 60 * 60 * 1000 - 1000,
        );
        expect(expiresAt - submittedAt).toBeLessThan(
          24 * 60 * 60 * 1000 + 60_000,
        );
        expect((await accepted).status()).toBe(200);
        await expect(page.getByTestId("edge-current-weight")).toHaveText("9");
        if (completion === "after Add") {
          // A new draft and the accepted result must survive the older read.
          await weight.fill("11");
          releaseInitial();
          await (await initialResponse).finished();
          await page.evaluate(
            () =>
              new Promise<void>((resolve) =>
                requestAnimationFrame(() =>
                  requestAnimationFrame(() => resolve()),
                ),
              ),
          );
          await expect(weight).toHaveValue("11");
          await expect(page.getByTestId("edge-current-weight")).toHaveText("9");
        }
        expect(adds).toBe(1);
        const actual = (await connectCall("GetEdge", { tail, head })) as {
          edge: { weight: number };
        };
        expect(actual.edge.weight).toBe(9);
        await page.screenshot({
          path: testInfo.outputPath(
            `add-draft-${width}-${completion.replaceAll(" ", "-")}.png`,
          ),
          fullPage: true,
        });
      } finally {
        releaseInitial();
      }
    });
  }
}
