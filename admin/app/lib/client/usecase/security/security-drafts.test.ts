import { describe, expect, test } from "bun:test";
import { SecurityAction, SecurityEffect } from "lantern-sdk/web";
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

test("directed pairs preserve one action and independent explicit bounds", () => {
  const draft = {
    ...roleDraft(),
    id: "connections",
    name: "Connections",
    rules: [
      {
        ...newRule(1),
        action: SecurityAction.EDGE_CREATE,
        pair: true,
        tailPrefix: "users:alice:",
        headPrefix: "profiles:",
      },
    ],
  };
  const role = buildRole(draft);
  expect(role.rules[0].resource).toEqual({
    case: "pair",
    value: {
      $typeName: "graph.v1.SecurityPrefixPair",
      tailPrefix: "users:alice:",
      headPrefix: "profiles:",
    },
  });
  expect(buildRole(roleDraft(role))).toEqual(role);
  expect(() =>
    buildRole({
      ...draft,
      rules: [{ ...draft.rules[0], pair: false, allKeys: true }],
    }),
  ).toThrow("directed");
  expect(() =>
    buildRole({ ...draft, rules: [{ ...draft.rules[0], headPrefix: "" }] }),
  ).toThrow("both");
  expect(() =>
    buildRole({
      ...draft,
      rules: [{ ...draft.rules[0], action: SecurityAction.VERTEX_READ }],
    }),
  ).toThrow("Edge");
  const allTargets = buildRole({
    ...draft,
    rules: [{ ...draft.rules[0], allHeads: true }],
  });
  expect(
    allTargets.rules[0].resource.case === "pair" &&
      allTargets.rules[0].resource.value.headPrefix,
  ).toBe("");
  expect(allTargets.rules[0].action).toBe(SecurityAction.EDGE_CREATE);
  expect(roleDraft(allTargets).rules[0].allHeads).toBe(true);
  expect(roleDraft(allTargets).rules[0].allTails).toBe(false);
});
