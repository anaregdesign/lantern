import { expect, test } from "bun:test";
import { createRouterTransport, Code, ConnectError } from "@connectrpc/connect";
import { SecurityClient } from "../src/security.js";
import { AuthMode, SecurityEnforcementState, parseBrowserSession } from "../src/security-types.js";
import { LanternSecurityService } from "../src/gen/graph/v1/security_pb.js";
import { FailedPreconditionError } from "../src/errors.js";
test("Security facade preserves protocol readiness and makes one mutation attempt", async () => {
  let attempts = 0;
  const transport = createRouterTransport(({ service }) =>
    service(LanternSecurityService, {
      getAuthCapabilities: () => ({ mode: AuthMode.OIDC, protocolVersion: 1, ready: false }),
      applySecurityChanges: () => {
        attempts++;
        throw new ConnectError("CAS conflict", Code.FailedPrecondition);
      },
    }),
  );
  const client = SecurityClient.withTransport(transport);
  const caps = await client.getAuthCapabilities();
  expect(caps.mode).toBe(AuthMode.OIDC);
  expect(caps.ready).toBe(false);
  await expect(
    client.applySecurityChanges({
      expectedRevision: 1n,
      changeId: new Uint8Array(16),
      changes: [],
    }),
  ).rejects.toBeInstanceOf(FailedPreconditionError);
  expect(attempts).toBe(1);
});
test("browser JSON decoding rejects unknown enum and malformed fields", () => {
  expect(() => parseBrowserSession('{"mode":"invented"}')).toThrow();
  expect(() => parseBrowserSession('{"principal":{"expiresAt":"not-a-time"}}')).toThrow();
  expect(parseBrowserSession('{"mode":"AUTH_MODE_OFF"}').mode).toBe(AuthMode.OFF);
});

test("Security status facade returns original retained proof without Apply outcomes", async () => {
  const changeId = new Uint8Array(16).fill(7);
  let statusCalls = 0;
  const transport = createRouterTransport(({ service }) =>
    service(LanternSecurityService, {
      getSecurityChangeStatus: (request) => {
        statusCalls++;
        expect(request.changeId).toEqual(changeId);
        return {
          changeId,
          version: {
            revision: 4n,
            digest: new Uint8Array(32).fill(1),
            generation: new Uint8Array(16).fill(2),
          },
          enforcement: SecurityEnforcementState.ENFORCED,
        };
      },
    }),
  );
  const proof = await SecurityClient.withTransport(transport).getSecurityChangeStatus({ changeId });
  expect(proof.changeId).toEqual(changeId);
  expect(proof.version?.revision).toBe(4n);
  expect(proof.enforcement).toBe(SecurityEnforcementState.ENFORCED);
  expect("applied" in proof).toBe(false);
  expect("replayed" in proof).toBe(false);
  expect(statusCalls).toBe(1);
});
