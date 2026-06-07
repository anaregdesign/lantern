import { defineConfig } from "tsup";

// Two entrypoints, two bundles:
//
//   - `dist/index.{js,cjs}`  — Node entry (`lantern-sdk`). Pulls in
//     `@connectrpc/connect-node` via `transport-node.ts`.
//   - `dist/web.{js,cjs}`    — Browser entry (`lantern-sdk/web`). Pulls
//     in `@connectrpc/connect-web` via `transport-web.ts`. Verified by
//     `test/web-bundle.test.ts` to NOT reference `@connectrpc/connect-node`.
//
// tsup's `splitting: false` produces a single self-contained chunk per
// entrypoint so a bundler can tree-shake at the entry boundary
// (importing `lantern-sdk/web` will not drag in `dist/index.js`).
export default defineConfig({
  entry: { index: "src/index.ts", web: "src/web.ts" },
  format: ["esm", "cjs"],
  dts: true,
  sourcemap: true,
  clean: true,
  target: "node20",
  splitting: false,
  shims: false,
  noExternal: [],
});
