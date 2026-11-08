import { describe, expect, test } from "bun:test";
import {
  SecurityPermission as P,
  SecurityResource as R,
  type SecurityRule,
} from "lantern-sdk/web";
import { scopeOptions, receiptSuggestedForEdge } from "./scope-options";
const rule = (
  permission: P,
  prefix: string,
  resourceKind = R.DATA,
): SecurityRule => ({
  $typeName: "graph.v1.SecurityRule",
  id: prefix || "all",
  permission,
  resourceKind,
  resource: { case: "prefix", value: prefix },
});
const roles = (rules: SecurityRule[]) => [{ id: "reader", rules }];
describe("Read scopes suggested by current Server Roles", () => {
  test("data read covers query and receipt suggestions while write grants no disclosure", () => {
    const grants = roles([rule(P.READ, "orders:")]);
    expect(scopeOptions(grants)).toEqual([{ prefix: "orders:" }]);
    expect(receiptSuggestedForEdge(grants, "orders:a", "orders:b")).toBe(true);
    expect(receiptSuggestedForEdge(grants, "orders:a", "private:b")).toBe(
      false,
    );
    expect(
      receiptSuggestedForEdge(
        roles([rule(P.WRITE, "orders:")]),
        "orders:a",
        "orders:b",
      ),
    ).toBe(false);
    expect(scopeOptions(roles([rule(P.WRITE, "orders:")]))).toEqual([]);
  });
  test("unioned Roles supply endpoint reads independently and role removal preserves remaining scope", () => {
    const left = { id: "left", rules: [rule(P.READ, "orders:a")] },
      right = { id: "right", rules: [rule(P.READ, "orders:b")] };
    expect(receiptSuggestedForEdge([left, right], "orders:a", "orders:b")).toBe(
      true,
    );
    expect(scopeOptions([right])).toEqual([{ prefix: "orders:b" }]);
    expect(receiptSuggestedForEdge([right], "orders:a", "orders:b")).toBe(
      false,
    );
  });
  test("management resource grants and missing or unsupported permission do not suggest data scope", () => {
    expect(
      scopeOptions(
        roles([
          rule(P.READ, "", R.SECURITY),
          rule(P.WRITE, "", R.SETTINGS),
          rule(P.READ, "", R.OPERATIONS),
          rule(P.READ, "", R.SCHEMA),
          rule(P.UNSPECIFIED, ""),
        ]),
      ),
    ).toEqual([]);
    expect(scopeOptions([])).toEqual([]);
  });
  test("literal wildcards stay literal and explicit empty prefix suggests all logical data", () => {
    expect(scopeOptions(roles([rule(P.READ, "orders:*")]))).toEqual([
      { prefix: "orders:*" },
    ]);
    expect(scopeOptions(roles([rule(P.READ, ""), rule(P.READ, "")]))).toEqual([
      { prefix: "" },
    ]);
  });
  test("typed UID scope uses only the server-provided self UID and never opaque subject or email", () => {
    const personal: SecurityRule = {
      $typeName: "graph.v1.SecurityRule",
      id: "personal",
      permission: P.READ,
      resourceKind: R.DATA,
      resource: {
        case: "principalUidPrefix",
        value: {
          $typeName: "graph.v1.SecurityPrincipalUIDPrefix",
          literalBefore: "users:",
          literalAfter: ":",
        },
      },
    };
    const granted = roles([personal]),
      uid = "0123456789abcdef0123456789abcdef";
    expect(scopeOptions(granted, uid)).toEqual([{ prefix: `users:${uid}:` }]);
    expect(
      receiptSuggestedForEdge(granted, `users:${uid}:a`, `users:${uid}:b`, uid),
    ).toBe(true);
    for (const unknown of [
      undefined,
      "subject",
      "user@example.invalid",
      "0".repeat(32),
    ])
      expect(scopeOptions(granted, unknown)).toEqual([]);
  });
});
