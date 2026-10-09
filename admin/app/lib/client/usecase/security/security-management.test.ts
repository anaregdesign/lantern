import { describe, expect, test } from "bun:test";
import {
  CurrentSecurityProgress,
  CurrentSecurityDisposition,
  CurrentAuthorizationStopObservation,
  SecurityAuthorizationRequirement,
  SecurityAuthorizationState,
  currentOriginalBinding,
  type CurrentSecurityReview,
  type CurrentSecurityChangeResult,
  type CurrentSecurityInvocationRejected,
  type BeginSecurityChangeAuthorizationResponse,
} from "lantern-sdk/web";
import {
  profile,
  version,
  changes,
  review,
  prepare,
  result,
  acknowledgement,
  bytes,
} from "../../../../../test/current-security";
import {
  SecurityManagementController,
  SecurityChangeRecovery,
  type SecurityManagementPort,
} from "./security-management";
const expiry = {
  $typeName: "google.protobuf.Timestamp" as const,
  seconds: 3000000000n,
  nanos: 0,
};
function begin(): BeginSecurityChangeAuthorizationResponse {
  return {
    $typeName: "graph.v1.BeginSecurityChangeAuthorizationResponse",
    authorizationId: bytes(32, 8),
    startUrl: "https://admin.example/auth/management-authorization/example",
    expiresAt: expiry,
    currentProfile: profile,
    attemptAffinity: "node-process-attempt",
  };
}
function fixture(
  overrides: Partial<SecurityManagementPort> = {},
  scope = new AbortController(),
  recovery?: SecurityChangeRecovery,
  owner = "browser-session",
) {
  const port: SecurityManagementPort = {
    roles: async () => ({
      $typeName: "graph.v1.ListRolesResponse",
      roles: [],
      version,
      nextCursor: "",
    }),
    issuers: async () => ({
      $typeName: "graph.v1.ListIssuersResponse",
      issuers: [],
      version,
      nextCursor: "",
    }),
    users: async () => ({
      $typeName: "graph.v1.ListUsersResponse",
      users: [],
      version,
      nextCursor: "",
    }),
    templates: async () => ({
      $typeName: "graph.v1.GetRoleTemplatesResponse",
      roles: [],
      version,
      nextCursor: "",
    }),
    audit: async () => ({ records: [], version, nextCursor: "" }),
    validateIssuer: async () => true,
    explain: async () => ({
      $typeName: "graph.v1.ExplainAccessResponse",
      allowed: false,
      matches: [],
      version,
    }),
    prepare: async (draft) => prepare(draft),
    beginAuthorization: async () => begin(),
    authorization: async (_review, id, affinity) => {
      expect(affinity).toBe("node-process-attempt");
      return {
        $typeName: "graph.v1.GetSecurityChangeAuthorizationResponse",
        authorizationId: id,
        state: SecurityAuthorizationState.APPROVED,
        authorizationProof: bytes(32, 9),
        expiresAt: expiry,
        currentProfile: profile,
      };
    },
    openAuthorization: () => ({ navigate() {}, close() {} }),
    invocationRejected: () => undefined,
    apply: async (request) => acknowledgement(result(request.currentReview)),
    status: async (r) => result(r),
    failure: () => "unavailable",
    ...overrides,
  };
  return new SecurityManagementController(
    port,
    "roles",
    scope.signal,
    recovery,
    owner,
  );
}
async function ready(c: SecurityManagementController) {
  await c.load();
  await c.review("Delete reader", changes);
}
function refused(r = review()): CurrentSecurityInvocationRejected {
  return {
    $typeName: "graph.v1.CurrentSecurityInvocationRejected",
    profile: r.profile,
    changeId: r.changeId,
    intentDigest: r.intentDigest,
    purposeRequired: true,
  };
}

describe("current reviewed changes", () => {
  test("server mints the full identity and one ordinary Apply has no recent-auth gate", async () => {
    let sends = 0;
    const c = fixture({
      prepare: async (draft) => {
        expect(draft.changeId).toBeUndefined();
        expect(draft.actor).toBeUndefined();
        expect(draft.intentDigest.length).toBe(0);
        return prepare(draft);
      },
      apply: async (request) => {
        sends++;
        expect(request.currentReview).toEqual(review());
        expect(request.authorizationProof).toBeUndefined();
        return acknowledgement();
      },
    });
    await ready(c);
    await c.apply();
    await c.apply();
    expect(sends).toBe(1);
    expect(c.getSnapshot().mutation).toBe("applied");
    expect(c.getSnapshot().message).not.toContain("35 seconds");
    c.dispose();
  });
  for (const read of [
    "audit",
    "templates",
    "members",
    "reload",
    "cancel",
  ] as const) {
    for (const settle of ["resolve", "reject"] as const) {
      test(`${read} supersedes delayed Prepare ${settle} without dispatch`, async () => {
        let finish!: () => void,
          signal!: AbortSignal,
          sends = 0;
        const c = fixture({
          prepare: (draft, s) => {
            signal = s;
            return new Promise((resolve, reject) => {
              finish = () =>
                settle === "resolve"
                  ? resolve(prepare(draft))
                  : reject(new Error("late"));
            });
          },
          apply: async () => {
            sends++;
            return acknowledgement();
          },
        });
        await c.load();
        const pending = c.review("Delete", changes);
        if (read === "audit") await c.loadAudit();
        else if (read === "templates") await c.loadTemplates("tenant:");
        else if (read === "members") await c.loadRoleMembers("reader");
        else if (read === "reload") await c.load();
        else c.cancelReview();
        expect(signal.aborted).toBe(true);
        finish();
        await pending;
        await c.apply();
        expect(sends).toBe(0);
        expect(c.getSnapshot().review?.approval).not.toBe("ordinary");
        c.dispose();
      });
    }
  }
  test("refused or changed preparation cannot authorize Apply", async () => {
    for (const corrupt of [
      (r: CurrentSecurityReview) => {
        r.profile!.timeProfile = bytes(32, 99);
      },
      (r: CurrentSecurityReview) => {
        r.expectedCut!.fences = bytes(32, 99);
      },
      (r: CurrentSecurityReview) => {
        r.changes = [];
      },
      (r: CurrentSecurityReview) => {
        r.changeId!.namespace = 0n;
      },
      (r: CurrentSecurityReview) => {
        r.intentDigest = bytes(31);
      },
    ]) {
      let sends = 0;
      const c = fixture({
        prepare: async (draft) => {
          const response = prepare(draft);
          corrupt(response.currentReview!);
          return response;
        },
        apply: async () => {
          sends++;
          return acknowledgement();
        },
      });
      await ready(c);
      await c.apply();
      expect(sends).toBe(0);
      expect(c.changeId()).toBe("");
      c.dispose();
    }
  });
  test("purpose keeps exact review, process affinity and one-use proof separate from recovery", async () => {
    const recovery = new SecurityChangeRecovery();
    let sends = 0,
      navigated = "";
    const c = fixture(
      {
        prepare: async (draft) =>
          prepare(draft, SecurityAuthorizationRequirement.REAUTHENTICATION),
        openAuthorization: () => ({
          navigate: (url) => {
            navigated = url;
          },
          close() {},
        }),
        beginAuthorization: async (r) => {
          expect(r).toEqual(review());
          return begin();
        },
        apply: async (request) => {
          sends++;
          expect(request.authorizationProof).toEqual(bytes(32, 9));
          expect(recovery.read("browser-session")?.review).toEqual(review());
          throw new Error("lost");
        },
      },
      undefined,
      recovery,
    );
    await ready(c);
    await c.apply();
    expect(sends).toBe(0);
    await c.authorize();
    expect(navigated).toContain("/auth/");
    await c.checkAuthorization();
    await c.apply();
    expect(sends).toBe(1);
    expect(c.getSnapshot().mutation).toBe("unconfirmed");
    expect("authorizationProof" in recovery.read("browser-session")!).toBe(
      false,
    );
    await c.apply();
    expect(sends).toBe(1);
    c.dispose();
  });
  for (const supersede of ["audit", "reload", "cancel"] as const) {
    test(`${supersede} supersedes delayed Begin and closes only its popup`, async () => {
      let finish!: (value: BeginSecurityChangeAuthorizationResponse) => void,
        closed = 0,
        navigated = 0;
      const c = fixture({
        prepare: async (draft) =>
          prepare(draft, SecurityAuthorizationRequirement.REAUTHENTICATION),
        beginAuthorization: () =>
          new Promise((resolve) => {
            finish = resolve;
          }),
        openAuthorization: () => ({
          navigate() {
            navigated++;
          },
          close() {
            closed++;
          },
        }),
      });
      await ready(c);
      const pending = c.authorize();
      if (supersede === "audit") await c.loadAudit();
      else if (supersede === "reload") await c.load();
      else c.cancelReview();
      finish(begin());
      await pending;
      expect(closed).toBeGreaterThan(0);
      expect(navigated).toBe(0);
      expect(c.getSnapshot().review?.approval).not.toBe("starting");
      c.dispose();
    });
  }
  test("cancelled or wrong-profile approval cannot publish proof into another review", async () => {
    let finish!: (
      value: Awaited<ReturnType<SecurityManagementPort["authorization"]>>,
    ) => void;
    const c = fixture({
      prepare: async (draft) =>
        prepare(draft, SecurityAuthorizationRequirement.REAUTHENTICATION),
      authorization: () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    });
    await ready(c);
    await c.authorize();
    const pending = c.checkAuthorization();
    c.cancelReview();
    await c.review("New", changes);
    finish({
      $typeName: "graph.v1.GetSecurityChangeAuthorizationResponse",
      authorizationId: bytes(32, 8),
      state: SecurityAuthorizationState.APPROVED,
      authorizationProof: bytes(32, 9),
      expiresAt: expiry,
      currentProfile: profile,
    });
    await pending;
    expect(c.getSnapshot().review?.authorizationProof).toBeUndefined();
    c.dispose();
  });
  test("only a bound first purpose refusal allows the same reviewed identity to be approved", async () => {
    let sends = 0;
    const c = fixture({
      apply: async () => {
        sends++;
        throw new Error("purpose");
      },
      invocationRejected: () => refused(),
    });
    await ready(c);
    await c.apply();
    expect(c.getSnapshot().review?.contract).toEqual(review());
    expect(c.getSnapshot().review?.approval).toBe("required");
    expect(c.changeId()).toBe("");
    await c.authorize();
    await c.checkAuthorization();
    await c.apply();
    expect(sends).toBe(2);
    c.dispose();
  });
  test("malformed or different identity refusals keep the original uncertain record", async () => {
    for (const corrupt of [
      (r: ReturnType<typeof refused>) => {
        r.profile!.configuration = bytes(32, 99);
      },
      (r: ReturnType<typeof refused>) => {
        r.changeId!.namespace++;
      },
      (r: ReturnType<typeof refused>) => {
        r.changeId!.nonce = bytes(16, 99);
      },
      (r: ReturnType<typeof refused>) => {
        r.intentDigest = bytes(32, 99);
      },
      (r: ReturnType<typeof refused>) => {
        r.purposeRequired = false;
      },
    ]) {
      const detail = structuredClone(refused());
      corrupt(detail);
      let sends = 0;
      const c = fixture({
        apply: async () => {
          sends++;
          throw new Error("unbound");
        },
        invocationRejected: () => detail,
      });
      await ready(c);
      await c.apply();
      await c.review("Other", changes);
      await c.apply();
      expect(sends).toBe(1);
      expect(c.getSnapshot().mutation).toBe("unconfirmed");
      c.dispose();
    }
  });
  for (const progress of [
    CurrentSecurityProgress.UNRESOLVED,
    CurrentSecurityProgress.ORIGIN_DURABLE,
    CurrentSecurityProgress.CHOSEN,
  ]) {
    test(`stage ${progress} retains full identity across remount and never creates or resends a mutation`, async () => {
      const recovery = new SecurityChangeRecovery();
      let sends = 0,
        prepares = 0;
      const c = fixture(
        {
          prepare: async (d) => {
            prepares++;
            return prepare(d);
          },
          apply: async (r) => {
            sends++;
            return acknowledgement(result(r.currentReview, progress));
          },
        },
        undefined,
        recovery,
      );
      await ready(c);
      await c.apply();
      await c.load();
      await c.review("Other", changes);
      await c.apply();
      expect(sends).toBe(1);
      expect(prepares).toBe(1);
      c.dispose();
      const after = fixture(
        {
          status: async (r) => {
            expect(currentOriginalBinding(r)).toBe(
              currentOriginalBinding(review()),
            );
            return result(r);
          },
          apply: async () => {
            throw new Error("unexpected replay");
          },
        },
        undefined,
        recovery,
      );
      await after.checkStatus();
      expect(after.getSnapshot().mutation).toBe("applied");
      expect(after.getSnapshot().result?.original?.items).toHaveLength(1);
      after.dispose();
    });
  }
  test("response loss retains review before dispatch and transport failure is not nonexecution", async () => {
    const recovery = new SecurityChangeRecovery();
    let sends = 0;
    const c = fixture(
      {
        apply: async (r) => {
          sends++;
          expect(recovery.read("browser-session")?.review).toEqual(
            r.currentReview,
          );
          throw new Error("lost");
        },
        status: async (r) => result(r, CurrentSecurityProgress.UNRESOLVED),
      },
      undefined,
      recovery,
    );
    await ready(c);
    await c.apply();
    await c.checkStatus();
    await c.review("new", changes);
    await c.apply();
    expect(sends).toBe(1);
    expect(c.getSnapshot().mutation).toBe("unconfirmed");
    c.dispose();
  });
  test("CAS and other original rejections are terminal evidence, requiring a fresh explicit review", async () => {
    for (const disposition of [
      CurrentSecurityDisposition.REJECTED_CAS,
      CurrentSecurityDisposition.REJECTED_ADMIN,
      CurrentSecurityDisposition.REJECTED_AUTHORITY,
      CurrentSecurityDisposition.REJECTED_PURPOSE,
      CurrentSecurityDisposition.REJECTED_CAPACITY,
      CurrentSecurityDisposition.REJECTED_INVARIANT,
    ]) {
      const c = fixture({
        apply: async (r) =>
          acknowledgement(
            result(
              r.currentReview,
              CurrentSecurityProgress.APPLIED,
              disposition,
            ),
          ),
      });
      await ready(c);
      await c.apply();
      expect(c.getSnapshot().mutation).toBe("rejected");
      expect(c.getSnapshot().version).toBeUndefined();
      expect(c.getSnapshot().review).toBeUndefined();
      await c.load();
      await c.review("Corrected", changes);
      expect(c.getSnapshot().review?.approval).toBe("ordinary");
      c.dispose();
    }
  });
  test("status preserves original evidence and rejects every changed identity and outcome", async () => {
    for (const corrupt of [
      (r: CurrentSecurityChangeResult) => {
        r.changeId!.namespace++;
      },
      (r: CurrentSecurityChangeResult) => {
        r.profile!.membership = bytes(32, 99);
      },
      (r: CurrentSecurityChangeResult) => {
        r.intentDigest = bytes(31);
      },
      (r: CurrentSecurityChangeResult) => {
        r.original!.commit!.value = bytes(32, 99);
      },
      (r: CurrentSecurityChangeResult) => {
        r.original!.resultingCut!.frontier = bytes(32, 99);
      },
      (r: CurrentSecurityChangeResult) => {
        r.original!.items[0].disposition =
          CurrentSecurityDisposition.REJECTED_CAS;
      },
      (r: CurrentSecurityChangeResult) => {
        r.progress = CurrentSecurityProgress.CHOSEN;
      },
    ]) {
      const c = fixture({
        status: async (r) => {
          const next = result(r);
          corrupt(next);
          return next;
        },
      });
      await ready(c);
      await c.apply();
      const prior = structuredClone(c.getSnapshot().result);
      await c.checkStatus();
      expect(c.getSnapshot().result).toEqual(prior);
      expect(c.getSnapshot().message).toContain("unavailable");
      c.dispose();
    }
  });
  test("weaker local status cannot erase proof and native stop observation is separate from Apply", async () => {
    let calls = 0;
    const c = fixture({
      status: async (r) =>
        ++calls === 1
          ? result(r, CurrentSecurityProgress.UNRESOLVED)
          : {
              ...result(r),
              stopObservation:
                CurrentAuthorizationStopObservation.OLD_CUT_NEW_AUTHORIZATIONS_STOPPED,
            },
    });
    await ready(c);
    await c.apply();
    await c.checkStatus();
    expect(c.getSnapshot().result?.original).toBeDefined();
    await c.checkStatus();
    expect(c.getSnapshot().message).toContain(
      "previously authorized output may still arrive",
    );
    c.dispose();
  });
  test("expired scope and superseded status cannot restore stale evidence or views", async () => {
    let finish!: (value: CurrentSecurityChangeResult) => void;
    const scope = new AbortController();
    const c = fixture(
      {
        status: () =>
          new Promise((resolve) => {
            finish = resolve;
          }),
      },
      scope,
    );
    await ready(c);
    await c.apply();
    const checking = c.checkStatus();
    await c.loadAudit();
    finish({
      ...result(),
      stopObservation:
        CurrentAuthorizationStopObservation.OLD_CUT_NEW_AUTHORIZATIONS_STOPPED,
    });
    await checking;
    expect(c.getSnapshot().result?.stopObservation).toBe(
      CurrentAuthorizationStopObservation.NOT_OBSERVED,
    );
    scope.abort();
    await expect(c.load()).rejects.toThrow();
    expect(c.getSnapshot().phase).toBe("ready");
    c.dispose();
  });
  test("full cut and credential binding partition membership pages", async () => {
    for (const changed of [
      { ...version, admissionBinding: bytes(32, 99) },
      {
        ...version,
        currentCut: { ...version.currentCut!, fences: bytes(32, 99) },
      },
      {
        ...version,
        currentProfile: { ...profile, timeProfile: bytes(32, 99) },
      },
    ]) {
      const c = fixture({
        users: async () => ({
          $typeName: "graph.v1.ListUsersResponse",
          users: [],
          version: changed,
          nextCursor: "",
        }),
      });
      await c.load();
      await c.loadRoleMembers("reader");
      expect(c.getSnapshot().memberRole).toBe("");
      expect(c.getSnapshot().message).toContain("policy changed");
      c.dispose();
    }
  });
  test("different browser owner cannot inherit an original recovery record", async () => {
    const recovery = new SecurityChangeRecovery();
    const c = fixture({}, undefined, recovery);
    await ready(c);
    await c.apply();
    c.dispose();
    const other = fixture({}, undefined, recovery, "other-session");
    expect(other.changeId()).toBe("");
    other.dispose();
    expect(recovery.read("browser-session")?.review).toEqual(review());
  });
});
