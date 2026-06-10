import { describe, expect, it } from "bun:test";

import {
  DEFAULT_PROMETHEUS_URL,
  normalisePrometheusUrl,
} from "./prometheus-url";

describe("DEFAULT_PROMETHEUS_URL", () => {
  it("is the same-origin reverse-proxy path", () => {
    expect(DEFAULT_PROMETHEUS_URL).toBe("/api/prom");
  });
});

describe("normalisePrometheusUrl", () => {
  it("accepts a root-relative same-origin path", () => {
    expect(normalisePrometheusUrl("/api/prom")).toBe("/api/prom");
  });

  it("strips trailing slashes from a relative path", () => {
    expect(normalisePrometheusUrl("/api/prom/")).toBe("/api/prom");
    expect(normalisePrometheusUrl("/api/prom///")).toBe("/api/prom");
  });

  it("keeps a bare root path as '/'", () => {
    expect(normalisePrometheusUrl("/")).toBe("/");
  });

  it("trims surrounding whitespace", () => {
    expect(normalisePrometheusUrl("  /api/prom  ")).toBe("/api/prom");
    expect(normalisePrometheusUrl(" http://localhost:9090 ")).toBe(
      "http://localhost:9090",
    );
  });

  it("accepts an absolute http URL and strips the trailing slash", () => {
    expect(normalisePrometheusUrl("http://localhost:9090/")).toBe(
      "http://localhost:9090",
    );
  });

  it("accepts an absolute https URL with a path prefix", () => {
    expect(normalisePrometheusUrl("https://prom.example.com/prefix/")).toBe(
      "https://prom.example.com/prefix",
    );
  });

  it("returns null for an empty or whitespace-only input", () => {
    expect(normalisePrometheusUrl("")).toBeNull();
    expect(normalisePrometheusUrl("   ")).toBeNull();
  });

  it("returns null for a non-http(s) scheme", () => {
    expect(normalisePrometheusUrl("ftp://example.com")).toBeNull();
    expect(normalisePrometheusUrl("ws://example.com")).toBeNull();
  });

  it("returns null for a protocol-relative URL", () => {
    expect(normalisePrometheusUrl("//evil.example.com")).toBeNull();
  });

  it("returns null for a bare host without a scheme or leading slash", () => {
    expect(normalisePrometheusUrl("localhost:9090")).toBeNull();
    expect(normalisePrometheusUrl("api/prom")).toBeNull();
  });
});
