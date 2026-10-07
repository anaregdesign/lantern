import { chromium, expect, test } from "@playwright/test";
import { spawn } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { securityUI } from "./helpers";

// Playwright normally enables focus emulation, which keeps background pages
// visible. Attach to our own ephemeral default context without that override.
// Session responses here are SPA contract fixtures; real authority is covered
// by the production Server/IdP Connect gates, not this controlled route.
test("native hidden-to-visible resume clears protected history before revalidation", async () => {
  const profile = mkdtempSync(join(tmpdir(), "lantern-admin-visibility-"));
  const child = spawn(
    chromium.executablePath(),
    [
      "--user-data-dir=" + profile,
      "--remote-debugging-port=0",
      "--headless=new",
      "--no-sandbox",
      "--no-first-run",
      "--no-default-browser-check",
      "--disable-background-networking",
      "--disable-component-update",
      "--disable-sync",
      "about:blank",
    ],
    { stdio: "ignore" },
  );
  const exited = new Promise<void>((resolve) =>
    child.once("exit", () => resolve()),
  );
  let browser: Awaited<ReturnType<typeof chromium.connectOverCDP>> | undefined;
  try {
    await expect
      .poll(() => existsSync(join(profile, "DevToolsActivePort")), {
        timeout: 10_000,
      })
      .toBe(true);
    const [port, path] = readFileSync(
      join(profile, "DevToolsActivePort"),
      "utf8",
    )
      .trim()
      .split("\n");
    browser = await chromium.connectOverCDP(`ws://127.0.0.1:${port}${path}`, {
      noDefaults: true,
    });
    const context = browser.contexts()[0];
    const page = context.pages()[0];
    const fixture = await securityUI(page);
    await page.route(
      `${fixture.primary}/browser/graph.v1.LanternService/ScanVertices`,
      (route) =>
        route.fulfill({
          contentType: "application/json",
          body: JSON.stringify({
            vertices: [{ key: "tenant:old", string: "old protected value" }],
          }),
        }),
    );
    await page.goto(fixture.primary + "/cli");
    await page.bringToFront();
    await expect(
      page.getByRole("button", { name: "Sign out", exact: true }),
    ).toBeVisible();
    await page.waitForTimeout(200);
    await page.getByTestId("cli-input").fill("scan vertices tenant:");
    await page.getByTestId("cli-input").press("Enter");
    await expect(page.getByTestId("cli-scrollback")).toContainText(
      "old protected value",
    );
    await page.evaluate(() => {
      (window as unknown as { visibilityEvents: string[] }).visibilityEvents =
        [];
      document.addEventListener("visibilitychange", () =>
        (
          window as unknown as { visibilityEvents: string[] }
        ).visibilityEvents.push(document.visibilityState),
      );
    });
    const other = await context.newPage();
    await other.goto("data:text/html,<title>Owned background tab</title>");
    await other.bringToFront();
    await expect
      .poll(() => page.evaluate(() => document.visibilityState))
      .toBe("hidden");
    let release!: () => void;
    const barrier = new Promise<void>((resolve) => {
      release = resolve;
    });
    let requested = false;
    await page.route(fixture.primary + "/auth/session", async (route) => {
      requested = true;
      await barrier;
      await route.fulfill({
        status: 401,
        contentType: "application/json",
        body: '{"code":"unauthenticated"}',
      });
    });
    await page.bringToFront();
    await expect
      .poll(() => page.evaluate(() => document.visibilityState))
      .toBe("visible");
    await expect.poll(() => requested).toBe(true);
    await expect(page.getByTestId("cli-scrollback")).toHaveCount(0);
    const observed = await page.evaluate(
      () =>
        (window as unknown as { visibilityEvents: string[] }).visibilityEvents,
    );
    expect(observed).toContain("hidden");
    expect(observed).toContain("visible");
    release();
    await expect(
      page.getByRole("button", { name: "Sign in with Example", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText("old protected value", { exact: false }),
    ).toHaveCount(0);
  } finally {
    await browser?.close();
    child.kill("SIGTERM");
    await exited;
    rmSync(profile, { recursive: true, force: true });
  }
});
