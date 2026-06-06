import { reactRouter } from "@react-router/dev/vite";
import { defineConfig } from "vite";
import tsconfigPaths from "vite-tsconfig-paths";

export default defineConfig({
  server: {
    port: 5173,
  },
  ssr: {
    // @fluentui/react-icons has barrel exports that omit `.js` extensions and
    // therefore fail Node's strict ESM resolver when used in the SSR /
    // pre-renderer environment. Bundling them keeps the import graph internal
    // to Vite.
    noExternal: ["@fluentui/react-icons", "@fluentui/react-components"],
  },
  plugins: [tsconfigPaths(), reactRouter()],
});
