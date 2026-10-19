import assert from "node:assert/strict";
import { Buffer } from "node:buffer";
import console from "node:console";
import { writeFile } from "node:fs/promises";
import process from "node:process";
import { pathToFileURL } from "node:url";

const registryURL = "https://registry.npmjs.org/lantern-sdk";
const versionPattern = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;
const limit = 4 << 20;

// Inventory is read-only. It does not qualify a package or grant publication.
export function summarizeRegistry(metadata, candidateVersion, sourceSHA) {
  assert.match(candidateVersion, versionPattern, "invalid candidate version");
  assert.match(sourceSHA, /^[a-f0-9]{40}$/, "invalid source SHA");
  assert.equal(metadata.name, "lantern-sdk", "wrong registry package");
  const latest = metadata["dist-tags"]?.latest;
  const published = metadata.versions?.[latest];
  assert.equal(typeof latest, "string", "missing latest registry tag");
  assert.equal(published?.name, "lantern-sdk", "invalid latest package");
  assert.equal(published.version, latest, "latest version mismatch");
  assert.match(published.dist?.shasum ?? "", /^[a-f0-9]{40}$/, "missing registry SHA-1");
  assert.match(
    published.dist?.integrity ?? "",
    /^sha512-[A-Za-z0-9+/]+=*$/,
    "missing registry integrity",
  );
  return {
    schema: 1,
    package: "lantern-sdk",
    candidateVersion,
    sourceSHA,
    registryURL,
    latestPublishedVersion: latest,
    latestPublishedShasum: published.dist.shasum,
    latestPublishedIntegrity: published.dist.integrity,
    candidateAlreadyPublished: Object.hasOwn(metadata.versions, candidateVersion),
    qualification: "inventory only; tag, archive and provenance gates remain required",
  };
}

export async function readRegistry(fetcher = globalThis.fetch) {
  const response = await fetcher(registryURL, {
    headers: { Accept: "application/json", "Cache-Control": "no-cache" },
    signal: globalThis.AbortSignal.timeout(15000),
    redirect: "error",
  });
  assert.equal(response.status, 200, "registry inventory request failed");
  const chunks = [];
  let length = 0;
  for await (const chunk of response.body) {
    length += chunk.byteLength;
    assert.ok(length <= limit, "oversized registry inventory");
    chunks.push(Buffer.from(chunk));
  }
  return JSON.parse(Buffer.concat(chunks).toString("utf8"));
}

async function main() {
  const [candidateVersion, sourceSHA, output] = process.argv.slice(2);
  assert.equal(process.argv.length, 5, "expected candidate, source SHA and output path");
  const result = summarizeRegistry(await readRegistry(), candidateVersion, sourceSHA);
  await writeFile(output, `${JSON.stringify(result, null, 2)}\n`, { flag: "wx", mode: 0o600 });
  console.log(JSON.stringify(result));
  assert.equal(
    result.candidateAlreadyPublished,
    false,
    "candidate version is already published; never overwrite or reuse it",
  );
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch(() => {
    console.error("release registry inventory failed; no publication was attempted");
    process.exitCode = 1;
  });
}
