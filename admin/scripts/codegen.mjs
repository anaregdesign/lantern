#!/usr/bin/env bun
// Codegen: generate protobuf-es message classes + Connect-Web client
// boilerplate from the canonical .proto files under repo `proto/`.
// Output: app/lib/api/gen/{graph,replication}/...
//
// Regenerate with: bun run codegen
//
// The bun runtime invokes `buf` via npx so the build does not need a
// global buf install — the bufbuild org publishes the CLI as
// @bufbuild/buf. The pinned version matches the rest of the repo (see
// root buf.gen.yaml).

import { spawn } from "node:child_process";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { rm, mkdir } from "node:fs/promises";

const __dirname = dirname(fileURLToPath(import.meta.url));
const adminRoot = resolve(__dirname, "..");
const genDir = resolve(adminRoot, "app/lib/api/gen");

const BUF_VERSION = "1.70.0";

async function main() {
  // Wipe the gen directory before re-emitting so deleted RPCs don't leave
  // stale stubs behind. Re-create the directory afterwards because buf
  // expects the output root to exist.
  await rm(genDir, { recursive: true, force: true });
  await mkdir(genDir, { recursive: true });

  await run("npx", [
    "--yes",
    `@bufbuild/buf@${BUF_VERSION}`,
    "generate",
    "--template",
    resolve(adminRoot, "buf.gen.yaml"),
  ]);
  console.log(`Wrote Connect-Web stubs to ${genDir}`);
}

function run(cmd, args) {
  return new Promise((res, rej) => {
    const child = spawn(cmd, args, {
      cwd: adminRoot,
      stdio: "inherit",
      env: process.env,
    });
    child.on("error", rej);
    child.on("exit", (code) => {
      if (code === 0) {
        res();
      } else {
        rej(new Error(`${cmd} ${args.join(" ")} exited ${code}`));
      }
    });
  });
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
