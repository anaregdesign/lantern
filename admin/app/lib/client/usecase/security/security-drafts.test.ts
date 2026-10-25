import { describe, expect, test } from "bun:test";
import {
  SecurityAction,
  SecurityEffect,
  type SecurityIssuer,
} from "lantern-sdk/web";
import {
  assignmentChange,
  buildIssuer,
  buildRole,
  issuerDraft,
  newRule,
  roleDraft,
  userDraft,
  userIdentity,
} from "./security-drafts";

describe("explicit security drafts", () => {
  test("prefix is literal, including a wildcard; all keys needs an explicit choice", () => {
    const draft = {
      ...roleDraft(),
      id: "reader",
      name: "Reader",
      rules: [newRule(1)],
    };
    expect(() => buildRole(draft)).toThrow("explicitly");
    draft.rules[0].prefix = "users:*";
    expect(buildRole(draft).rules[0].resource).toEqual({
      case: "prefix",
      value: "users:*",
    });
    draft.rules[0].allKeys = true;
    expect(buildRole(draft).rules[0].resource).toEqual({
      case: "prefix",
      value: "",
    });
    draft.rules[0].allKeys = false;
    draft.rules[0].prefix = "sys:auth";
    expect(() => buildRole(draft)).toThrow("logical");
  });
  test("Deny is retained and capability resource choices are explicit", () => {
    const draft = {
      ...roleDraft(),
      id: "observer",
      name: "Observer",
      rules: [
        {
          ...newRule(1),
          effect: SecurityEffect.DENY,
          action: SecurityAction.OPERATIONS_READ,
          global: true,
        },
      ],
    };
    expect(buildRole(draft).rules[0].effect).toBe(SecurityEffect.DENY);
    draft.rules[0].global = false;
    expect(() => buildRole(draft)).toThrow("scope");
  });
  test("Issuer writes strip read metadata; secret preserve, clear and replace differ", () => {
    const draft = {
      ...issuerDraft(),
      issuer: "https://idp.example",
      clientId: "admin",
      apiAudience: "lantern",
      redirectUri: "https://admin.example/auth/callback",
    };
    expect(buildIssuer(draft).secretRef).toBeUndefined();
    expect(buildIssuer(draft).configRevision).toBe(0n);
    expect(buildIssuer(draft).humanSubjectNamespaceQualified).toBe(false);
    expect(
      buildIssuer({ ...draft, humanSubjectNamespaceQualified: true })
        .humanSubjectNamespaceQualified,
    ).toBe(true);
    expect(buildIssuer({ ...draft, secretMode: "clear" }).secretRef).toBe("");
    expect(
      buildIssuer({
        ...draft,
        secretMode: "replace",
        secretRef: "installed-binding",
      }).secretRef,
    ).toBe("installed-binding");
    expect(() => buildIssuer({ ...draft, locked: true })).toThrow("operator");
  });
  test("browser and API profiles can be configured independently", () => {
    const browser = buildIssuer({
      ...issuerDraft(),
      issuer: "https://idp.example",
      clientId: "browser-client",
      redirectUri: "https://admin.example/auth/callback",
    });
    expect(browser.clientId).toBe("browser-client");
    expect(browser.apiAudience).toBe("");
    expect(browser.secretRef).toBeUndefined();
    const api = buildIssuer({
      ...issuerDraft(),
      issuer: "https://idp.example",
      apiAudience: "lantern-api",
      humanSubjectNamespaceQualified: true,
    });
    expect(api.apiAudience).toBe("lantern-api");
    expect(api.clientId).toBe("");
    expect(api.redirectUri).toBe("");
    expect(api.secretRef).toBeUndefined();
    expect(api.humanSubjectNamespaceQualified).toBe(true);
  });
  test("absent or incomplete profiles do not borrow another profile's settings", () => {
    const draft = { ...issuerDraft(), issuer: "https://idp.example" };
    expect(() => buildIssuer(draft)).toThrow("Configure");
    expect(() => buildIssuer({ ...draft, clientId: "browser" })).toThrow(
      "redirect URI",
    );
    expect(() =>
      buildIssuer({
        ...draft,
        apiAudience: "api",
        redirectUri: "https://admin.example/auth/callback",
      }),
    ).toThrow("Browser client ID");
    expect(() =>
      buildIssuer({
        ...draft,
        apiAudience: "api",
        secretMode: "replace",
        secretRef: "installed-binding",
      }),
    ).toThrow("Browser client ID");
    expect(() =>
      buildIssuer({
        ...draft,
        clientId: "browser",
        redirectUri: "https://admin.example/auth/callback",
        humanSubjectNamespaceQualified: true,
      }),
    ).toThrow("requires an API audience");
  });
  test("removing browser login requires clearing its retained secret binding", () => {
    const draft = {
      ...issuerDraft(),
      issuer: "https://idp.example",
      apiAudience: "api",
      hasSecretBinding: true,
      existing: true,
    };
    expect(() => buildIssuer(draft)).toThrow("Browser client ID");
    const api = buildIssuer({ ...draft, secretMode: "clear" });
    expect(api.secretRef).toBe("");
    expect(api.clientId).toBe("");
    expect(api.apiAudience).toBe("api");
  });
  test("Google profiles retain the pinned signing algorithm", () => {
    const draft = {
      ...issuerDraft(),
      issuer: "https://accounts.google.com",
      apiAudience: "api",
    };
    expect(() => buildIssuer(draft)).toThrow("RS256");
    expect(buildIssuer({ ...draft, algorithms: "RS256" }).algorithms).toEqual([
      "RS256",
    ]);
  });
  test("identical subjects in distinct Issuers remain distinct; assignments contain only Role IDs", () => {
    const first = {
      ...userDraft(),
      issuer: "https://first.example",
      subject: "same",
    };
    const second = { ...first, issuer: "https://second.example" };
    expect(userIdentity(first)).not.toEqual(userIdentity(second));
    const assignment = assignmentChange({ ...first, roleId: "reader" });
    expect(assignment.operation.case).toBe("putAssignment");
    expect(() =>
      assignmentChange({ ...first, roleId: "cluster_replica" }),
    ).toThrow("Peer");
    expect(() =>
      assignmentChange(
        {
          ...first,
          roleId: "reader",
          assignments: [
            {
              $typeName: "graph.v1.SecurityRoleAssignment",
              roleId: "reader",
              envOwned: true,
            },
          ],
        },
        true,
      ),
    ).toThrow("locked");
  });
  test("Apple read-only secret flag is omitted from updates while its profile survives", () => {
    const prior = buildIssuer({
      ...issuerDraft(),
      issuer: "https://appleid.apple.com",
      clientId: "browser-client",
      apiAudience: "lantern",
      redirectUri: "https://admin.example/auth/callback",
    });
    prior.nativeClients = [
      {
        $typeName: "graph.v1.SecurityNativeClient",
        id: "ios",
        kind: 2,
        clientId: "com.example.app",
        serviceOrigin: "https://lantern.example",
        presenters: [],
        redirectUri: "",
        androidPackage: "",
        hasSecretBinding: true,
      },
    ];
    const draft = issuerDraft(prior);
    const changed = buildIssuer({ ...draft, enabled: false });
    expect(draft.nativeClients[0].hasSecretBinding).toBe(true);
    expect(changed.nativeClients).toEqual([
      { ...prior.nativeClients[0], hasSecretBinding: false },
    ]);
    expect(changed.nativeClients[0]).not.toHaveProperty("secretRef");
    const nativeOnly = buildIssuer({
      ...draft,
      clientId: "",
      apiAudience: "",
      redirectUri: "",
    });
    expect(nativeOnly.nativeClients).toEqual(changed.nativeClients);
    expect(nativeOnly.clientId).toBe("");
    expect(nativeOnly.apiAudience).toBe("");
    changed.nativeClients[0].presenters.push("detached");
    expect(draft.nativeClients[0].presenters).toEqual([]);
  });
  test("browser Issuer edits retain detached operator native profiles", () => {
    const value: SecurityIssuer = {
      ...buildIssuer({
        ...issuerDraft(),
        issuer: "https://accounts.google.com",
        algorithms: "RS256",
        clientId: "browser-client",
        apiAudience: "lantern",
        redirectUri: "https://admin.example/auth/callback",
      }),
      nativeClients: [
        {
          $typeName: "graph.v1.SecurityNativeClient",
          id: "mobile",
          kind: 1, // Google ID-token profile on the public wire.
          clientId: "backend.apps.googleusercontent.com",
          serviceOrigin: "https://lantern.example",
          presenters: ["android-client"],
          redirectUri: "",
          androidPackage: "",
          hasSecretBinding: false,
        },
      ],
    };
    const draft = issuerDraft(value);
    const changed = buildIssuer({ ...draft, enabled: false });
    expect(changed.nativeClients).toEqual(value.nativeClients);
    value.nativeClients[0].presenters[0] = "changed-source";
    changed.nativeClients[0].presenters.push("changed-output");
    expect(draft.nativeClients[0].presenters).toEqual(["android-client"]);
    expect(issuerDraft().nativeClients).toEqual([]);
  });
});

test("Server-owned Role locks are not client-owned policy metadata", () => {
  const value = {
    $typeName: "graph.v1.SecurityRole" as const,
    id: "security_admin",
    name: "Security administrator",
    rules: [],
    envOwned: true,
  };
  expect(() => buildRole(roleDraft(value))).toThrow("operator");
  const mutable = { ...roleDraft(), id: "reader", name: "Reader" };
  expect(buildRole(mutable).envOwned).toBe(false);
});

test("UID and unknown selectors cannot become an empty literal draft", () => {
  const role = buildRole({
    ...roleDraft(),
    id: "personal",
    name: "Personal",
    rules: [{ ...newRule(1), prefix: "users:" }],
  });
  role.rules[0].resource = {
    case: "principalUidPrefix",
    value: {
      $typeName: "graph.v1.SecurityPrincipalUIDPrefix",
      literalBefore: "users:",
      literalAfter: ":",
    },
  };
  expect(() => roleDraft(role)).toThrow("unsupported");
  expect(role.rules[0].resource.case).toBe("principalUidPrefix");
  role.rules[0].resource = { case: undefined };
  expect(() => roleDraft(role)).toThrow("unsupported");
  role.rules[0].resource = { case: "global", value: false };
  expect(() => roleDraft(role)).toThrow("unsupported");
});

test("Head-managed Roles contain Vertex prefix grants, not derived Edge operations", () => {
  const draft = {
    ...roleDraft(),
    id: "relationships",
    name: "Head relationships",
    rules: [
      {
        ...newRule(1),
        action: SecurityAction.VERTEX_READ,
        prefix: "users:alice:",
      },
      {
        ...newRule(2),
        action: SecurityAction.VERTEX_WRITE,
        prefix: "profiles:",
      },
    ],
  };
  const role = buildRole(draft);
  expect(role.rules.map((rule) => rule.resource)).toEqual([
    { case: "prefix", value: "users:alice:" },
    { case: "prefix", value: "profiles:" },
  ]);
  expect(buildRole(roleDraft(role))).toEqual(role);
  for (const action of [
    SecurityAction.EDGE_READ,
    SecurityAction.EDGE_CREATE,
    SecurityAction.EDGE_ADD,
    SecurityAction.EDGE_WRITE,
    SecurityAction.EDGE_DELETE,
  ]) {
    expect(() =>
      buildRole({
        ...draft,
        rules: [{ ...newRule(1), action, allKeys: true }],
      }),
    ).toThrow("action");
  }
});
