import { expect, test } from "bun:test";
import { normaliseBaseUrl } from "./base-url";
test("gateway URLs reject embedded credentials and query/fragment ambiguity", () => {
  for (const url of [
    "https://user:secret@example.com",
    "https://example.com?token=secret",
    "https://example.com#fragment",
    "file:///tmp/server",
  ])
    expect(normaliseBaseUrl(url)).toBeNull();
  expect(normaliseBaseUrl(" https://example.com/api/ ")).toBe(
    "https://example.com/api",
  );
});
