import { expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport, Code, ConnectError } from "@connectrpc/connect";
import {
  SecurityClient,
  SecurityOperationAuthorizationRequiredError,
  SecurityChangePrecommitRejectedError,
} from "../src/security.js";
import {
  AuthMode,
  SecurityEnforcementState,
  SecurityAuthorizationRequirement,
  SecurityAuthorizationState,
  parseBrowserSession,
} from "../src/security-types.js";
import {
  LanternSecurityService,
  SecurityOperationAuthorizationRequiredSchema,
  SecurityChangePrecommitRejectedSchema,
  SecurityChangeRejectionReason,
} from "../src/gen/graph/v1/security_pb.js";
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

test("operation authorization transports exact review and proof separately with one Apply attempt", async () => {
  const review = {
    expectedVersion: {
      revision: 3n,
      digest: new Uint8Array(32).fill(1),
      generation: new Uint8Array(16).fill(2),
    },
    changeId: new Uint8Array(16).fill(7),
    changes: [],
  };
  const authorizationId = new Uint8Array(32).fill(8),
    proof = new Uint8Array(32).fill(9);
  let attempts = 0;
  const transport = createRouterTransport(({ service }) =>
    service(LanternSecurityService, {
      prepareSecurityChanges: (request) => {
        expect(request.review?.changeId).toEqual(review.changeId);
        return {
          ...review,
          intentDigest: new Uint8Array(32).fill(4),
          requirement: SecurityAuthorizationRequirement.REAUTHENTICATION,
        };
      },
      beginSecurityChangeAuthorization: (request) => {
        expect(request.review?.expectedVersion?.digest).toEqual(review.expectedVersion.digest);
        return {
          authorizationId,
          startUrl: "https://admin.example/auth/management-authorization/" + "A".repeat(43),
        };
      },
      getSecurityChangeAuthorization: (request) => {
        expect(request.authorizationId).toEqual(authorizationId);
        return {
          authorizationId,
          authorizationProof: proof,
          state: SecurityAuthorizationState.APPROVED,
        };
      },
      applySecurityChanges: (request) => {
        attempts++;
        expect(request.changeId).toEqual(review.changeId);
        expect(request.authorizationProof).toEqual(proof);
        throw new ConnectError("operation proof expired", Code.FailedPrecondition);
      },
    }),
  );
  const client = SecurityClient.withTransport(transport);
  expect((await client.prepareSecurityChanges({ review })).requirement).toBe(
    SecurityAuthorizationRequirement.REAUTHENTICATION,
  );
  expect((await client.beginSecurityChangeAuthorization({ review })).authorizationId).toEqual(
    authorizationId,
  );
  const approval = await client.getSecurityChangeAuthorization({ authorizationId });
  expect(approval.authorizationProof).toEqual(proof);
  await expect(
    client.applySecurityChanges({
      expectedRevision: 3n,
      changeId: review.changeId,
      changes: review.changes,
      authorizationProof: approval.authorizationProof,
    }),
  ).rejects.toBeInstanceOf(FailedPreconditionError);
  expect(attempts).toBe(1);
});

test("Security SDK preserves exact typed noncommit proof refusal through plural and singular Apply", async () => {
  const detail = create(SecurityOperationAuthorizationRequiredSchema, {
    changeId: new Uint8Array(16).fill(7),
    expectedVersion: {
      revision: 3n,
      digest: new Uint8Array(32).fill(1),
      generation: new Uint8Array(16).fill(2),
    },
    intentDigest: new Uint8Array(32).fill(4),
  });
  let attempts = 0;
  const refuse = () => {
    attempts++;
    throw new ConnectError("operation approval required", Code.FailedPrecondition, undefined, [
      { desc: SecurityOperationAuthorizationRequiredSchema, value: detail },
    ]);
  };
  const transport = createRouterTransport(({ service }) =>
    service(LanternSecurityService, { applySecurityChanges: refuse, applySecurityChange: refuse }),
  );
  const client = SecurityClient.withTransport(transport);
  for (const call of [
    () =>
      client.applySecurityChanges({ expectedRevision: 3n, changeId: detail.changeId, changes: [] }),
    () => client.applySecurityChange({ expectedRevision: 3n, changeId: detail.changeId }),
  ]) {
    try {
      await call();
      throw new Error("typed refusal not raised");
    } catch (error) {
      expect(error).toBeInstanceOf(SecurityOperationAuthorizationRequiredError);
      const refused = error as SecurityOperationAuthorizationRequiredError;
      expect(refused.detail.changeId).toEqual(detail.changeId);
      expect(refused.detail.expectedVersion).toEqual(detail.expectedVersion);
      expect(refused.detail.intentDigest).toEqual(detail.intentDigest);
    }
  }
  expect(attempts).toBe(2);
});

test("precommit refusal preserves exact invocation through single and batch Apply", async () => {
  const detail = create(SecurityChangePrecommitRejectedSchema, {
    changeId: new Uint8Array(16).fill(7),
    expectedRevision: 3n,
    reason: SecurityChangeRejectionReason.UNKNOWN_ROLE,
  });
  let attempts = 0;
  const refuse = () => {
    attempts++;
    throw new ConnectError("unknown Role", Code.FailedPrecondition, undefined, [
      { desc: SecurityChangePrecommitRejectedSchema, value: detail },
    ]);
  };
  const client = SecurityClient.withTransport(
    createRouterTransport(({ service }) =>
      service(LanternSecurityService, {
        applySecurityChanges: refuse,
        applySecurityChange: refuse,
      }),
    ),
  );
  for (const call of [
    () => client.applySecurityChanges({ expectedRevision: 3n, changeId: detail.changeId }),
    () => client.applySecurityChange({ expectedRevision: 3n, changeId: detail.changeId }),
  ]) {
    try {
      await call();
      throw new Error("refusal absent");
    } catch (error) {
      expect(error).toBeInstanceOf(SecurityChangePrecommitRejectedError);
      expect((error as SecurityChangePrecommitRejectedError).detail).toEqual(detail);
      expect(ConnectError.from((error as SecurityChangePrecommitRejectedError).cause).code).toBe(
        Code.FailedPrecondition,
      );
    }
  }
  expect(attempts).toBe(2);
});

test("malformed, duplicated, unknown, conflicting or postcommit error details remain generic", async () => {
  const valid = create(SecurityChangePrecommitRejectedSchema, {
    changeId: new Uint8Array(16).fill(7),
    expectedRevision: 3n,
    reason: SecurityChangeRejectionReason.UNKNOWN_ROLE,
  });
  const encode = (value: typeof valid) => ({ desc: SecurityChangePrecommitRejectedSchema, value });
  const auth = create(SecurityOperationAuthorizationRequiredSchema);
  for (const fixture of [
    { code: Code.Unavailable, details: [encode(valid)] },
    { code: Code.Aborted, details: [encode(valid)] },
    { code: Code.FailedPrecondition, details: [encode({ ...valid, reason: 999 })] },
    {
      code: Code.FailedPrecondition,
      details: [encode({ ...valid, changeId: new Uint8Array(15) })],
    },
    {
      code: Code.FailedPrecondition,
      details: [encode({ ...valid, changeId: new Uint8Array(16) })],
    },
    { code: Code.FailedPrecondition, details: [encode({ ...valid, expectedRevision: 0n })] },
    { code: Code.FailedPrecondition, details: [encode(valid), encode(valid)] },
    {
      code: Code.FailedPrecondition,
      details: [
        encode(valid),
        { type: SecurityChangePrecommitRejectedSchema.typeName, value: new Uint8Array([255]) },
      ],
    },
    {
      code: Code.FailedPrecondition,
      details: [encode(valid), { desc: SecurityOperationAuthorizationRequiredSchema, value: auth }],
    },
    { code: Code.FailedPrecondition, details: [] },
  ]) {
    const client = SecurityClient.withTransport(
      createRouterTransport(({ service }) =>
        service(LanternSecurityService, {
          applySecurityChanges: () => {
            throw new ConnectError("unconfirmed", fixture.code, undefined, fixture.details);
          },
        }),
      ),
    );
    try {
      await client.applySecurityChanges({});
      throw new Error("error absent");
    } catch (error) {
      expect(error).not.toBeInstanceOf(SecurityChangePrecommitRejectedError);
    }
  }
});
