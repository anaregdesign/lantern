import { describe, expect, it } from "bun:test";

import { opsReducer, type OpsAction } from "./reducer";
import { INITIAL_OPS_STATE, type OpsState } from "./state";
import type { ServerStatus } from "~/lib/client/infrastructure/api/get-server-status";
import type { ReplicationStatus } from "~/lib/client/infrastructure/api/get-replication-status";

const sampleServer: ServerStatus = {
  version: "v1.0.0",
  goVersion: "go1.26.4",
  startedAtMs: 1_700_000_000_000,
  uptimeSeconds: 3700,
  defaultTtlSeconds: 60,
  maxBatchSize: 10_000,
  maxKeyBytes: 1024,
  scanDefaultLimit: 1000,
  scanMaxLimit: 10_000,
  tlsEnabled: false,
  replicationEnabled: true,
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
    cursorTtlSeconds: 60,
    maxSessions: 128,
    maxSessionHits: 10_000,
    maxSessionBytes: 67_108_864,
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
      generation: 7,
    },
  },
};

const sampleReplication: ReplicationStatus = {
  nodeId: "abc123",
  localNowMs: 1_700_000_003_700,
  enabled: true,
  peers: [
    {
      address: "10.0.0.1:6380",
      state: "streaming",
      lastEventAtMs: 1_700_000_003_500,
      stalenessMs: 200,
      appliedSeq: 99,
      error: "",
    },
  ],
};

function apply(actions: OpsAction[]): OpsState {
  return actions.reduce(opsReducer, INITIAL_OPS_STATE);
}

describe("opsReducer", () => {
  describe("FETCH_STARTED", () => {
    it("flips both cards to loading on the first fetch", () => {
      const next = opsReducer(INITIAL_OPS_STATE, {
        type: "FETCH_STARTED",
        epoch: 1,
      });
      expect(next.server.status).toBe("loading");
      expect(next.replication.status).toBe("loading");
      expect(next.fetchEpoch).toBe(1);
    });

    it("preserves card status on subsequent fetches (no flash)", () => {
      const after = apply([
        { type: "FETCH_STARTED", epoch: 1 },
        { type: "SERVER_LOADED", epoch: 1, data: sampleServer },
        { type: "REPLICATION_LOADED", epoch: 1, data: sampleReplication },
        { type: "FETCH_STARTED", epoch: 2 },
      ]);
      // Both cards must stay in 'ready' so the UI does not flash a
      // loading skeleton on every poll tick.
      expect(after.server.status).toBe("ready");
      expect(after.server.data).toBe(sampleServer);
      expect(after.replication.status).toBe("ready");
      expect(after.fetchEpoch).toBe(2);
    });
  });

  describe("epoch gating", () => {
    it("ignores stale SERVER_LOADED dispatches", () => {
      const after = apply([
        { type: "FETCH_STARTED", epoch: 1 },
        { type: "FETCH_STARTED", epoch: 2 },
        // Stale: epoch=1 result arrives after a newer fetch was started.
        { type: "SERVER_LOADED", epoch: 1, data: sampleServer },
      ]);
      expect(after.server.data).toBeNull();
      expect(after.server.status).toBe("loading");
    });

    it("applies the matching epoch", () => {
      const after = apply([
        { type: "FETCH_STARTED", epoch: 1 },
        { type: "SERVER_LOADED", epoch: 1, data: sampleServer },
      ]);
      expect(after.server.data).toBe(sampleServer);
      expect(after.server.status).toBe("ready");
    });

    it("ignores stale error dispatches", () => {
      const after = apply([
        { type: "FETCH_STARTED", epoch: 1 },
        { type: "FETCH_STARTED", epoch: 2 },
        { type: "SERVER_ERROR", epoch: 1, error: "old" },
      ]);
      expect(after.server.error).toBeNull();
    });
  });

  describe("independent card failure", () => {
    it("a server error does not clear replication state", () => {
      const after = apply([
        { type: "FETCH_STARTED", epoch: 1 },
        { type: "REPLICATION_LOADED", epoch: 1, data: sampleReplication },
        { type: "SERVER_ERROR", epoch: 1, error: "boom" },
      ]);
      expect(after.server.status).toBe("error");
      expect(after.server.error).toBe("boom");
      expect(after.replication.status).toBe("ready");
      expect(after.replication.data).toBe(sampleReplication);
    });

    it("a replication error does not clear server state", () => {
      const after = apply([
        { type: "FETCH_STARTED", epoch: 1 },
        { type: "SERVER_LOADED", epoch: 1, data: sampleServer },
        { type: "REPLICATION_ERROR", epoch: 1, error: "boom" },
      ]);
      expect(after.server.status).toBe("ready");
      expect(after.replication.status).toBe("error");
      expect(after.replication.error).toBe("boom");
    });
  });

  describe("SET_POLL_MS", () => {
    it("clamps negative values to 0 (polling disabled)", () => {
      const after = opsReducer(INITIAL_OPS_STATE, {
        type: "SET_POLL_MS",
        pollMs: -1,
      });
      expect(after.pollMs).toBe(0);
    });

    it("accepts a positive value verbatim", () => {
      const after = opsReducer(INITIAL_OPS_STATE, {
        type: "SET_POLL_MS",
        pollMs: 10_000,
      });
      expect(after.pollMs).toBe(10_000);
    });
  });
});
