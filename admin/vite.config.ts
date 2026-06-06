import { reactRouter } from "@react-router/dev/vite";
import { defineConfig } from "vite";
import tsconfigPaths from "vite-tsconfig-paths";

export default defineConfig({
  server: {
    port: 5173,
  },
  ssr: {
    // SPA mode (`ssr: false`) still spins up a Node-side pre-renderer to emit
    // `build/client/index.html`. Several deps in our graph (Fluent UI v9,
    // Griffel, stylis, …) ship dual CJS/ESM entry points whose interop layer
    // breaks under Node's strict ESM resolver when installers like bun pick a
    // different entry than pnpm did. Bundling every dep into the SSR build
    // keeps imports internal to Vite/Rollup and sidesteps every installer-
    // specific resolver quirk in one go. Safe because nothing about the
    // pre-rendered output ships to the browser at runtime.
    noExternal: true,
  },
  plugins: [tsconfigPaths(), reactRouter()],
});
