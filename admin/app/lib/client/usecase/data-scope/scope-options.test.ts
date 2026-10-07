import { describe, expect, test } from "bun:test";
import {
  SecurityAction as A,
  SecurityEffect as E,
  type SecurityRule,
} from "lantern-sdk/web";
import {
  scopeOptions,
  deniedScopes,
  QUERY_SCOPE_ACTIONS,
  receiptSuggestedForEdge,
} from "./scope-options";
const rule = (action: A, effect: E, prefix: string): SecurityRule => ({
  $typeName: "graph.v1.SecurityRule",
  id: prefix || "all",
  action,
  effect,
  resource: { case: "prefix", value: prefix },
});
const roles = (rules: SecurityRule[]) => [{ id: "reader", rules }];
describe("Scopes suggested by current Server Roles", () => {
  test("optional receipts need grants for both endpoints, with Deny precedence", () => {
    expect(
      receiptSuggestedForEdge(
        roles([
          rule(A.VERTEX_READ, E.ALLOW, "orders:"),
          rule(A.VERTEX_WRITE, E.ALLOW, "orders:"),
        ]),
        "orders:a",
        "orders:b",
      ),
    ).toBe(false);
    expect(
      receiptSuggestedForEdge(
        roles([rule(A.RECEIPT_READ, E.ALLOW, "audit:")]),
        "orders:a",
        "orders:b",
      ),
    ).toBe(false);
    expect(
      receiptSuggestedForEdge(
        roles([rule(A.RECEIPT_READ, E.ALLOW, "orders:a")]),
        "orders:a",
        "orders:b",
      ),
    ).toBe(false);
    const grants = roles([rule(A.RECEIPT_READ, E.ALLOW, "orders:")]);
    expect(receiptSuggestedForEdge(grants, "orders:a", "orders:b")).toBe(true);
    expect(
      receiptSuggestedForEdge(
        [
          ...grants,
          { id: "deny", rules: [rule(A.RECEIPT_READ, E.DENY, "orders:b")] },
        ],
        "orders:a",
        "orders:b",
      ),
    ).toBe(false);
  });
  test("intersects query and read grants and shows nested Deny exceptions", () => {
    const r = roles([
      rule(A.VERTEX_READ, E.ALLOW, "orders:"),
      rule(A.QUERY, E.ALLOW, "orders:open:"),
      rule(A.VERTEX_READ, E.DENY, "orders:open:private:"),
    ]);
    expect(scopeOptions(r, QUERY_SCOPE_ACTIONS)).toEqual([
      { prefix: "orders:open:", denied: ["orders:open:private:"] },
    ]);
    expect(deniedScopes(r, QUERY_SCOPE_ACTIONS, "orders:open:")).toEqual([
      "orders:open:private:",
    ]);
  });
  test("Deny across another Role removes a fully denied candidate", () => {
    const r = [
      ...roles([rule(A.VERTEX_READ, E.ALLOW, "orders:")]),
      { id: "deny", rules: [rule(A.VERTEX_READ, E.DENY, "orders:")] },
    ];
    expect(scopeOptions(r, [A.VERTEX_READ])).toEqual([]);
  });
  test("read-only and management Roles do not imply query or data scope", () => {
    expect(
      scopeOptions(
        roles([rule(A.VERTEX_READ, E.ALLOW, "orders:")]),
        QUERY_SCOPE_ACTIONS,
      ),
    ).toEqual([]);
    expect(
      scopeOptions(roles([rule(A.MANAGE, E.ALLOW, "")]), [A.VERTEX_READ]),
    ).toEqual([]);
    expect(scopeOptions([], [A.VERTEX_READ])).toEqual([]);
  });
  test("literal wildcards stay literal, all-key grants retain exceptions", () => {
    expect(
      scopeOptions(roles([rule(A.VERTEX_READ, E.ALLOW, "orders:*")]), [
        A.VERTEX_READ,
      ])[0].prefix,
    ).toBe("orders:*");
    expect(
      scopeOptions(
        roles([
          rule(A.VERTEX_READ, E.ALLOW, ""),
          rule(A.VERTEX_READ, E.DENY, "private:"),
        ]),
        [A.VERTEX_READ],
      ),
    ).toEqual([{ prefix: "", denied: ["private:"] }]);
  });
});
