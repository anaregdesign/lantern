// @ts-check
import eslint from "@eslint/js";
import tseslint from "typescript-eslint";

export default tseslint.config(
  {
    ignores: [
      "dist/**",
      "node_modules/**",
      // Legacy ts-proto stubs consumed by src/client.ts (the
      // grpc-js Lantern class). Removed wholesale in #347 / #342.
      "src/generated/**",
      // protobuf-es v2 stubs consumed by src/connect-client.ts (the
      // additive LanternConnect class). Both directories are pure
      // codegen output — regenerate with `bun run codegen`.
      "src/gen/**",
    ],
  },
  eslint.configs.recommended,
  ...tseslint.configs.recommended,
  {
    languageOptions: {
      parserOptions: {
        ecmaVersion: 2022,
        sourceType: "module",
      },
    },
    rules: {
      "@typescript-eslint/no-explicit-any": "off",
      "@typescript-eslint/no-unused-vars": [
        "error",
        { argsIgnorePattern: "^_", varsIgnorePattern: "^_" },
      ],
    },
  },
);
