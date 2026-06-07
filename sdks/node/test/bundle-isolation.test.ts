/**
 * Bundle isolation guard (#409).
 *
 * The `lantern-sdk/web` subpath export is the SDK's browser entrypoint.
 * Pulling `@connectrpc/connect-node` into a browser bundle silently
 * breaks Vite/Webpack builds with Node-builtin polyfill warnings and,
 * in worst case, ships dead Node-only code to the browser at runtime.
 *
 * This test reads the emitted bundles and asserts that:
 *   - `dist/web.{js,cjs}` contains zero `require("@connectrpc/connect-node")`
 *     or `from "@connectrpc/connect-node"` statements.
 *   - `dist/index.{js,cjs}` (the Node entrypoint) contains zero
 *     `@connectrpc/connect-web` statements (saves an unused dep round-trip
 *     for Node consumers).
 *
 * The bundles must be built first (`bun run build`). The test skips
 * cleanly when the bundle is missing so a fresh checkout does not fail
 * before the user runs the build.
 */

import { describe, expect, test } from "bun:test";
import { readFileSync, existsSync } from "node:fs";
import { resolve } from "node:path";

const distRoot = resolve(import.meta.dirname, "..", "dist");

function realImportCount(bundlePath: string, pkg: string): number {
  if (!existsSync(bundlePath)) return -1;
  const src = readFileSync(bundlePath, "utf8");
  const patterns = [
    new RegExp(`require\\("${pkg}"\\)`, "g"),
    new RegExp(`from\\s+"${pkg}"`, "g"),
    new RegExp(`import\\s*\\(\\s*"${pkg}"\\s*\\)`, "g"),
  ];
  let n = 0;
  for (const p of patterns) {
    n += (src.match(p) ?? []).length;
  }
  return n;
}

describe("bundle isolation (#409)", () => {
  test("web bundle excludes @connectrpc/connect-node", () => {
    for (const bundle of ["web.js", "web.cjs"]) {
      const path = resolve(distRoot, bundle);
      const n = realImportCount(path, "@connectrpc/connect-node");
      if (n === -1) {
        // Bundle not built — skip silently. The CI script runs
        // `bun run build` before `bun test`, so a missed build only
        // affects local-dev runs (where the user just forgot to
        // build before testing).
        return;
      }
      expect(n).toBe(0);
    }
  });

  test("node bundle excludes @connectrpc/connect-web", () => {
    for (const bundle of ["index.js", "index.cjs"]) {
      const path = resolve(distRoot, bundle);
      const n = realImportCount(path, "@connectrpc/connect-web");
      if (n === -1) {
        return;
      }
      expect(n).toBe(0);
    }
  });
});
