// Explicit opt-in child of the owned Go native fixture. This is not a mocked
// Playwright spec and does not start another Server or native time owner.
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import {
  createReadStream,
  existsSync,
  lstatSync,
  readFileSync,
  realpathSync,
  writeFileSync,
} from "node:fs";
import { isAbsolute, join } from "node:path";
import { createInterface } from "node:readline";
import { expect } from "@playwright/test";
import {
  chromium,
  type Browser,
  type BrowserServer,
  type Page,
  type Request,
} from "playwright";
import { fromJson, type JsonValue } from "@bufbuild/protobuf";
import {
  currentOriginalBinding,
  currentSecurityVersionBinding,
  ManagedLogLevel,
  LogPolicyAdoptionState,
  LogSettingsApplicationState,
  LogSettingsError,
  LogSettingsSource,
  OperationalGroup,
  OperationalSettingsApplicationState,
  OperationalSettingsChangeMode,
  OperationalSettingsError,
  OperationalSettingsSource,
  parseBrowserSession,
  parseCurrentSecurityReview,
  validateCurrentReview,
  validateLogSettings,
  validateOperationalSettings,
  type CurrentSecurityReview,
  type GetLogSettingsResponse,
  type GetOperationalSettingsResponse,
  type OperationalGroupSettings,
} from "lantern-sdk/web";
import {
  GetLogSettingsResponseSchema,
  GetOperationalSettingsResponseSchema,
  GetSecurityChangeStatusRequestSchema,
  PreviewOperationalPolicyResponseSchema,
} from "../../../sdks/node/src/gen/graph/v1/security_pb";

type Cookie = {
  name: string;
  value: string;
  url: string;
  secure: boolean;
  httpOnly: boolean;
  sameSite: "Strict";
  expires?: number;
};
type Manifest = {
  schema: 1;
  scenario: "log" | "operational-slow-rpc";
  origin: "https://admin.example";
  proxy: string;
  spki: string;
  cookies: Cookie[];
  evidence: string;
};
type Result = {
  schema: 1;
  status: "PASS";
  generation: string;
  applyRequests: 1;
  statusRequests: number;
  revokedReadStatus: number;
  revokedPrepareStatus: number;
  originalBindingSame: true;
} & (
  | { scenario: "log"; level: number }
  | {
      scenario: "operational-slow-rpc";
      group: OperationalGroup.SLOW_RPC;
      thresholdMs: number;
    }
);
type DiagnosticRow = {
  method: string;
  path: string;
  status: number;
  error?: string;
};
type BrowserLifecycle = {
  schema: 1;
  owner: "playwright";
  pid: number | null;
  rootExitObserved: boolean;
  rootCloseObserved: boolean;
  serverCloseCompleted: boolean;
  clientDisconnectCompleted: boolean;
  exitCode: number | null;
  signalCode: string | null;
  binary: { path: string; sha256: string };
  version: string;
};

const recoveryKey = "lantern.admin.security-change-recovery.v1";
const service = "/browser/graph.v1.LanternSecurityService/";
const deadline = Date.now() + 90_000;
let stage = "manifest";
let browser: Browser | undefined;
let browserServer: BrowserServer | undefined;
let browserEndpoint = "";
let rootExit: Promise<void> | undefined;
let rootClose: Promise<void> | undefined;
let cleanup: Promise<void> | undefined;
let lifecycleWritten = false;
const lifecycle: BrowserLifecycle = {
  schema: 1,
  owner: "playwright",
  pid: null,
  rootExitObserved: false,
  rootCloseObserved: false,
  serverCloseCompleted: false,
  clientDisconnectCompleted: false,
  exitCode: null,
  signalCode: null,
  binary: { path: "", sha256: "" },
  version: "",
};
let page: Page | undefined;
let manifest: Manifest | undefined;
let cookiesStarted = false;
const diagnosticRows: DiagnosticRow[] = [];
let diagnosticDropped = 0;

function diagnosticPath(request: Request, origin: string): string | undefined {
  const url = new URL(request.url());
  if (url.origin !== origin) return;
  const paths = [
    "/",
    "/settings",
    "/favicon.ico",
    "/auth/session",
    "/graph.v1.LanternSecurityService/GetAuthCapabilities",
    service + "GetCurrentPrincipal",
    service + "GetLogSettings",
    service + "GetOperationalSettings",
    service + "PrepareSecurityChanges",
    service + "ApplySecurityChanges",
    service + "GetSecurityChangeStatus",
  ];
  if (
    paths.includes(url.pathname) ||
    /^\/assets\/[A-Za-z0-9._-]{1,160}\.(js|css|woff2?|svg|png|ico)$/.test(
      url.pathname,
    )
  ) {
    return url.pathname;
  }
}
function diagnosticRow(
  request: Request,
  origin: string,
  status: number,
  error?: string,
) {
  const path = diagnosticPath(request, origin);
  if (!path) return;
  if (diagnosticRows.length === 128) {
    diagnosticDropped++;
    return;
  }
  const method = ["GET", "POST", "HEAD"].includes(request.method())
    ? request.method()
    : "OTHER";
  diagnosticRows.push({ method, path, status, ...(error ? { error } : {}) });
}
function observeFailures(currentPage: Page, origin: string) {
  currentPage.on("response", (response) =>
    diagnosticRow(response.request(), origin, response.status()),
  );
  const networkErrors = new Set([
    "net::ERR_ABORTED",
    "net::ERR_FAILED",
    "net::ERR_CONNECTION_CLOSED",
    "net::ERR_CONNECTION_RESET",
    "net::ERR_CONNECTION_REFUSED",
    "net::ERR_TIMED_OUT",
    "net::ERR_CERT_AUTHORITY_INVALID",
    "net::ERR_CERT_COMMON_NAME_INVALID",
    "net::ERR_CERT_DATE_INVALID",
    "net::ERR_NAME_NOT_RESOLVED",
    "net::ERR_PROXY_CONNECTION_FAILED",
    "net::ERR_TUNNEL_CONNECTION_FAILED",
    "net::ERR_HTTP_RESPONSE_CODE_FAILURE",
    "net::ERR_INVALID_RESPONSE",
    "net::ERR_EMPTY_RESPONSE",
  ]);
  currentPage.on("requestfailed", (request) => {
    const code = request.failure()?.errorText;
    if (code && networkErrors.has(code))
      diagnosticRow(request, origin, 0, code);
  });
}
async function failureDiagnostics(error: unknown) {
  if (!manifest || !cookiesStarted) return;
  const errorNames = new Set([
    "Error",
    "AssertionError",
    "TimeoutError",
    "TypeError",
    "RangeError",
    "SyntaxError",
    "ReferenceError",
    "AbortError",
    "AggregateError",
    "ConnectError",
  ]);
  const errorName =
    error instanceof Error && errorNames.has(error.name)
      ? error.name
      : "UnknownError";
  const aria: Record<string, number | null> = {
    authRegion: null,
    signIn: null,
    authUnavailable: null,
    logPanel: null,
    newLevel: null,
    operationalPanel: null,
    threshold: null,
    review: null,
    apply: null,
  };
  let pixels: Buffer | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const currentPage = page;
  if (currentPage && !currentPage.isClosed()) {
    const capture = async () => {
      await currentPage.setViewportSize({ width: 1280, height: 900 });
      const controls = {
        authRegion: currentPage.getByRole("region", {
          name: "Authentication",
          exact: true,
        }),
        signIn: currentPage.getByText("Sign in to Lantern", { exact: true }),
        authUnavailable: currentPage.getByText("Authentication unavailable", {
          exact: true,
        }),
        logPanel: currentPage.getByRole("region", {
          name: "Log level settings",
          exact: true,
        }),
        newLevel: currentPage.getByLabel("New log level", { exact: true }),
        operationalPanel: currentPage.getByRole("region", {
          name: "Operational policies",
          exact: true,
        }),
        threshold: currentPage.getByLabel("Threshold (ms)", { exact: true }),
        review: currentPage.getByRole("dialog", {
          name: "Review changes",
          exact: true,
        }),
        apply: currentPage.getByRole("button", {
          name: "Save changes",
          exact: true,
        }),
      };
      await Promise.all([
        ...Object.entries(controls).map(async ([key, control]) => {
          aria[key] = await control.count();
        }),
        currentPage
          .screenshot({
            type: "png",
            fullPage: false,
            animations: "disabled",
            timeout: 5000,
          })
          .then((value) => {
            pixels = value;
          }),
      ]);
    };
    try {
      await Promise.race([
        capture().catch(() => undefined),
        new Promise<void>((resolve) => {
          timer = setTimeout(resolve, 5000);
        }),
      ]);
    } finally {
      clearTimeout(timer);
    }
  }
  writeFileSync(
    join(manifest.evidence, "ui-failure.json"),
    JSON.stringify({
      schema: 1,
      stage,
      errorName,
      rows: diagnosticRows.slice(),
      dropped: diagnosticDropped,
      aria,
    }) + "\n",
    { mode: 0o600, flag: "wx" },
  );
  if (pixels)
    writeFileSync(join(manifest.evidence, "ui-failure-1280.png"), pixels, {
      mode: 0o600,
      flag: "wx",
    });
}

function bounded(milliseconds = 15_000): number {
  const remaining = deadline - Date.now();
  assert(remaining > 0, "native browser budget exhausted");
  return Math.min(milliseconds, remaining);
}
function object(value: unknown): Record<string, unknown> {
  assert(value && typeof value === "object" && !Array.isArray(value));
  return value as Record<string, unknown>;
}
function privateFile(path: string) {
  const stat = lstatSync(path);
  assert(
    stat.isFile() && !stat.isSymbolicLink() && (stat.mode & 0o777) === 0o600,
  );
  if (process.getuid) assert.equal(stat.uid, process.getuid());
  assert(stat.size > 0 && stat.size <= 32 << 10);
}
function readManifest(): Manifest {
  const path = process.env.LANTERN_CURRENT_ADMIN_BROWSER_MANIFEST;
  assert(path && isAbsolute(path));
  privateFile(path);
  const value = object(JSON.parse(readFileSync(path, "utf8")));
  assert(
    Object.keys(value).every((key) =>
      [
        "schema",
        "scenario",
        "origin",
        "proxy",
        "spki",
        "cookies",
        "evidence",
      ].includes(key),
    ),
  );
  assert(value.schema === 1 && value.origin === "https://admin.example");
  assert(value.scenario === "log" || value.scenario === "operational-slow-rpc");
  assert(typeof value.proxy === "string" && typeof value.spki === "string");
  const proxy = new URL(value.proxy);
  assert(
    proxy.protocol === "http:" &&
      proxy.hostname === "127.0.0.1" &&
      Number(proxy.port) > 0 &&
      !proxy.username &&
      !proxy.password &&
      proxy.pathname === "/" &&
      !proxy.search &&
      !proxy.hash,
  );
  assert(
    /^[A-Za-z0-9+/]{43}=$/.test(value.spki) &&
      Buffer.from(value.spki, "base64").length === 32 &&
      Buffer.from(value.spki, "base64").toString("base64") === value.spki,
  );
  assert(Array.isArray(value.cookies) && value.cookies.length === 2);
  const names = new Set<string>();
  for (const item of value.cookies) {
    const cookie = object(item);
    assert(
      Object.keys(cookie).every((key) =>
        [
          "name",
          "value",
          "url",
          "secure",
          "httpOnly",
          "sameSite",
          "expires",
        ].includes(key),
      ),
    );
    assert(
      typeof cookie.name === "string" &&
        ["__Host-lantern-session", "__Host-lantern-csrf"].includes(
          cookie.name,
        ) &&
        !names.has(cookie.name),
    );
    names.add(cookie.name);
    assert(
      typeof cookie.value === "string" &&
        cookie.value.length > 0 &&
        cookie.value.length <= 4096 &&
        [...cookie.value].every((character) => {
          const code = character.charCodeAt(0);
          return code > 32 && code !== 127;
        }),
    );
    assert(
      (cookie.url === value.origin || cookie.url === value.origin + "/") &&
        cookie.secure === true &&
        typeof cookie.httpOnly === "boolean" &&
        cookie.sameSite === "Strict",
    );
    assert(
      cookie.expires === undefined ||
        (typeof cookie.expires === "number" &&
          Number.isFinite(cookie.expires) &&
          (cookie.expires === -1 || cookie.expires > Date.now() / 1000)),
    );
  }
  assert(typeof value.evidence === "string" && isAbsolute(value.evidence));
  const evidence = lstatSync(value.evidence);
  assert(
    evidence.isDirectory() &&
      !evidence.isSymbolicLink() &&
      (evidence.mode & 0o077) === 0,
  );
  if (process.getuid) assert.equal(evidence.uid, process.getuid());
  assert(!existsSync(join(value.evidence, "result.json")));
  return value as unknown as Manifest;
}
function writeResult(
  value: Result | { schema: 1; status: "FAIL"; stage: string },
) {
  if (!manifest) return;
  writeFileSync(
    join(manifest.evidence, "result.json"),
    JSON.stringify(value) + "\n",
    { mode: 0o600, flag: "wx" },
  );
}
function requestMethod(request: Request, origin: string): string | undefined {
  const url = new URL(request.url());
  if (
    url.origin !== origin ||
    request.method() !== "POST" ||
    !url.pathname.startsWith(service)
  )
    return;
  return url.pathname.slice(service.length);
}
async function storedReview(
  currentPage: Page,
  original: CurrentSecurityReview,
): Promise<string> {
  const raw = await currentPage.evaluate(
    (key) => sessionStorage.getItem(key),
    recoveryKey,
  );
  assert(raw && raw.length <= 1 << 20);
  const envelope = object(JSON.parse(raw));
  assert(envelope.version === 1 && Array.isArray(envelope.records));
  const matches = envelope.records.flatMap((value) => {
    const row = object(value);
    assert(typeof row.review === "string");
    const review = parseCurrentSecurityReview(row.review);
    validateCurrentReview(review);
    return currentOriginalBinding(review) === currentOriginalBinding(original)
      ? [row.review]
      : [];
  });
  assert.equal(matches.length, 1);
  return matches[0];
}
async function browserFetch(
  currentPage: Page,
  origin: string,
  path: string,
  csrf?: string,
  body?: unknown,
) {
  return currentPage.evaluate(
    async ({ url, csrf, body, timeout }) => {
      const response = await fetch(url, {
        method: body === undefined ? "GET" : "POST",
        credentials: "same-origin",
        cache: "no-store",
        redirect: "error",
        signal: AbortSignal.timeout(timeout),
        headers:
          body === undefined
            ? undefined
            : {
                "Content-Type": "application/json",
                "Connect-Protocol-Version": "1",
                ...(csrf ? { "X-Lantern-CSRF": csrf } : {}),
              },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      const text = await response.text();
      if (text.length > 1 << 20) throw new Error("oversized fixture response");
      return { status: response.status, text };
    },
    { url: origin + path, csrf, body, timeout: bounded(5000) },
  );
}
async function qualifiedSession(currentPage: Page, origin: string) {
  const until = Date.now() + bounded(15_000);
  for (;;) {
    const response = await browserFetch(currentPage, origin, "/auth/session");
    if (response.status === 200) {
      const session = parseBrowserSession(response.text);
      assert(session.principal?.csrfToken);
      currentSecurityVersionBinding(session.principal.version);
      return session;
    }
    assert.equal(
      response.status,
      503,
      "session refused outside bounded source requalification",
    );
    assert(Date.now() < until, "session requalification incomplete");
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
}
async function deniedRead(
  currentPage: Page,
  origin: string,
  csrf: string,
  method: "GetLogSettings" | "GetOperationalSettings",
): Promise<number> {
  const until = Date.now() + bounded(15_000);
  for (;;) {
    const response = await browserFetch(
      currentPage,
      origin,
      service + method,
      csrf,
      {},
    );
    if (response.status === 401 || response.status === 403)
      return response.status;
    assert.equal(
      response.status,
      503,
      "revoked credentials produced a protected response",
    );
    assert(Date.now() < until, "revocation read requalification incomplete");
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
}
async function waitForRevocation() {
  const input = createInterface({ input: process.stdin, terminal: false });
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    await Promise.race([
      (async () => {
        for await (const line of input) {
          assert.equal(
            line,
            "revoked",
            "unexpected native fixture acknowledgement",
          );
          return;
        }
        throw new Error("native fixture acknowledgement missing");
      })(),
      new Promise<never>((_, reject) => {
        timer = setTimeout(
          () =>
            reject(new Error("native revocation acknowledgement timed out")),
          bounded(45_000),
        );
      }),
    ]);
  } finally {
    clearTimeout(timer);
    input.close();
    process.stdin.pause();
  }
}
function settingsResponse(response: unknown): GetLogSettingsResponse {
  const result = fromJson(GetLogSettingsResponseSchema, response as JsonValue);
  validateLogSettings(result);
  return result;
}
type SettingsObservation =
  | { scenario: "log"; value: GetLogSettingsResponse }
  | {
      scenario: "operational-slow-rpc";
      value: GetOperationalSettingsResponse;
    };
function scenarioSettings(
  scenario: Manifest["scenario"],
  response: unknown,
): SettingsObservation {
  if (scenario === "log")
    return { scenario, value: settingsResponse(response) };
  const value = fromJson(
    GetOperationalSettingsResponseSchema,
    response as JsonValue,
  );
  validateOperationalSettings(value);
  return { scenario, value };
}
function slowSettings(
  settings: GetOperationalSettingsResponse,
): OperationalGroupSettings {
  const state = settings.groups.find(
    (group) => group.group === OperationalGroup.SLOW_RPC,
  );
  assert(state && state.effective?.group.case === "slowRpc");
  assert(
    state.error === OperationalSettingsError.NONE &&
      state.application === OperationalSettingsApplicationState.APPLIED &&
      state.changeMode === OperationalSettingsChangeMode.HOT &&
      !state.restartRequired,
  );
  return state;
}
function slowThreshold(settings: GetOperationalSettingsResponse): bigint {
  const group = slowSettings(settings).effective?.group;
  assert(group?.case === "slowRpc");
  return group.value.thresholdMs;
}
function observedGeneration(settings: SettingsObservation): bigint {
  return settings.scenario === "log"
    ? settings.value.desiredGeneration
    : (slowSettings(settings.value).desired?.generation ?? 0n);
}
async function displayedScenario(
  currentPage: Page,
  settings: SettingsObservation,
) {
  if (settings.scenario === "log")
    return displayedObservation(currentPage, settings.value);
  const check = expect.configure({ timeout: bounded() });
  for (const name of [
    "Slow RPC diagnostics",
    "Request admission",
    "Request and traversal limits",
    "Backup production",
    "Graph capacity and garbage collection",
    "Lifecycle and readiness",
    "Search request policy",
    "Replication scheduling",
  ]) {
    await check(
      currentPage.getByRole("region", { name, exact: true }),
    ).toHaveCount(1);
  }
  const state = slowSettings(settings.value);
  const panel = currentPage.getByRole("region", {
    name: "Slow RPC diagnostics",
    exact: true,
  });
  const field = (name: string) =>
    panel
      .locator("dt")
      .filter({ hasText: new RegExp("^" + name + "$") })
      .locator("xpath=following-sibling::dd[1]");
  await check(field("Desired / applied generation")).toHaveText(
    `${state.desired?.generation ?? 0n} / ${state.appliedGeneration}`,
  );
  await check(field("Application")).toHaveText(
    state.desired ? "Applied on this node" : "Startup values active",
  );
  await check(field("Ownership")).toHaveText(
    state.desired ? "Managed policy" : "Not adopted",
  );
  await check(field("Change mode")).toHaveText("Applies to new work");
  const sources = {
    [OperationalSettingsSource.DEFAULT]: "Default",
    [OperationalSettingsSource.ENVIRONMENT]: "Environment",
    [OperationalSettingsSource.DERIVED]: "Derived startup value",
    [OperationalSettingsSource.MANAGED]: "Managed policy",
  };
  const source = sources[state.provenance[0]?.source as keyof typeof sources];
  assert(source && state.effective?.group.case === "slowRpc");
  const desired = state.desired?.values?.group;
  const row = panel
    .getByRole("rowheader", { name: "Threshold (ms)", exact: true })
    .locator("..");
  await check(row.locator("td")).toHaveText([
    String(state.effective.group.value.thresholdMs),
    desired?.case === "slowRpc"
      ? String(desired.value.thresholdMs)
      : "Not adopted",
    source,
  ]);
}
function label(level: ManagedLogLevel): string {
  const values = {
    [ManagedLogLevel.DEBUG]: "Debug",
    [ManagedLogLevel.INFO]: "Info",
    [ManagedLogLevel.WARN]: "Warn",
    [ManagedLogLevel.ERROR]: "Error",
  };
  const value = values[level as keyof typeof values];
  assert(value);
  return value;
}
async function displayedObservation(
  currentPage: Page,
  settings: GetLogSettingsResponse,
) {
  const panel = currentPage.getByRole("region", {
    name: "Log level settings",
    exact: true,
  });
  const check = expect.configure({ timeout: bounded() });
  const field = (name: string) =>
    panel
      .locator("dt")
      .filter({ hasText: new RegExp("^" + name + "$") })
      .locator("xpath=following-sibling::dd[1]");
  await check(field("Effective level")).toHaveText(
    label(settings.effectiveLevel),
  );
  const sources = {
    [LogSettingsSource.DEFAULT]: "Default",
    [LogSettingsSource.ENVIRONMENT]: "Environment",
    [LogSettingsSource.MANAGED]: "Managed policy",
  };
  await check(field("Source")).toHaveText(
    sources[settings.source as keyof typeof sources],
  );
  await check(field("Desired policy")).toHaveText(
    settings.desired ? label(settings.desired.level) : "Not adopted",
  );
  await check(field("Applied / desired generation")).toHaveText(
    `${settings.appliedGeneration} / ${settings.desiredGeneration}`,
  );
  const application =
    settings.application === LogSettingsApplicationState.PENDING
      ? "Pending on this node"
      : settings.adoption === LogPolicyAdoptionState.MANAGED &&
          settings.appliedGeneration > 0n
        ? "Applied on this node"
        : "Startup setting active";
  assert(
    settings.application !== LogSettingsApplicationState.ERROR &&
      settings.error === LogSettingsError.NONE,
  );
  await check(field("Application")).toHaveText(application);
}

function observeBrowserRoot(server: BrowserServer) {
  const child = server.process();
  lifecycle.pid = child.pid ?? null;
  rootExit = new Promise((resolve) =>
    child.once("exit", (code, signal) => {
      lifecycle.rootExitObserved = true;
      lifecycle.exitCode = code;
      lifecycle.signalCode = signal;
      resolve();
    }),
  );
  rootClose = new Promise((resolve) =>
    child.once("close", () => {
      lifecycle.rootCloseObserved = true;
      resolve();
    }),
  );
  assert(lifecycle.pid && lifecycle.pid > 0);
  return child;
}
async function bindBrowserBinary(path: string) {
  const resolved = realpathSync(path);
  assert(isAbsolute(resolved) && lstatSync(resolved).isFile());
  const hash = createHash("sha256");
  for await (const chunk of createReadStream(resolved)) hash.update(chunk);
  lifecycle.binary = { path: resolved, sha256: hash.digest("hex") };
}
function writeBrowserLifecycle() {
  if (!manifest || lifecycleWritten) return;
  lifecycleWritten = true;
  writeFileSync(
    join(manifest.evidence, "browser-lifecycle.json"),
    JSON.stringify(lifecycle) + "\n",
    { mode: 0o600, flag: "wx" },
  );
}
async function closeOwnedBrowser() {
  if (cleanup) return cleanup;
  cleanup = (async () => {
    const attached = browser;
    const server = browserServer;
    browser = undefined;
    browserServer = undefined;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const close = async () => {
      // Start the owner shutdown independently of a potentially stuck client.
      const clientClose = (async () => {
        if (attached) {
          await attached.close();
          lifecycle.clientDisconnectCompleted = true;
        }
      })();
      const serverClose = (async () => {
        if (server) {
          await server.close();
          lifecycle.serverCloseCompleted = true;
        }
      })();
      const results = await Promise.allSettled([
        clientClose,
        serverClose,
        rootExit,
        rootClose,
      ]);
      const rejected = results.find((result) => result.status === "rejected");
      if (rejected?.status === "rejected") throw rejected.reason;
    };
    try {
      await Promise.race([
        close(),
        new Promise<never>((_, reject) => {
          timer = setTimeout(
            () => reject(new Error("owned browser cleanup budget exhausted")),
            8000,
          );
        }),
      ]);
    } finally {
      clearTimeout(timer);
      writeBrowserLifecycle();
    }
  })();
  return cleanup;
}

async function run(m: Manifest): Promise<Result> {
  const operational = m.scenario === "operational-slow-rpc";
  const settingsMethod = operational
    ? "GetOperationalSettings"
    : "GetLogSettings";
  const reviewButton = "Review changes";
  const reviewRegion = "Review changes";
  const applyButton = "Save changes";
  const editor = operational ? "Threshold (ms)" : "New log level";
  const statusLabel = operational
    ? "Check original operational change status"
    : "Check original change status";
  stage = "browser server launch";
  browserServer = await chromium.launchServer({
    headless: true,
    chromiumSandbox: true,
    proxy: { server: m.proxy },
    args: ["--ignore-certificate-errors-spki-list=" + m.spki],
    timeout: bounded(),
  });
  const child = observeBrowserRoot(browserServer);
  stage = "browser process binding";
  await bindBrowserBinary(child.spawnfile);
  browserEndpoint = browserServer.wsEndpoint();
  stage = "browser client connection";
  browser = await chromium.connect(browserEndpoint, { timeout: bounded() });
  lifecycle.version = browser.version();
  assert(lifecycle.version.length > 0 && lifecycle.version.length <= 128);
  stage = "browser context creation";
  const context = await browser.newContext();
  stage = "browser page creation";
  page = await context.newPage();
  stage = "owned page viewport";
  await page.setViewportSize({ width: 1280, height: 900 });
  page.setDefaultTimeout(15_000);
  page.setDefaultNavigationTimeout(20_000);
  stage = "owned page init script";
  await page.addInitScript(
    (origin) => localStorage.setItem("lantern.admin.baseUrl", origin),
    m.origin,
  );
  stage = "cookie installation";
  // Cookie validation/protocol errors can echo arguments, including values.
  cookiesStarted = true;
  await context.addCookies(m.cookies);
  const requests: Record<string, Record<string, unknown>[]> = {
    ApplySecurityChanges: [],
    PrepareSecurityChanges: [],
    GetSecurityChangeStatus: [],
  };
  let malformedRequest = false;
  page.on("request", (request) => {
    const method = requestMethod(request, m.origin);
    if (!method || !(method in requests)) return;
    try {
      assert(!request.headers().authorization);
      requests[method].push(object(request.postDataJSON()));
    } catch {
      malformedRequest = true;
    }
  });
  const check = expect.configure({ timeout: bounded() });
  observeFailures(page, m.origin);
  stage = "root navigation";
  await page.goto(m.origin + "/", {
    waitUntil: "domcontentloaded",
    timeout: bounded(20_000),
  });
  stage = "session qualification";
  const session = await qualifiedSession(page, m.origin);
  const csrf = session.principal!.csrfToken;
  const first = page.waitForResponse(
    (response) =>
      requestMethod(response.request(), m.origin) === settingsMethod &&
      response.status() === 200,
    { timeout: bounded(20_000) },
  );
  // Observe rejection immediately; the original promise still fails when awaited.
  void first.catch(() => undefined);
  stage = "settings navigation";
  await page.goto(m.origin + "/settings", {
    waitUntil: "domcontentloaded",
    timeout: bounded(20_000),
  });
  stage = "first 200 settings response";
  const firstResponse = await first;
  stage = "settings decode";
  const before = scenarioSettings(m.scenario, await firstResponse.json());
  stage = "rendered observation";
  await displayedScenario(page, before);
  if (before.scenario === "operational-slow-rpc") {
    stage = "read-only connected capacity preview";
    const capacity = page.getByRole("region", {
      name: "Graph capacity and garbage collection",
      exact: true,
    });
    await capacity
      .getByRole("button", {
        name: "Edit graph capacity and garbage collection",
        exact: true,
      })
      .click();
    if (
      !before.value.groups.find((g) => g.group === OperationalGroup.CAPACITY)
        ?.desired
    )
      await capacity
        .getByRole("button", {
          name: "Use this node’s values for adoption",
          exact: true,
        })
        .click();
    await capacity.getByLabel("Max vertices", { exact: true }).fill("1");
    const response = page.waitForResponse(
      (r) =>
        requestMethod(r.request(), m.origin) === "PreviewOperationalPolicy" &&
        r.status() === 200,
      { timeout: bounded(20_000) },
    );
    void response.catch(() => undefined);
    await capacity
      .getByRole("button", {
        name: "Preview connected-node capacity",
        exact: true,
      })
      .click();
    const sampled = fromJson(
      PreviewOperationalPolicyResponseSchema,
      await (await response).json(),
    );
    assert.equal(sampled.capacityUsage?.member, before.value.member);
    const sample = capacity.getByRole("table", {
      name: "Capacity usage sample",
      exact: true,
    });
    await check(sample.getByRole("row")).toHaveCount(5);
    await check(
      sample.getByRole("row").filter({ hasText: "Vertex admission footprint" }),
    ).toContainText(sampled.capacityUsage!.vertexFootprint.toString());
    await capacity.getByLabel("Max vertices", { exact: true }).fill("2");
    await check(sample).toHaveCount(0);
    assert.equal(requests.ApplySecurityChanges.length, 0);
    assert.equal(requests.PrepareSecurityChanges.length, 0);
  }
  const target =
    before.scenario === "log"
      ? before.value.effectiveLevel === ManagedLogLevel.INFO
        ? ManagedLogLevel.ERROR
        : ManagedLogLevel.INFO
      : slowThreshold(before.value) === 0n
        ? 37
        : 0;
  stage = "explicit review and one Apply";
  if (before.scenario === "operational-slow-rpc") {
    const card = page.getByRole("region", {
      name: "Slow RPC diagnostics",
      exact: true,
    });
    await card
      .getByRole("button", { name: "Edit slow rpc diagnostics", exact: true })
      .click();
    if (!slowSettings(before.value).desired) {
      await card
        .getByRole("button", {
          name: "Use this node’s values for adoption",
          exact: true,
        })
        .click();
    }
    await card.getByLabel(editor, { exact: true }).fill(String(target));
  } else {
    await page.getByLabel(editor, { exact: true }).selectOption(String(target));
  }
  await page
    .getByRole("region", {
      name: operational ? "Slow RPC diagnostics" : "Log level settings",
      exact: true,
    })
    .getByRole("button", { name: reviewButton, exact: true })
    .click();
  const apply = page.getByRole("button", {
    name: applyButton,
    exact: true,
  });
  await check(apply).toBeEnabled();
  await apply.click();
  await check(
    page.getByText("The response was not confirmed.", { exact: false }),
  ).toBeVisible();
  assert(!malformedRequest);
  assert.equal(requests.ApplySecurityChanges.length, 1);
  assert.equal(Number(requests.PrepareSecurityChanges.length), 1);
  const originalJSON = requests.ApplySecurityChanges[0].currentReview;
  const original = parseCurrentSecurityReview(JSON.stringify(originalJSON));
  validateCurrentReview(original);
  assert.equal(original.changes.length, 1);
  const operation = original.changes[0].operation;
  if (before.scenario === "operational-slow-rpc") {
    assert(
      operation.case === "putOperationalPolicy" &&
        operation.value.version === 1 &&
        operation.value.expectedGeneration === observedGeneration(before) &&
        operation.value.values?.group.case === "slowRpc" &&
        operation.value.values.group.value.thresholdMs === BigInt(target),
    );
  } else {
    assert(
      operation.case === "putLogPolicy" && operation.value.level === target,
    );
  }
  const savedReview = await storedReview(page, original);
  stage = "reload original recovery";
  await qualifiedSession(page, m.origin);
  await page.reload({
    waitUntil: "domcontentloaded",
    timeout: bounded(20_000),
  });
  const statusButton = page.getByRole("button", {
    name: statusLabel,
    exact: true,
  });
  await check(statusButton).toBeVisible();
  await check(
    page.getByRole("button", {
      name: operational ? "Edit slow rpc diagnostics" : reviewButton,
      exact: true,
    }),
  ).toBeDisabled();
  await check(
    page.getByText(
      operational
        ? "The original outcome is not confirmed."
        : "The original change is not confirmed.",
      { exact: false },
    ),
  ).toBeVisible();
  assert.equal(await storedReview(page, original), savedReview);
  assert.equal(requests.ApplySecurityChanges.length, 1);
  assert.equal(requests.GetSecurityChangeStatus.length, 0);
  stage = "explicit original status";
  await statusButton.click();
  await check(
    page.getByText(
      operational
        ? "The operational policy was committed."
        : "The log policy was committed.",
      { exact: false },
    ),
  ).toBeVisible();
  assert(requests.GetSecurityChangeStatus.length >= 1);
  for (const request of requests.GetSecurityChangeStatus) {
    const decoded = fromJson(
      GetSecurityChangeStatusRequestSchema,
      request as JsonValue,
    );
    assert.equal(
      currentOriginalBinding({
        profile: decoded.currentProfile,
        changeId: decoded.currentChangeId,
        intentDigest: decoded.currentIntentDigest,
      }),
      currentOriginalBinding(original),
    );
  }
  assert.equal(await storedReview(page, original), savedReview);
  const observed = page.waitForResponse(
    (response) =>
      requestMethod(response.request(), m.origin) === settingsMethod &&
      response.status() === 200,
    { timeout: bounded(20_000) },
  );
  void observed.catch(() => undefined);
  await page
    .getByRole("button", {
      name: operational ? "Reload operational settings" : "Reload settings",
      exact: true,
    })
    .click();
  const after = scenarioSettings(m.scenario, await (await observed).json());
  assert.equal(observedGeneration(after), observedGeneration(before) + 1n);
  if (after.scenario === "operational-slow-rpc") {
    const state = slowSettings(after.value);
    assert(
      state.desired?.values?.group.case === "slowRpc" &&
        state.desired.values.group.value.thresholdMs === BigInt(target),
    );
  } else {
    assert.equal(after.value.desired?.level, target);
  }
  await displayedScenario(page, after);
  assert.equal(requests.ApplySecurityChanges.length, 1);
  stage = "rendered evidence";
  await page.screenshot({
    path: join(m.evidence, "settings-native-1280.png"),
    fullPage: true,
    animations: "disabled",
    timeout: bounded(),
  });
  await page.setViewportSize({ width: 390, height: 900 });
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  );
  await page.screenshot({
    path: join(m.evidence, "settings-native-390.png"),
    fullPage: true,
    animations: "disabled",
    timeout: bounded(),
  });
  await page.setViewportSize({ width: 1280, height: 900 });
  stage = "await exact-session revocation";
  process.stdout.write("current_admin_browser ready-for-revocation\n");
  await waitForRevocation();
  stage = "revoked read and preparation";
  const revokedReadStatus = await deniedRead(
    page,
    m.origin,
    csrf,
    settingsMethod,
  );
  const denied = await browserFetch(
    page,
    m.origin,
    service + "PrepareSecurityChanges",
    csrf,
    { currentReview: originalJSON },
  );
  assert(denied.status === 401 || denied.status === 403);
  assert(
    requests.PrepareSecurityChanges.length === 2 &&
      requests.ApplySecurityChanges.length === 1 &&
      !malformedRequest,
  );
  stage = "revoked UI clears protected review";
  await page.reload({
    waitUntil: "domcontentloaded",
    timeout: bounded(20_000),
  });
  await page.evaluate(() => window.dispatchEvent(new Event("focus")));
  const authentication = page.getByRole("region", {
    name: "Authentication",
    exact: true,
  });
  await check(authentication).toBeVisible();
  await check(
    authentication.getByText("Sign in to Lantern", { exact: true }),
  ).toBeVisible();
  await check(
    page.getByRole("dialog", { name: reviewRegion, exact: true }),
  ).toHaveCount(0);
  await check(
    page.getByRole("button", { name: applyButton, exact: true }),
  ).toHaveCount(0);
  await check(page.getByLabel(editor, { exact: true })).toHaveCount(0);
  assert.equal(await storedReview(page, original), savedReview);
  assert.equal(requests.ApplySecurityChanges.length, 1);
  await page.screenshot({
    path: join(m.evidence, "settings-native-revoked.png"),
    fullPage: true,
    animations: "disabled",
    timeout: bounded(),
  });
  return {
    schema: 1,
    status: "PASS",
    generation: String(observedGeneration(after)),
    applyRequests: 1,
    statusRequests: requests.GetSecurityChangeStatus.length,
    revokedReadStatus,
    revokedPrepareStatus: denied.status,
    originalBindingSame: true,
    ...(after.scenario === "log"
      ? { scenario: "log" as const, level: target }
      : {
          scenario: "operational-slow-rpc" as const,
          group: OperationalGroup.SLOW_RPC as const,
          thresholdMs: target,
        }),
  };
}

try {
  manifest = readManifest();
  const result = await run(manifest);
  stage = "owned browser cleanup";
  await closeOwnedBrowser();
  writeFileSync(
    join(manifest.evidence, "network-observation.json"),
    JSON.stringify({
      schema: 1,
      rows: diagnosticRows.slice(),
      dropped: diagnosticDropped,
    }) + "\n",
    { mode: 0o600, flag: "wx" },
  );
  assert(
    lifecycle.rootExitObserved &&
      lifecycle.rootCloseObserved &&
      lifecycle.serverCloseCompleted &&
      lifecycle.clientDisconnectCompleted,
  );
  assert(lifecycle.exitCode !== null || lifecycle.signalCode !== null);
  writeResult(result);
  process.stdout.write("current_admin_browser PASS\n");
} catch (error) {
  if (manifest && !cookiesStarted && error instanceof Error) {
    process.stderr.write(
      "current_admin_browser setup-diagnostic " +
        JSON.stringify({
          schema: 1,
          stage,
          name: error.name.slice(0, 64),
          message: (browserEndpoint
            ? error.message.replaceAll(
                browserEndpoint,
                "<owned Playwright endpoint>",
              )
            : error.message
          ).slice(0, 1984),
        }) +
        "\n",
    );
  }
  try {
    await failureDiagnostics(error);
  } catch {
    /* Diagnostics cannot replace or change the original failure. */
  }
  try {
    await closeOwnedBrowser();
  } catch {
    /* The lifecycle artifact retains incomplete public-API cleanup evidence. */
  }
  try {
    writeResult({ schema: 1, status: "FAIL", stage });
  } catch {
    /* Missing/invalid evidence remains a failure. */
  }
  process.stderr.write("current_admin_browser FAIL stage=" + stage + "\n");
  process.exitCode = 1;
} finally {
  process.stdin.pause();
}
