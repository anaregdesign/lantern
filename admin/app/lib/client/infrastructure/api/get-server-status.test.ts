import { describe, expect, it } from "bun:test";

import type { LanternClient } from "./lantern-client";
import { getServerStatus } from "./get-server-status";

function clientReturning(response: unknown): LanternClient {
  return {
    getServerStatus: async () => response,
  } as unknown as LanternClient;
}

function baseResponse(): Record<string, unknown> {
  return {
    version: "test",
    goVersion: "go-test",
    maxBatchSize: 0,
    maxKeyBytes: 0,
    scanDefaultLimit: 0,
    scanMaxLimit: 0,
    tlsEnabled: false,
    replicationEnabled: false,
    vertexCount: 0n,
    edgeCount: 0n,
  };
}

describe("getServerStatus causal metadata presence", () => {
  it("keeps an unsupported old-server field unavailable", async () => {
    const status = await getServerStatus(clientReturning(baseResponse()));

    expect(status.causalMetadata).toBeNull();
  });

  it("keeps a present all-zero snapshot distinct from unsupported", async () => {
    const status = await getServerStatus(
      clientReturning({ ...baseResponse(), causalMetadata: {} }),
    );

    expect(status.causalMetadata).not.toBeNull();
    expect(status.causalMetadata?.vertices).toMatchObject({
      limit: 0,
      entries: 0,
      rejectedTotal: 0,
      overLimit: false,
    });
    expect(status.causalMetadata?.edges).toMatchObject({
      limit: 0,
      entries: 0,
      rejectedTotal: 0,
      overLimit: false,
    });
  });
});
