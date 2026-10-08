import { chromium, expect, test } from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { securityUI } from "./helpers";

type OwnedBrowserConnection = {
  isConnected(): boolean;
  newBrowserCDPSession(): Promise<{
    send(method: "Browser.close"): Promise<unknown>;
  }>;
  close(): Promise<void>;
};

async function closeOwnedChromium(
  child: ChildProcess,
  exited: Promise<void>,
  profile: string,
  browser?: OwnedBrowserConnection,
) {
  const exitedWithin = async (milliseconds: number) => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {
      return await Promise.race([
        exited.then(() => true),
        new Promise<boolean>((resolve) => {
          timer = setTimeout(() => resolve(false), milliseconds);
        }),
      ]);
    } finally {
      clearTimeout(timer);
    }
  };
  const signalOwnedBrowser = (signal: "SIGTERM" | "SIGKILL") => {
    if (!child.pid) return;
    try {
      if (process.platform === "win32") child.kill(signal);
      else process.kill(-child.pid, signal);
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "ESRCH") throw error;
    }
  };
  try {
    // Closing a CDP attachment only disconnects it. Ask the owned browser
    // to shut down and flush its profile before removing that directory.
    if (browser?.isConnected()) {
      await Promise.race([
        browser
          .newBrowserCDPSession()
          .then((session) => session.send("Browser.close")),
        exitedWithin(5_000).then((stopped) => {
          if (!stopped) throw new Error("Owned Chromium close timed out");
        }),
      ]);
    }
  } finally {
    if (!(await exitedWithin(5_000))) signalOwnedBrowser("SIGTERM");
    if (!(await exitedWithin(2_000))) signalOwnedBrowser("SIGKILL");
    await exited;
    // Stop any remaining children in the group created by this spawn.
    signalOwnedBrowser("SIGTERM");
    await browser?.close();
    rmSync(profile, {
      recursive: true,
      force: true,
      maxRetries: 5,
      retryDelay: 100,
    });
  }
}

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
    { stdio: "ignore", detached: true },
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
    await closeOwnedChromium(child, exited, profile, browser);
  }
});

// A real child owns the profile writer. SIGTERM loses its final write;
// the explicit browser-close request flushes it before process exit.
// This keeps shutdown ordering deterministic without relying on an rmdir race.
test("owned Chromium cleanup waits for the profile writer to flush", async () => {
  const root = mkdtempSync(join(tmpdir(), "lantern-visibility-shutdown-"));
  const profile = join(root, "profile");
  const proof = join(root, "flush-proof");
  mkdirSync(profile);
  const child = spawn(
    process.execPath,
    [
      "-e",
      `
        const { writeFileSync } = require("node:fs");
        const { join } = require("node:path");
        const [profile, proof] = process.argv.slice(1);
        writeFileSync(join(profile, "Preferences"), "pending");
        process.on("message", (method) => {
          if (method !== "Browser.close") process.exit(1);
          writeFileSync(join(profile, "Preferences"), "flushed");
          writeFileSync(proof, "flushed");
          process.disconnect();
          process.exit(0);
        });
        process.send("ready");
      `,
      profile,
      proof,
    ],
    { detached: true, stdio: ["ignore", "ignore", "ignore", "ipc"] },
  );
  const exited = new Promise<void>((resolve) =>
    child.once("exit", () => resolve()),
  );
  const ready = new Promise<void>((resolve, reject) => {
    child.once("message", () => resolve());
    child.once("error", reject);
  });
  const connection: OwnedBrowserConnection = {
    isConnected: () => true,
    newBrowserCDPSession: async () => ({
      send: async (method) => {
        child.send(method);
      },
    }),
    close: async () => {},
  };
  try {
    await ready;
    await closeOwnedChromium(child, exited, profile, connection);
    expect(existsSync(proof)).toBe(true);
    expect(readFileSync(proof, "utf8")).toBe("flushed");
    expect(existsSync(profile)).toBe(false);
    expect(child.exitCode).toBe(0);
  } finally {
    await closeOwnedChromium(child, exited, profile);
    rmSync(root, { recursive: true, force: true });
  }
});
