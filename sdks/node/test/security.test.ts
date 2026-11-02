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
  currentUserId,
  SecurityPrincipalKind,
  ManagedLogLevel,
  LogSettingsSource,
  LogPolicyAdoptionState,
  LogSettingsApplicationState,
  LogSettingsError,
} from "../src/security-types.js";
import {
  LanternSecurityService,
  SecurityOperationAuthorizationRequiredSchema,
  SecurityChangePrecommitRejectedSchema,
  SecurityChangeRejectionReason,
  GetLogSettingsResponseSchema,
  LogPolicyPreparationReferenceSchema,
} from "../src/gen/graph/v1/security_pb.js";
import { FailedPreconditionError } from "../src/errors.js";

function logSettingsFixture() {
  const bytes = (length: number, value: number) => new Uint8Array(length).fill(value);
  const profile = {
    version: 2,
    domain: bytes(32, 1),
    cohort: bytes(32, 2),
    generation: bytes(16, 3),
    protocol: bytes(32, 4),
    timeProfile: bytes(32, 5),
    membership: bytes(32, 6),
    configuration: bytes(32, 7),
  };
  const cut = {
    version: 1,
    domain: profile.domain,
    cohort: profile.cohort,
    generation: profile.generation,
    sequence: 9n,
    previous: bytes(32, 0),
    projection: bytes(32, 8),
    frontier: bytes(32, 9),
    fences: bytes(32, 10),
    policy: bytes(32, 11),
  };
  return create(GetLogSettingsResponseSchema, {
    schemaVersion: 1,
    member: 1,
    effectiveLevel: ManagedLogLevel.INFO,
    source: LogSettingsSource.ENVIRONMENT,
    adoption: LogPolicyAdoptionState.UNADOPTED,
    application: LogSettingsApplicationState.APPLIED,
    error: LogSettingsError.NONE,
    version: { currentProfile: profile, currentCut: cut, admissionBinding: bytes(32, 12) },
  });
}

function logPreparationFixture(settings: ReturnType<typeof logSettingsFixture>) {
  const profile = settings.version!.currentProfile!;
  return create(LogPolicyPreparationReferenceSchema, {
    changeId: {
      version: 1,
      domain: profile.domain,
      cohort: profile.cohort,
      namespace: 11n,
      nonce: new Uint8Array(16).fill(3),
    },
    intentDigest: new Uint8Array(32).fill(4),
    actorDigest: new Uint8Array(32).fill(5),
  });
}

test("log settings distinguish unadopted, committed pending, installed and local apply failure", async () => {
  const settings = logSettingsFixture();
  let calls = 0;
  const client = SecurityClient.withTransport(
    createRouterTransport(({ service }) =>
      service(LanternSecurityService, {
        getLogSettings: () => {
          calls++;
          return settings;
        },
      }),
    ),
  );
  expect((await client.getLogSettings()).appliedGeneration).toBe(0n);
  settings.adoption = LogPolicyAdoptionState.FENCED_UNADOPTED;
  settings.preparation = logPreparationFixture(settings);
  expect((await client.getLogSettings()).adoption).toBe(LogPolicyAdoptionState.FENCED_UNADOPTED);
  settings.desired = {
    $typeName: "graph.v1.ManagedLogPolicy",
    version: 1,
    level: ManagedLogLevel.WARN,
  };
  settings.desiredGeneration = 2n;
  settings.adoption = LogPolicyAdoptionState.MANAGED;
  settings.application = LogSettingsApplicationState.PENDING;
  const pending = await client.getLogSettings();
  expect(pending.effectiveLevel).toBe(ManagedLogLevel.INFO);
  expect(pending.appliedGeneration).toBe(0n);
  settings.application = LogSettingsApplicationState.ERROR;
  settings.error = LogSettingsError.APPLY_FAILED;
  expect((await client.getLogSettings()).source).toBe(LogSettingsSource.ENVIRONMENT);
  settings.application = LogSettingsApplicationState.APPLIED;
  settings.error = LogSettingsError.NONE;
  settings.effectiveLevel = ManagedLogLevel.WARN;
  settings.source = LogSettingsSource.MANAGED;
  settings.appliedGeneration = 2n;
  settings.appliedCut = settings.version!.currentCut;
  expect((await client.getLogSettings()).appliedGeneration).toBe(2n);
  settings.appliedCut = { ...settings.version!.currentCut!, sequence: 8n };
  expect((await client.getLogSettings()).application).toBe(LogSettingsApplicationState.APPLIED);
  settings.appliedCut = { ...settings.appliedCut, generation: new Uint8Array(16).fill(99) };
  await expect(client.getLogSettings()).rejects.toThrow();
  settings.appliedCut = settings.version!.currentCut;
  settings.effectiveLevel = ManagedLogLevel.INFO;
  await expect(client.getLogSettings()).rejects.toThrow();
  expect(calls).toBe(8);
});

test("log settings reject unknown schemas and false local application claims", async () => {
  let settings = logSettingsFixture();
  const client = SecurityClient.withTransport(
    createRouterTransport(({ service }) =>
      service(LanternSecurityService, {
        getLogSettings: () => settings,
      }),
    ),
  );
  for (const edit of [
    () => {
      settings.schemaVersion = 2;
    },
    () => {
      settings.member = 0;
    },
    () => {
      settings.effectiveLevel = 99;
    },
    () => {
      settings.source = 99;
    },
    () => {
      settings.error = LogSettingsError.UNSPECIFIED;
    },
    () => {
      settings.adoption = LogPolicyAdoptionState.MANAGED;
    },
    () => {
      settings.appliedGeneration = 1n;
    },
    () => {
      settings.source = LogSettingsSource.MANAGED;
    },
    () => {
      settings.appliedCut = settings.version!.currentCut;
    },
    () => {
      settings.application = LogSettingsApplicationState.PENDING;
    },
    () => {
      settings.application = LogSettingsApplicationState.ERROR;
    },
    () => {
      settings.error = LogSettingsError.APPLY_FAILED;
    },
    () => {
      settings.version = undefined;
    },
  ]) {
    settings = logSettingsFixture();
    edit();
    await expect(client.getLogSettings()).rejects.toThrow();
  }
});

test("fenced log preparation references are complete, bounded and profile-bound", async () => {
  let settings = logSettingsFixture();
  const client = SecurityClient.withTransport(
    createRouterTransport(({ service }) =>
      service(LanternSecurityService, { getLogSettings: () => settings }),
    ),
  );
  for (const edit of [
    () => {
      settings.preparation = undefined;
    },
    () => {
      settings.adoption = LogPolicyAdoptionState.UNADOPTED;
    },
    () => {
      settings.preparation!.changeId = undefined;
    },
    () => {
      settings.preparation!.changeId!.version = 2;
    },
    () => {
      settings.preparation!.changeId!.cohort = new Uint8Array(32).fill(99);
    },
    () => {
      settings.preparation!.changeId!.nonce = new Uint8Array(16);
    },
    () => {
      settings.preparation!.intentDigest = new Uint8Array(31).fill(4);
    },
    () => {
      settings.preparation!.actorDigest = new Uint8Array(32);
    },
  ]) {
    settings = logSettingsFixture();
    settings.adoption = LogPolicyAdoptionState.FENCED_UNADOPTED;
    settings.preparation = logPreparationFixture(settings);
    edit();
    await expect(client.getLogSettings()).rejects.toThrow();
  }
});
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

test("self user ID is optional, validated and never cached across accounts", async () => {
  let id: string | undefined;
  let kind = SecurityPrincipalKind.OIDC;
  let signedOut = false,
    calls = 0;
  const client = SecurityClient.withTransport(
    createRouterTransport(({ service }) =>
      service(LanternSecurityService, {
        getCurrentPrincipal: () => {
          calls++;
          if (signedOut) throw new ConnectError("signed out", Code.Unauthenticated);
          return { userId: id, identity: { kind } };
        },
      }),
    ),
  );
  for (const value of ["1".repeat(32), undefined, "2".repeat(32)]) {
    id = value;
    expect(await client.getCurrentUserId()).toBe(value);
  }
  for (const value of ["", "short", "0".repeat(32), "A".repeat(32)]) {
    id = value;
    await expect(client.getCurrentUserId()).rejects.toThrow();
  }
  kind = SecurityPrincipalKind.MACHINE;
  id = undefined;
  expect(await client.getCurrentUserId()).toBeUndefined();
  id = "1".repeat(32);
  await expect(client.getCurrentUserId()).rejects.toThrow();
  signedOut = true;
  await expect(client.getCurrentUserId()).rejects.toThrow();
  expect(calls).toBe(10);
  expect(currentUserId(undefined)).toBeUndefined();
  const session = parseBrowserSession(
    JSON.stringify({
      mode: "AUTH_MODE_OIDC",
      principal: { identity: { kind: "SECURITY_PRINCIPAL_KIND_OIDC" }, userId: "1".repeat(32) },
    }),
  );
  expect(currentUserId(session.principal)).toBe("1".repeat(32));
  expect(() => parseBrowserSession('{"principal":{"userId":""}}')).toThrow();
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
