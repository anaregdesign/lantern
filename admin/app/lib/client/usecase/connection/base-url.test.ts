import { expect, test } from "bun:test";
import { defaultBaseUrl, normaliseBaseUrl } from "./base-url";
test("fresh production browsers use the same-origin gateway", () => {
  expect(defaultBaseUrl("https://localhost:8443")).toBe(
    "https://localhost:8443",
  );
  expect(defaultBaseUrl("http://localhost:8080")).toBe("http://localhost:8080");
  expect(defaultBaseUrl("http://localhost:5173", true)).toBe(
    "http://localhost:6380",
  );
  expect(defaultBaseUrl()).toBe("http://localhost:6380");
});
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
