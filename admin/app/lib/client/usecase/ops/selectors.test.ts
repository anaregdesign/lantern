import { describe, expect, it } from "bun:test";

import type { ServerStatus } from "~/lib/client/infrastructure/api/get-server-status";
import {
  formatCount,
  formatBytes,
  formatDuration,
  formatStaleness,
  formatUptime,
  peerStatePillIntent,
  replicationCardSummary,
  searchHealthIntent,
  searchStatusSummary,
  serverCardSummary,
} from "./selectors";

/**
 * makeStatus builds a complete ServerStatus fixture with sensible
 * defaults so each test only spells out the fields it cares about.
 */
function makeStatus(overrides: Partial<ServerStatus> = {}): ServerStatus {
  return {
    version: "v1.0.0",
    goVersion: "go1.26.4",
    startedAtMs: 1_700_000_000_000,
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
    search: {
      enabled: true,
      positionsEnabled: true,
      defaultLimit: 100,
      maxLimit: 1000,
      defaultMatchMode: "any",
      defaultMinShouldMatch: 1,
      maxFuzziness: 2,
      analyzerVersion: "script-aware-v1",
      projectionVersion: "vertex-key-value-v1",
      configFingerprint: "fingerprint",
      timeoutMs: 5000,
      maxQueryBytes: 16384,
      maxQueryTerms: 1024,
      maxDictionaryVisits: 1_000_000,
      maxPostingVisits: 10_000_000,
      maxPositionVisits: 10_000_000,
      maxExpirationVisits: 1_000_000,
      maxInFlight: 32,
      maxDocumentBytes: 1_048_576,
      maxDocumentTokens: 32_768,
      maxDocumentTerms: 16_384,
      maxLiveTerms: 1_000_000,
      maxLivePostings: 10_000_000,
      maxPositionEntries: 10_000_000,
      compactionRatio: 1.5,
      compactionMinRetired: 10_000,
      index: {
        health: "healthy",
        documents: 42,
        physicalDocuments: 44,
        expiredDocuments: 2,
        expirationQueueEntries: 2,
        expirationPurged: 7,
        liveTerms: 11,
        retainedTermSlots: 13,
        retainedOrdinals: 44,
        postings: 120,
        positionEntries: 240,
        estimatedLiveBytes: 1024,
        estimatedRetainedBytes: 1536,
        rebuildCount: 2,
        lastRebuildDurationSeconds: 0.025,
        lastExpirationPurgeDurationSeconds: 0.001,
      },
    },
    ...overrides,
  };
}

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

describe("formatBytes", () => {
  it("uses binary units without hiding small values", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(1536)).toBe("1.50 KiB");
    expect(formatBytes(Number.NaN)).toBe("-");
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
    const rows = serverCardSummary(
      makeStatus({
        uptimeSeconds: 3600,
        tlsEnabled: true,
        replicationEnabled: false,
      }),
    );
    // First row is always Version.
    expect(rows[0]).toEqual(["Version", "v1.0.0"]);
    // A server with a known start instant renders its formatted uptime.
    expect(rows.find(([label]) => label === "Uptime")?.[1]).toBe("1h 0m");
    // Replication label flips based on the bool input.
    expect(rows.find(([label]) => label === "Replication")?.[1]).toBe(
      "disabled",
    );
    expect(rows.find(([label]) => label === "TLS")?.[1]).toBe("enabled");
    expect(rows.some(([label]) => label === "Search config")).toBe(false);
  });

  it("substitutes (dev) when version is empty", () => {
    const rows = serverCardSummary(
      makeStatus({
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
        vertexCount: 0,
        edgeCount: 0,
      }),
    );
    expect(rows[0][1]).toBe("(dev)");
  });

  it("renders — for Uptime when started_at is absent on the wire (#943)", () => {
    // startedAtMs === 0 means the server sent no started_at field (a
    // stale/older server, or MarkStarted never fired). Uptime is unknown
    // and must not masquerade as a fresh "0s".
    const rows = serverCardSummary(
      makeStatus({ startedAtMs: 0, uptimeSeconds: 0 }),
    );
    expect(rows.find(([label]) => label === "Uptime")?.[1]).toBe("—");
  });

  it("still renders 0s for a genuinely just-started server (#943)", () => {
    // A server that started this instant reports started_at WITH
    // uptimeSeconds === 0 — that is a legitimate "0s", discriminated from
    // the absent-field case by startedAtMs being set.
    const rows = serverCardSummary(
      makeStatus({ startedAtMs: 1_700_000_000_000, uptimeSeconds: 0 }),
    );
    expect(rows.find(([label]) => label === "Uptime")?.[1]).toBe("0s");
  });
});

describe("searchStatusSummary", () => {
  it("surfaces capability, budgets, index health inputs, and retention", () => {
    const search = makeStatus().search;
    const rows = searchStatusSummary(search);
    expect(rows.find(([label]) => label === "Capability")?.[1]).toBe("enabled");
    expect(rows.find(([label]) => label === "Documents")?.[1]).toContain(
      "42 live",
    );
    expect(rows.find(([label]) => label === "Retained storage")?.[1]).toContain(
      "1.50× live",
    );
    expect(rows.find(([label]) => label === "Config fingerprint")?.[1]).toBe(
      "fingerprint",
    );
  });

  it("maps every health state to an actionable intent", () => {
    expect(searchHealthIntent("healthy")).toBe("success");
    expect(searchHealthIntent("incomplete")).toBe("error");
    expect(searchHealthIntent("disabled")).toBe("info");
    expect(searchHealthIntent("unspecified")).toBe("warning");
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
