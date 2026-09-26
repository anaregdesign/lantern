import { describe, expect, test } from "bun:test";

import { assertPackageArchive, assertReceiptPackageExports } from "../scripts/package-archive.mjs";
import * as nodeSDK from "../src/index.js";
import * as webSDK from "../src/web.js";

const manifest = {
  name: "lantern-sdk",
  version: "0.12.0",
  type: "module",
  files: ["dist", "README.md", "LICENSE"],
  engines: { node: ">=20" },
  main: "./dist/index.cjs",
  module: "./dist/index.js",
  types: "./dist/index.d.ts",
  exports: {
    ".": {
      types: "./dist/index.d.ts",
      import: "./dist/index.js",
      require: "./dist/index.cjs",
    },
    "./web": {
      types: "./dist/web.d.ts",
      import: "./dist/web.js",
      require: "./dist/web.cjs",
    },
  },
};

const entries = [
  "package/package.json",
  "package/README.md",
  "package/LICENSE",
  ...Object.values(manifest.exports).flatMap((conditions) =>
    Object.values(conditions).map((target) => `package/${target.slice(2)}`),
  ),
];

describe("npm pack candidate validation", () => {
  test("accepts the public surface declared by the manifest", () => {
    expect(() => assertPackageArchive(manifest, entries)).not.toThrow();
  });

  test("rejects an omitted export condition or subpath", () => {
    const noTypes = {
      ...manifest,
      exports: {
        ...manifest.exports,
        "./web": {
          import: manifest.exports["./web"].import,
          require: manifest.exports["./web"].require,
        },
      },
    };
    expect(() => assertPackageArchive(noTypes, entries)).toThrow(
      'missing package.json export "./web" condition "types"',
    );

    const noWeb = { ...manifest, exports: { ".": manifest.exports["."] } };
    expect(() => assertPackageArchive(noWeb, entries)).toThrow(
      'missing package.json export "./web"',
    );
  });

  test("rejects missing exported files and public documentation", () => {
    expect(() =>
      assertPackageArchive(
        manifest,
        entries.filter((file) => file !== "package/dist/web.d.ts"),
      ),
    ).toThrow('missing export "./web" condition "types" target in npm pack: dist/web.d.ts');
    expect(() =>
      assertPackageArchive(
        manifest,
        entries.filter((file) => file !== "package/dist/index.cjs"),
      ),
    ).toThrow('missing export "." condition "require" target in npm pack: dist/index.cjs');
    expect(() =>
      assertPackageArchive(
        manifest,
        entries.filter((file) => file !== "package/LICENSE"),
      ),
    ).toThrow("missing public npm pack file: LICENSE");
  });

  test.each([
    "package/src/client.ts",
    "package/test/client.test.ts",
    "package/.env.production",
    "package/dist/credentials.pem",
    "package/dist/private-key.json",
    "package/dist/.ssh/id_ed25519",
  ])("rejects forbidden repo-only or secret file %s", (file) => {
    expect(() => assertPackageArchive(manifest, [...entries, file])).toThrow(
      `forbidden npm pack entry: ${file.slice("package/".length)}`,
    );
  });

  test("rejects other repository files outside the public selection", () => {
    expect(() => assertPackageArchive(manifest, [...entries, "package/tsconfig.json"])).toThrow(
      "unexpected npm pack entry: tsconfig.json",
    );
  });

  test("rejects an expanded files selection even when npm excludes the extra path", () => {
    expect(() =>
      assertPackageArchive({ ...manifest, files: [...manifest.files, "src"] }, entries),
    ).toThrow('package.json files disallows "src"');
  });

  test("keeps the Node floor and root declaration target aligned with exports", () => {
    expect(() => assertPackageArchive({ ...manifest, engines: { node: ">=22" } }, entries)).toThrow(
      'package.json engines.node must be ">=20"',
    );
    expect(() => assertPackageArchive({ ...manifest, types: "./dist/web.d.ts" }, entries)).toThrow(
      'package.json types must match export "." condition "types"',
    );
  });

  test("rejects a package identity or version that differs from the release tag", () => {
    expect(() => assertPackageArchive({ ...manifest, name: "other-sdk" }, entries)).toThrow(
      'npm pack package name must be "lantern-sdk"',
    );
    expect(() => assertPackageArchive({ ...manifest, version: "" }, entries)).toThrow(
      "npm pack package version is required",
    );
    for (const ref of [undefined, "refs/pull/42/merge", "refs/heads/main"]) {
      expect(() => assertPackageArchive(manifest, entries, ref)).not.toThrow();
    }
    expect(() => assertPackageArchive(manifest, entries, "refs/tags/sdks/node/v0.11.0")).toThrow(
      "npm pack version 0.12.0 does not match release tag refs/tags/sdks/node/v0.11.0",
    );
    expect(() =>
      assertPackageArchive(manifest, entries, "refs/tags/sdks/node/v0.12.0"),
    ).not.toThrow();
  });

  test("exposes receipt APIs from both Node and web entrypoints", () => {
    expect(() => assertReceiptPackageExports(nodeSDK, "node")).not.toThrow();
    expect(() => assertReceiptPackageExports(webSDK, "web")).not.toThrow();
  });

  test("rejects missing receipt exports and class methods", () => {
    expect(() =>
      assertReceiptPackageExports({ ...nodeSDK, mintReceiptOperationContext: undefined }, "node"),
    ).toThrow("missing function export mintReceiptOperationContext");
    expect(() =>
      assertReceiptPackageExports({ ...webSDK, CONTRIB_ID_BYTES: undefined }, "web"),
    ).toThrow("expected CONTRIB_ID_BYTES=24");
    expect(() =>
      assertReceiptPackageExports({ ...nodeSDK, RECEIPT_OPERATION_ID_BYTES: 48 }, "node"),
    ).toThrow("expected RECEIPT_OPERATION_ID_BYTES=49");

    function MissingStatuses() {}
    Object.setPrototypeOf(MissingStatuses.prototype, nodeSDK.Lantern.prototype);
    Object.defineProperty(MissingStatuses.prototype, "getReceiptStatuses", { value: undefined });
    expect(() =>
      assertReceiptPackageExports({ ...nodeSDK, Lantern: MissingStatuses }, "node"),
    ).toThrow("missing Lantern.getReceiptStatuses");
  });
});
