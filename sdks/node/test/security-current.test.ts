import { expect, test } from "bun:test";
import { clone, create } from "@bufbuild/protobuf";
import { createRouterTransport, ConnectError, Code } from "@connectrpc/connect";
import { SecurityClient, CurrentSecurityInvocationRejectedError } from "../src/security.js";
import {
  copySecurityContract,
  currentProfileBinding,
  currentCutBinding,
  currentSecurityVersionBinding,
  currentOriginalBinding,
  validateCurrentResult,
  validateCurrentReview,
  reconcileCurrentResult,
  validatePreparedCurrentReview,
} from "../src/security-current.js";
import {
  CurrentAuthorityProfileSchema,
  CurrentSemanticCutSchema,
  CurrentSecurityReviewSchema,
  CurrentSecurityChangeResultSchema,
  CurrentSecurityInvocationRejectedSchema,
  SecurityVersionSchema,
  LanternSecurityService,
  CurrentSecurityProgress,
  CurrentSecurityDisposition,
  CurrentAuthorizationStopObservation,
  SecurityPrincipalKind,
  SecurityAuthorizationRequirement,
} from "../src/gen/graph/v1/security_pb.js";
const bytes = (size = 32, value = 1) => new Uint8Array(size).fill(value);
function fixture() {
  const profile = create(CurrentAuthorityProfileSchema, {
    version: 2,
    domain: bytes(),
    cohort: bytes(32, 2),
    generation: bytes(16, 3),
    protocol: bytes(32, 4),
    timeProfile: bytes(32, 5),
    membership: bytes(32, 6),
    configuration: bytes(32, 7),
  });
  const cut = create(CurrentSemanticCutSchema, {
    version: 1,
    domain: profile.domain.slice(),
    cohort: profile.cohort.slice(),
    generation: profile.generation.slice(),
    sequence: 3n,
    previous: bytes(32, 0),
    projection: bytes(32, 8),
    frontier: bytes(32, 9),
    fences: bytes(32, 10),
    policy: bytes(32, 11),
  });
  const review = clone(
    CurrentSecurityReviewSchema,
    create(CurrentSecurityReviewSchema, {
      profile,
      expectedCut: cut,
      changeId: {
        version: 1,
        domain: profile.domain,
        cohort: profile.cohort,
        namespace: 9n,
        nonce: bytes(16, 12),
      },
      actor: { kind: SecurityPrincipalKind.OIDC, issuer: "https://idp.example", subject: "human" },
      intentDigest: bytes(32, 13),
      changes: [{ operation: { case: "deleteRole", value: "reader" } }],
    }),
  );
  const result = clone(
    CurrentSecurityChangeResultSchema,
    create(CurrentSecurityChangeResultSchema, {
      profile,
      changeId: review.changeId,
      intentDigest: review.intentDigest,
      progress: CurrentSecurityProgress.APPLIED,
      stopObservation: CurrentAuthorizationStopObservation.NOT_OBSERVED,
      original: {
        changeId: review.changeId,
        intentDigest: review.intentDigest,
        handoffDigest: bytes(32, 14),
        commit: {
          version: 1,
          domain: profile.domain,
          cohort: profile.cohort,
          membership: bytes(32, 15),
          configuration: bytes(32, 16),
          slot: 19n,
          value: bytes(32, 17),
        },
        disposition: CurrentSecurityDisposition.APPLIED,
        items: [{ index: 0, kind: "delete_role", disposition: CurrentSecurityDisposition.APPLIED }],
        observedCut: cut,
        resultingCut: { ...cut, sequence: 4n, projection: bytes(32, 18) },
      },
    }),
  );
  return { profile, cut, review, result };
}
test("current cache keys cover all profile and cut fields plus credential binding", () => {
  const { profile, cut } = fixture();
  for (const key of [
    "domain",
    "cohort",
    "generation",
    "protocol",
    "timeProfile",
    "membership",
    "configuration",
  ] as const) {
    const other = clone(CurrentAuthorityProfileSchema, profile);
    other[key][0] ^= 1;
    expect(currentProfileBinding(other)).not.toBe(currentProfileBinding(profile));
  }
  for (const key of ["previous", "projection", "frontier", "fences", "policy"] as const) {
    const other = clone(CurrentSemanticCutSchema, cut);
    other[key][0] ^= 1;
    expect(currentCutBinding(other, profile)).not.toBe(currentCutBinding(cut, profile));
  }
  const version = create(SecurityVersionSchema, {
    currentProfile: profile,
    currentCut: cut,
    admissionBinding: bytes(32, 19),
  });
  expect(currentSecurityVersionBinding({ ...version, admissionBinding: bytes(32, 20) })).not.toBe(
    currentSecurityVersionBinding(version),
  );
  expect(() => currentSecurityVersionBinding({ ...version, revision: 1n })).toThrow();
  expect(() => currentProfileBinding({ ...profile, version: 3 })).toThrow();
  expect(() => currentCutBinding({ ...cut, generation: bytes(16, 99) }, profile)).toThrow();
});
test("prepared reviews preserve exact command order and every inspected cut field", () => {
  const { review } = fixture();
  validatePreparedCurrentReview(review, review);
  expect(() => validatePreparedCurrentReview(review, { ...review, changes: [] })).toThrow();
  expect(() =>
    validatePreparedCurrentReview(review, {
      ...review,
      expectedCut: { ...review.expectedCut!, frontier: bytes(32, 99) },
    }),
  ).toThrow();
  expect(() =>
    currentOriginalBinding({ ...review, changeId: { ...review.changeId!, namespace: 0n } }),
  ).toThrow();
});
test("unresolved, durable, chosen, Apply and stop observation remain distinct", () => {
  const { review, result } = fixture();
  validateCurrentResult(result, review);
  for (const progress of [
    CurrentSecurityProgress.UNRESOLVED,
    CurrentSecurityProgress.ORIGIN_DURABLE,
    CurrentSecurityProgress.CHOSEN,
  ]) {
    const stage = { ...result, progress, original: undefined };
    validateCurrentResult(stage, review);
    expect(reconcileCurrentResult(result, stage, review).original).toEqual(result.original);
    expect(() =>
      validateCurrentResult(
        { ...stage, stopObservation: CurrentAuthorizationStopObservation.WAITING },
        review,
      ),
    ).toThrow();
  }
  for (const field of ["handoffDigest", "intentDigest"] as const) {
    const altered = clone(CurrentSecurityChangeResultSchema, result);
    altered.original![field][0] ^= 1;
    expect(() => reconcileCurrentResult(result, altered, review)).toThrow();
  }
  expect(() =>
    validateCurrentResult({ ...result, progress: CurrentSecurityProgress.CHOSEN }, review),
  ).toThrow();
  expect(() => validateCurrentResult({ ...result, original: undefined }, review)).toThrow();
  expect(() =>
    validateCurrentResult({ ...result, changeId: { ...result.changeId!, namespace: 10n } }, review),
  ).toThrow();
  const detached = reconcileCurrentResult(undefined, result, review);
  detached.original!.commit!.value[0] ^= 1;
  expect(detached.original!.commit!.value).not.toEqual(result.original!.commit!.value);
});
test("current SDK makes one Apply and correlates only an exact single v2 invocation refusal", async () => {
  for (const malformed of [false, true]) {
    const { review } = fixture();
    let sends = 0;
    const client = SecurityClient.withTransport(
      createRouterTransport(({ service }) =>
        service(LanternSecurityService, {
          applySecurityChanges: (request) => {
            sends++;
            expect(request.currentReview).toEqual(review);
            const detail = create(CurrentSecurityInvocationRejectedSchema, {
              profile: review.profile,
              changeId: review.changeId,
              intentDigest: review.intentDigest,
              purposeRequired: true,
            });
            if (malformed) detail.changeId = { ...detail.changeId!, namespace: 10n };
            throw new ConnectError("purpose", Code.FailedPrecondition, undefined, [
              { desc: CurrentSecurityInvocationRejectedSchema, value: detail },
            ]);
          },
        }),
      ),
    );
    try {
      await client.applySecurityChanges({ currentReview: review });
      throw new Error("expected refusal");
    } catch (error) {
      expect(error instanceof CurrentSecurityInvocationRejectedError).toBe(!malformed);
    }
    expect(sends).toBe(1);
  }
});
test("current SDK preserves server-minted review and status and rejects mismatched originals", async () => {
  const { review, result } = fixture();
  let sends = 0,
    lookups = 0;
  const client = SecurityClient.withTransport(
    createRouterTransport(({ service }) =>
      service(LanternSecurityService, {
        prepareSecurityChanges: (request) => {
          expect(request.currentReview?.changeId).toBeUndefined();
          return { currentReview: review, requirement: SecurityAuthorizationRequirement.ORDINARY };
        },
        applySecurityChanges: () => {
          sends++;
          return { currentResult: result };
        },
        getSecurityChangeStatus: (request) => {
          lookups++;
          expect(request.currentChangeId).toEqual(review.changeId);
          return { currentResult: { ...result, intentDigest: bytes(32, 99) } };
        },
      }),
    ),
  );
  const prepared = await client.prepareSecurityChanges({
    currentReview: {
      profile: review.profile,
      expectedCut: review.expectedCut,
      changes: review.changes,
    },
  });
  expect(prepared.currentReview).toEqual(review);
  expect(
    (await client.applySecurityChanges({ currentReview: review })).currentResult?.original,
  ).toEqual(result.original);
  await expect(
    client.getSecurityChangeStatus({
      currentProfile: review.profile,
      currentChangeId: review.changeId,
      currentIntentDigest: review.intentDigest,
    }),
  ).rejects.toThrow();
  expect(sends).toBe(1);
  expect(lookups).toBe(1);
});

test("retained real Node pooled-byte contracts are detached and portable", () => {
  const { review } = fixture();
  const pooled = Buffer.allocUnsafe(128);
  pooled.fill(7);
  review.profile!.protocol = pooled.subarray(16, 48);
  const detached = copySecurityContract({ review });
  expect(detached.review.profile!.protocol).toEqual(new Uint8Array(32).fill(7));
  pooled.fill(9);
  expect(detached.review.profile!.protocol).toEqual(new Uint8Array(32).fill(7));
  expect(() => validateCurrentReview(detached.review)).not.toThrow();
});
