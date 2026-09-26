import { execFileSync } from "node:child_process";
import console from "node:console";
import {
  copyFileSync,
  existsSync,
  mkdtempSync,
  readdirSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { join } from "node:path";
import process from "node:process";
import { fileURLToPath, pathToFileURL, URL } from "node:url";

import { assertPackageArchive, assertReceiptPackageExports } from "./package-archive.mjs";

const packageRoot = fileURLToPath(new URL("../", import.meta.url));
const destination = mkdtempSync(join(tmpdir(), "lantern-npm-pack-"));
try {
  execFileSync("npm", ["pack", "--pack-destination", destination, "--json"], {
    cwd: packageRoot,
    stdio: ["ignore", "ignore", "inherit"],
  });
  // npm's JSON result shape varies by version; inspect the tarball it actually wrote.
  const tarballs = readdirSync(destination).filter((file) => file.endsWith(".tgz"));
  if (tarballs.length !== 1) {
    throw new Error(`npm pack produced ${tarballs.length} tarballs; expected exactly one`);
  }
  const archive = join(destination, tarballs[0]);
  const entries = execFileSync("tar", ["-tzf", archive], { encoding: "utf8" })
    .split(/\r?\n/)
    .filter(Boolean);
  const manifest = JSON.parse(
    execFileSync("tar", ["-xOzf", archive, "package/package.json"], { encoding: "utf8" }),
  );
  assertPackageArchive(manifest, entries, process.env.GITHUB_REF);

  const dependencies = join(packageRoot, "node_modules");
  if (!existsSync(dependencies)) {
    throw new Error("npm pack receipt checks need the Bun dependencies; run bun install first");
  }
  execFileSync("tar", ["-xzf", archive, "-C", destination]);
  const extracted = join(destination, "package");
  symlinkSync(
    dependencies,
    join(extracted, "node_modules"),
    process.platform === "win32" ? "junction" : "dir",
  );

  const esmProbe = join(extracted, ".verify-exports.mjs");
  writeFileSync(
    esmProbe,
    'export * as node from "lantern-sdk";\nexport * as web from "lantern-sdk/web";\n',
  );
  const esm = await import(pathToFileURL(esmProbe).href);
  assertReceiptPackageExports(esm.node, "node");
  assertReceiptPackageExports(esm.web, "web");

  const requireFromPackage = createRequire(join(extracted, "package.json"));
  assertReceiptPackageExports(requireFromPackage("lantern-sdk"), "node");
  assertReceiptPackageExports(requireFromPackage("lantern-sdk/web"), "web");

  const typeProbe = join(extracted, ".verify-receipt-types.mts");
  copyFileSync(new URL("./package-receipt-types.mts", import.meta.url), typeProbe);
  const cjsTypeProbe = join(extracted, ".verify-receipt-types.cts");
  copyFileSync(new URL("./package-receipt-types.cts", import.meta.url), cjsTypeProbe);
  execFileSync(
    join(dependencies, ".bin", "tsc"),
    [
      "--noEmit",
      "--strict",
      "--skipLibCheck",
      "--target",
      "es2022",
      "--module",
      "nodenext",
      "--moduleResolution",
      "nodenext",
      "--pretty",
      "false",
      typeProbe,
      cjsTypeProbe,
    ],
    { cwd: extracted, stdio: "inherit" },
  );
  console.log(
    `Verified npm pack candidate: ${manifest.name}@${manifest.version} (${entries.length} files; Node/web ESM, CJS, and receipt types)`,
  );
} finally {
  rmSync(destination, { recursive: true, force: true });
}
