import { describe, expect, test } from "bun:test";
import {
  AuthMode,
  parseBrowserSession,
  type GetAuthCapabilitiesResponse,
  type BrowserSession,
} from "lantern-sdk/web";
import { AuthController, type AuthGateway } from "./auth-state";

const now = Date.now();
function capabilities(mode = AuthMode.OIDC): GetAuthCapabilitiesResponse {
  return {
    $typeName: "graph.v1.GetAuthCapabilitiesResponse",
    mode,
    protocolVersion: 1,
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
  return parseBrowserSession(
    JSON.stringify({
      mode: "AUTH_MODE_OIDC",
      principal: {
        identity: {
          kind: "SECURITY_PRINCIPAL_KIND_OIDC",
          issuer: "https://idp.example",
          subject: "alice",
        },
        version: {
          revision,
          digest: btoa("d".repeat(32)),
          generation: btoa("g".repeat(16)),
        },
        expiresAt: new Date(now + 25_000).toISOString(),
        csrfToken: csrf,
      },
    }),
  );
}
function fixture(overrides: Partial<AuthGateway> = {}) {
  return new AuthController(
    {
      sameOriginHTTPS: true,
      capabilities: async () => capabilities(),
      session: async () => session(),
      logout: async () => {},
      login: () => {},
      ...overrides,
    },
    () => now,
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
