import assert from "node:assert/strict";
import { ReadableStream } from "node:stream/web";
import test from "node:test";
import { TextEncoder } from "node:util";
import { readRegistry, summarizeRegistry } from "./release-registry-inventory.mjs";

function registry() {
  return {
    name: "lantern-sdk",
    "dist-tags": { latest: "0.13.0" },
    versions: {
      "0.13.0": {
        name: "lantern-sdk",
        version: "0.13.0",
        dist: { shasum: "a".repeat(40), integrity: "sha512-YWJjZA==" },
      },
    },
  };
}

test("unpublished candidate is separate from the latest published package", () => {
  const result = summarizeRegistry(registry(), "0.14.0", "b".repeat(40));
  assert.equal(result.latestPublishedVersion, "0.13.0");
  assert.equal(result.candidateAlreadyPublished, false);
});

test("existing version is recorded and cannot be treated as absent", () => {
  assert.equal(
    summarizeRegistry(registry(), "0.13.0", "b".repeat(40)).candidateAlreadyPublished,
    true,
  );
});

test("malformed registry responses never qualify absence", () => {
  for (const mutate of [
    (value) => {
      value.name = "other-package";
    },
    (value) => {
      delete value.versions;
    },
    (value) => {
      value["dist-tags"].latest = "0.99.0";
    },
    (value) => {
      delete value.versions["0.13.0"].dist.integrity;
    },
  ]) {
    const value = registry();
    mutate(value);
    assert.throws(() => summarizeRegistry(value, "0.14.0", "b".repeat(40)));
  }
});

test("invalid release identity is refused", () => {
  assert.throws(() => summarizeRegistry(registry(), "latest", "b".repeat(40)));
  assert.throws(() => summarizeRegistry(registry(), "0.14.0", "main"));
});

test("HTTP and malformed body failures never become an unpublished verdict", async () => {
  for (const response of [
    new globalThis.Response("not found", { status: 404 }),
    new globalThis.Response("upstream unavailable", { status: 503 }),
    new globalThis.Response("not JSON", { status: 200 }),
  ]) {
    await assert.rejects(readRegistry(async () => response));
  }
});

test("inventory request is fixed and bounded and preserves UTF-8", async () => {
  const metadata = registry();
  metadata.description = "日本語";
  const bytes = new TextEncoder().encode(JSON.stringify(metadata));
  const result = await readRegistry(async (url, options) => {
    assert.equal(url, "https://registry.npmjs.org/lantern-sdk");
    assert.equal(options.redirect, "error");
    assert.ok(options.signal instanceof globalThis.AbortSignal);
    return new globalThis.Response(
      new ReadableStream({
        start(controller) {
          for (const byte of bytes) controller.enqueue(Uint8Array.of(byte));
          controller.close();
        },
      }),
    );
  });
  assert.equal(result.description, metadata.description);
});
