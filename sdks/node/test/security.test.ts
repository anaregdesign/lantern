import { expect, test } from "bun:test";
import { createRouterTransport, Code, ConnectError } from "@connectrpc/connect";
import { SecurityClient } from "../src/security.js";
import { AuthMode, parseBrowserSession } from "../src/security-types.js";
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
