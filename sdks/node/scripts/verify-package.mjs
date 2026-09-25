import { execFileSync } from "node:child_process";
import console from "node:console";
import { mkdtempSync, readdirSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath, URL } from "node:url";

import { assertPackageArchive } from "./package-archive.mjs";

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
  assertPackageArchive(manifest, entries);
  console.log(
    `Verified npm pack candidate: ${manifest.name}@${manifest.version} (${entries.length} files)`,
  );
} finally {
  rmSync(destination, { recursive: true, force: true });
}
