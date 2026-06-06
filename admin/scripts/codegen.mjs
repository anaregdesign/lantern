#!/usr/bin/env bun
// Codegen: convert pb/openapiv2/graph/v1/graph.swagger.json (OpenAPI 2.0) to
// OpenAPI 3.0 in a temp file, then emit TypeScript types via openapi-typescript.
// Output: app/lib/client/infrastructure/api/lantern-api.gen.ts
//
// Regenerate with: bun run codegen

import { readFile, writeFile, mkdir, unlink } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { tmpdir } from "node:os";
import openapiTS, { astToString } from "openapi-typescript";
import swagger2openapi from "swagger2openapi";

const __dirname = dirname(fileURLToPath(import.meta.url));
const adminRoot = resolve(__dirname, "..");
const repoRoot = resolve(adminRoot, "..");

const swaggerPath = resolve(
  repoRoot,
  "pb/openapiv2/graph/v1/graph.swagger.json",
);
const outPath = resolve(
  adminRoot,
  "app/lib/client/infrastructure/api/lantern-api.gen.ts",
);

const HEADER = `/**
 * Generated OpenAPI types for the Lantern gateway.
 *
 * Source: pb/openapiv2/graph/v1/graph.swagger.json
 * Regenerate with \`bun run codegen\`. Do not edit by hand.
 */

`;

async function main() {
  const swaggerRaw = await readFile(swaggerPath, "utf8");
  const swagger = JSON.parse(swaggerRaw);
  const converted = await new Promise((resolveP, rejectP) => {
    swagger2openapi.convertObj(swagger, { patch: true }, (err, result) => {
      if (err) {
        rejectP(err);
      } else {
        resolveP(result.openapi);
      }
    });
  });

  const tmpFile = resolve(tmpdir(), `lantern-openapi-${process.pid}.json`);
  await writeFile(tmpFile, JSON.stringify(converted), "utf8");

  try {
    const ast = await openapiTS(new URL(`file://${tmpFile}`));
    const body = astToString(ast);
    await mkdir(dirname(outPath), { recursive: true });
    await writeFile(outPath, HEADER + body, "utf8");
    console.log(`Wrote ${outPath}`);
  } finally {
    await unlink(tmpFile).catch(() => {});
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
