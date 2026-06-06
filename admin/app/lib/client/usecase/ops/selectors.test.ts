import { describe, expect, it } from "bun:test";

import {
  formatCount,
  formatDuration,
  formatStaleness,
  formatUptime,
  peerStatePillIntent,
  replicationCardSummary,
  serverCardSummary,
} from "./selectors";

describe("formatUptime", () => {
  it("returns 0s for zero or negative input", () => {
    expect(formatUptime(0)).toBe("0s");
    expect(formatUptime(-1)).toBe("0s");
  });
  it("renders sub-minute seconds verbatim", () => {
    expect(formatUptime(5)).toBe("5s");
    expect(formatUptime(59)).toBe("59s");
  });
  it("falls back to minutes + seconds under an hour", () => {
    expect(formatUptime(90)).toBe("1m 30s");
  });
  it("falls back to hours + minutes under a day", () => {
    expect(formatUptime(3700)).toBe("1h 1m");
  });
  it("falls back to days + hours past a day", () => {
    expect(formatUptime(86_400 + 3600)).toBe("1d 1h");
  });
});

describe("formatDuration", () => {
  it("returns - for non-positive input", () => {
    expect(formatDuration(0)).toBe("-");
    expect(formatDuration(-1)).toBe("-");
  });
  it("renders sub-minute seconds verbatim", () => {
    expect(formatDuration(30)).toBe("30s");
  });
  it("collapses minute/hour/day boundaries", () => {
    expect(formatDuration(60)).toBe("1m");
    expect(formatDuration(3600)).toBe("1h");
    expect(formatDuration(86_400)).toBe("1d");
  });
});

describe("formatStaleness", () => {
  it("returns - for non-positive input", () => {
    expect(formatStaleness(0)).toBe("-");
  });
  it("rounds down to whole seconds for sub-minute lag", () => {
    expect(formatStaleness(2_500)).toBe("2s ago");
  });
  it("steps through minute/hour/day boundaries", () => {
    expect(formatStaleness(90_000)).toBe("1m ago");
    expect(formatStaleness(3_600_000)).toBe("1h ago");
    expect(formatStaleness(86_400_000)).toBe("1d ago");
  });
});

describe("formatCount", () => {
  it("returns - for NaN", () => {
    expect(formatCount(NaN)).toBe("-");
  });
  it("applies thousands separators", () => {
    expect(formatCount(1_234_567)).toMatch(/1[,.]234[,.]567/);
  });
});

describe("peerStatePillIntent", () => {
  it("maps every documented state to a Fluent intent", () => {
    expect(peerStatePillIntent("streaming")).toBe("success");
    expect(peerStatePillIntent("connecting")).toBe("info");
    expect(peerStatePillIntent("backoff")).toBe("warning");
    expect(peerStatePillIntent("closed")).toBe("error");
    expect(peerStatePillIntent("unspecified")).toBe("warning");
  });
});

describe("serverCardSummary", () => {
  it("emits a stable ordered list of (label, value) pairs", () => {
    const rows = serverCardSummary({
      version: "v1.0.0",
      goVersion: "go1.26.4",
      startedAtMs: 0,
      uptimeSeconds: 3600,
      defaultTtlSeconds: 60,
      maxBatchSize: 10_000,
      maxKeyBytes: 1024,
      scanDefaultLimit: 1000,
      scanMaxLimit: 10_000,
      tlsEnabled: true,
      replicationEnabled: false,
      vertexCount: 42,
      edgeCount: 17,
    });
    // First row is always Version.
    expect(rows[0]).toEqual(["Version", "v1.0.0"]);
    // Replication label flips based on the bool input.
    expect(rows.find(([label]) => label === "Replication")?.[1]).toBe(
      "disabled",
    );
    expect(rows.find(([label]) => label === "TLS")?.[1]).toBe("enabled");
  });

  it("substitutes (dev) when version is empty", () => {
    const rows = serverCardSummary({
      version: "",
      goVersion: "",
      startedAtMs: 0,
      uptimeSeconds: 0,
      defaultTtlSeconds: 0,
      maxBatchSize: 0,
      maxKeyBytes: 0,
      scanDefaultLimit: 0,
      scanMaxLimit: 0,
      tlsEnabled: false,
      replicationEnabled: false,
      vertexCount: 0,
      edgeCount: 0,
    });
    expect(rows[0][1]).toBe("(dev)");
  });
});

describe("replicationCardSummary", () => {
  it("collapses single-instance state to the documented label", () => {
    const rows = replicationCardSummary({
      nodeId: "abc",
      localNowMs: 0,
      enabled: false,
      peers: [],
    });
    expect(rows.find(([label]) => label === "Replication")?.[1]).toBe(
      "disabled (single instance)",
    );
    expect(rows.find(([label]) => label === "Peers tracked")?.[1]).toBe("0");
  });
});
