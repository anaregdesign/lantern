import { clone } from "@bufbuild/protobuf";
import {
  BrowserSessionSchema,
  CurrentSessionRevocationReviewSchema,
} from "../../../../../../sdks/node/src/gen/graph/v1/security_pb";
import { describe, expect, test } from "bun:test";
import {
  AuthMode,
  SecurityPrincipalKind,
  SecurityEnforcementState,
  type SessionRevocation,
  type CurrentSessionRevocationReview,
  type GetAuthCapabilitiesResponse,
  type BrowserSession,
} from "lantern-sdk/web";
import {
  AuthController,
  SessionRevocationRecovery,
  type AuthGateway,
} from "./auth-state";

import {
  profile,
  cut,
  version,
  review,
  result,
} from "../../../../../test/current-security";

const now = Date.now();
function capabilities(mode = AuthMode.OIDC): GetAuthCapabilitiesResponse {
  return {
    $typeName: "graph.v1.GetAuthCapabilitiesResponse",
    mode,
    protocolVersion: mode === AuthMode.OIDC ? 2 : 1,
    currentProfile: mode === AuthMode.OIDC ? profile : undefined,
    currentOriginEnabled: mode === AuthMode.OIDC,
    currentMember: 1,
    ready: true,
    loginPath: mode === AuthMode.OIDC ? "/auth/login" : "",
    loginIssuers:
      mode === AuthMode.OIDC
        ? [
            {
              $typeName: "graph.v1.LoginIssuer",
              issuer: "https://idp.example",
              label: "Example",
            },
          ]
        : [],
  };
}
function session(revision = "1", csrf = "A".repeat(43)): BrowserSession {
  return clone(BrowserSessionSchema, {
    $typeName: "graph.v1.BrowserSession",
    mode: AuthMode.OIDC,
    currentProfile: profile,
    principal: {
      $typeName: "graph.v1.GetCurrentPrincipalResponse",
      identity: {
        $typeName: "graph.v1.SecurityIdentity",
        kind: SecurityPrincipalKind.OIDC,
        issuer: "https://idp.example",
        subject: "alice",
        machineName: "",
      },
      roles: [],
      version: {
        ...version,
        currentCut: { ...cut, sequence: BigInt(revision) },
      },
      recentAuthentication: false,
      expiresAt: {
        $typeName: "google.protobuf.Timestamp",
        seconds: BigInt(Math.floor((now + 25000) / 1000)),
        nanos: ((now + 25000) % 1000) * 1e6,
      },
      csrfToken: csrf,
    },
  });
}
function sessionReview(): CurrentSessionRevocationReview {
  const r = review();
  return clone(CurrentSessionRevocationReviewSchema, {
    $typeName: "graph.v1.CurrentSessionRevocationReview",
    profile: r.profile,
    expectedCut: { ...cut, sequence: 1n },
    changeId: r.changeId,
    actor: r.actor,
    intentDigest: r.intentDigest,
    sessionDigest: "a".repeat(64),
    sessionLineage: 1n,
  });
}
function revocation(extra: Partial<SessionRevocation> = {}): SessionRevocation {
  return {
    $typeName: "graph.v1.SessionRevocation",
    enforcement: SecurityEnforcementState.UNSPECIFIED,
    localCookieCleared: false,
    ...extra,
  };
}

function fixture(
  overrides: Partial<AuthGateway> = {},
  recovery?: SessionRevocationRecovery,
) {
  return new AuthController(
    {
      sameOriginHTTPS: true,
      capabilities: async () => capabilities(),
      session: async () => session(),
      logout: async (request) =>
        request.prepareOnly
          ? revocation({ currentReview: sessionReview() })
          : revocation({ localCookieCleared: true }),
      logoutStatus: async () => result(),
      login: () => {},
      ...overrides,
    },
    () => now,
    recovery,
  );
}
describe("Admin authority lifetime", () => {
  test("scope changes abort the old display generation without extending its authority", async () => {
    let reads = 0;
    let release!: (value: BrowserSession) => void;
    const short = session();
    short.principal!.expiresAt = {
      $typeName: "google.protobuf.Timestamp",
      seconds: BigInt(Math.floor((now + 1150) / 1000)),
      nanos: ((now + 1150) % 1000) * 1e6,
    };
    const controller = fixture({
      session: async () =>
        ++reads === 1
          ? short
          : new Promise<BrowserSession>((done) => {
              release = done;
            }),
    });
    await controller.refresh();
    const before = controller.getSnapshot();
    if (before.kind !== "ready") throw new Error("ready expected");
    controller.selectScope("orders:");
    const scoped = controller.getSnapshot();
    expect(before.signal.aborted).toBe(true);
    expect(scoped.epoch).toBe(before.epoch + 1);
    controller.selectScope("orders:");
    expect(controller.getSnapshot()).toBe(scoped);
    await new Promise<void>((done) => setTimeout(done, 180));
    expect(controller.getSnapshot().kind).toBe("checking");
    expect(scoped.kind === "ready" && scoped.signal.aborted).toBe(true);
    release(session());
    await Promise.resolve();
    controller.dispose();
  });
  test("resume hides protected data before a slow or unavailable session check completes", async () => {
    let fail!: (error: Error) => void;
    let resumed = false;
    const controller = fixture({
      session: () =>
        resumed
          ? new Promise<BrowserSession>((_, reject) => {
              fail = reject;
            })
          : Promise.resolve(session()),
    });
    await controller.refresh();
    const before = controller.getSnapshot();
    resumed = true;
    const pending = controller.refresh(true);
    expect(controller.getSnapshot().kind).toBe("checking");
    expect(before.kind === "ready" && before.signal.aborted).toBe(true);
    await Promise.resolve();
    fail(new Error("session unavailable"));
    await pending;
    expect(controller.getSnapshot().kind).toBe("error");
    controller.dispose();
  });
  test("only supported explicit OFF opens data without session requests", async () => {
    let calls = 0;
    const controller = fixture({
      capabilities: async () => capabilities(AuthMode.OFF),
      session: async () => {
        calls++;
        return null;
      },
    });
    await controller.refresh();
    expect(controller.getSnapshot().kind).toBe("off");
    expect(calls).toBe(0);
    controller.dispose();
  });
  test("unavailable, unknown and malformed responses never become OFF", async () => {
    for (const caps of [
      { ...capabilities(), ready: false },
      { ...capabilities(AuthMode.OFF), protocolVersion: 2 },
      { ...capabilities(), mode: AuthMode.UNSPECIFIED },
    ]) {
      const controller = fixture({ capabilities: async () => caps });
      await controller.refresh();
      expect(controller.getSnapshot().kind).toBe("error");
      controller.dispose();
    }
    const failed = fixture({
      capabilities: async () => {
        throw new Error("network failure");
      },
    });
    await failed.refresh();
    expect(failed.getSnapshot().kind).toBe("error");
    failed.dispose();
  });
  test("OIDC requires same-origin HTTPS, then login if the session is absent", async () => {
    const remote = fixture({ sameOriginHTTPS: false });
    await remote.refresh();
    expect(remote.getSnapshot().kind).toBe("error");
    remote.dispose();
    const absent = fixture({ session: async () => null });
    await absent.refresh();
    expect(absent.getSnapshot().kind).toBe("login");
    absent.dispose();
  });
  test("changed policy aborts old work and increments the display partition", async () => {
    let revision = "1";
    const controller = fixture({ session: async () => session(revision) });
    await controller.refresh();
    const before = controller.getSnapshot();
    if (before.kind !== "ready") throw new Error("ready expected");
    await controller.refresh();
    expect(controller.getSnapshot().epoch).toBe(before.epoch);
    expect(before.signal.aborted).toBe(false);
    revision = "2";
    await controller.refresh();
    expect(before.signal.aborted).toBe(true);
    expect(controller.getSnapshot().epoch).toBeGreaterThan(before.epoch);
    controller.dispose();
  });
  test("logout response loss cannot restore the old session on refresh", async () => {
    let csrf = "A".repeat(43);
    const controller = fixture({
      session: async () => session("1", csrf),
      logout: async () => {
        throw new Error("lost response");
      },
    });
    await controller.refresh();
    const before = controller.getSnapshot();
    await controller.logout();
    expect(before.kind === "ready" && before.signal.aborted).toBe(true);
    await controller.refresh();
    expect(controller.getSnapshot().kind).toBe("login");
    csrf = "B".repeat(43);
    await controller.refresh();
    expect(controller.getSnapshot().kind).toBe("ready");
    controller.dispose();
  });
  test("late previous-gateway completion cannot restore a disposed controller", async () => {
    let resolve!: (value: BrowserSession) => void;
    const controller = fixture({
      session: () =>
        new Promise<BrowserSession>((done) => {
          resolve = done;
        }),
    });
    const pending = controller.refresh();
    await Promise.resolve();
    await Promise.resolve();
    controller.dispose();
    resolve(session());
    await pending;
    expect(controller.getSnapshot().kind).toBe("checking");
  });
});

test("logout retains review before Apply, separates local deletion, and uses status after sign-in", async () => {
  let csrf = "A".repeat(43),
    applies = 0,
    prepares = 0,
    local = 0;
  const controller = fixture({
    session: async () => session("1", csrf),
    logout: async (request) => {
      if (request.prepareOnly) {
        prepares++;
        return revocation({ currentReview: sessionReview() });
      }
      if (request.localOnly) {
        local++;
        return revocation({ localCookieCleared: true });
      }
      applies++;
      expect(controller.hasLogoutRecord()).toBe(true);
      expect(request.review).toEqual(sessionReview());
      throw new Error("lost Apply response");
    },
    logoutStatus: async (r) => {
      expect(r).toEqual(sessionReview());
      return result({ ...review(), expectedCut: r.expectedCut });
    },
  });
  await controller.refresh();
  await controller.logout();
  expect(applies).toBe(1);
  expect(prepares).toBe(1);
  expect(local).toBe(1);
  expect(controller.getLogoutMessage()).toContain(
    "Cluster revocation is unconfirmed",
  );
  expect(controller.getSnapshot().kind).toBe("login");
  csrf = "B".repeat(43);
  await controller.refresh();
  await controller.checkLogoutStatus();
  expect(controller.getLogoutMessage()).toContain(
    "does not describe the new session",
  );
  expect(applies).toBe(1);
  controller.dispose();
});
test("full current profile, cut and credential binding invalidate granting views", async () => {
  let current = session();
  const controller = fixture({ session: async () => current });
  await controller.refresh();
  for (const change of [
    () => {
      current.principal!.version!.currentCut!.fences = new Uint8Array(32).fill(
        77,
      );
    },
    () => {
      current.principal!.version!.admissionBinding = new Uint8Array(32).fill(
        78,
      );
    },
  ]) {
    const prior = controller.getSnapshot();
    current = structuredClone(current);
    change();
    await controller.refresh();
    expect(prior.kind === "ready" && prior.signal.aborted).toBe(true);
  }
  current = structuredClone(current);
  current.currentProfile!.timeProfile = new Uint8Array(32).fill(99);
  await controller.refresh();
  expect(controller.getSnapshot().kind).toBe("error");
  controller.dispose();
});
test("current-v2 refuses scalar legacy sessions and ordinary long-lived credentials need no recent auth", async () => {
  const long = session();
  long.principal!.expiresAt!.seconds += 3600n;
  const controller = fixture({ session: async () => long });
  await controller.refresh();
  expect(controller.getSnapshot().kind).toBe("ready");
  controller.dispose();
  const legacy = session();
  legacy.principal!.version = { ...version, revision: 1n };
  const refused = fixture({ session: async () => legacy });
  await refused.refresh();
  expect(refused.getSnapshot().kind).toBe("error");
  refused.dispose();
});

test("logout recovery survives gateway disposal and retains each new session operation", async () => {
  const recovery = new SessionRevocationRecovery();
  let applies = 0,
    statuses = 0;
  const original = fixture(
    {
      logout: async (request) => {
        if (request.prepareOnly)
          return revocation({ currentReview: sessionReview() });
        if (request.localOnly) return revocation({ localCookieCleared: true });
        applies++;
        throw new Error("lost response");
      },
    },
    recovery,
  );
  await original.refresh();
  await original.logout();
  original.dispose();
  const next = fixture(
    {
      session: async () => session("1", "B".repeat(43)),
      logoutStatus: async (retained) => {
        statuses++;
        expect(retained).toEqual(sessionReview());
        return result({ ...review(), expectedCut: retained.expectedCut });
      },
      logout: async (request) => {
        if (request.prepareOnly) {
          const nextReview = sessionReview();
          nextReview.sessionDigest = "b".repeat(64);
          nextReview.changeId!.nonce = new Uint8Array(16).fill(51);
          return revocation({ currentReview: nextReview });
        }
        applies++;
        return revocation({ localCookieCleared: true });
      },
    },
    recovery,
  );
  await next.refresh();
  expect(next.hasLogoutRecord()).toBe(true);
  await next.checkLogoutStatus();
  expect(statuses).toBe(1);
  expect(applies).toBe(1);
  await next.logout();
  expect(applies).toBe(2);
  expect(recovery.forPrincipal(session().principal!)).toHaveLength(2);
  next.dispose();
});
