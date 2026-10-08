import {
  SecurityAction,
  SecurityEffect,
  type SecurityRole,
} from "lantern-sdk/web";

type Roles = readonly Pick<SecurityRole, "id" | "rules">[];
export interface ScopeOption {
  prefix: string;
  denied: string[];
}

/** Suggestions from the Server's current Roles, never an authorization decision. */
export function scopeOptions(
  roles: Roles,
  actions: readonly SecurityAction[],
): ScopeOption[] {
  if (!actions.length) return [];
  const rules = roles.flatMap((role) => role.rules);
  let prefixes = [""];
  for (const action of actions) {
    const grants = rules.flatMap((rule) =>
      rule.action === action &&
      rule.effect === SecurityEffect.ALLOW &&
      rule.resource.case === "prefix"
        ? [rule.resource.value]
        : [],
    );
    prefixes = prefixes.flatMap((left) =>
      grants.flatMap((right) =>
        left.startsWith(right) ? [left] : right.startsWith(left) ? [right] : [],
      ),
    );
  }
  const denies = deniedScopes(roles, actions, "");
  return [...new Set(prefixes)]
    .filter((prefix) => !denies.some((denied) => prefix.startsWith(denied)))
    .sort()
    .map((prefix) => ({
      prefix,
      denied: denies.filter((denied) => denied.startsWith(prefix)),
    }));
}

export function deniedScopes(
  roles: Roles,
  actions: readonly SecurityAction[],
  prefix: string,
): string[] {
  return [
    ...new Set(
      roles.flatMap((role) =>
        role.rules.flatMap((rule) =>
          actions.includes(rule.action) &&
          rule.effect === SecurityEffect.DENY &&
          rule.resource.case === "prefix" &&
          (rule.resource.value.startsWith(prefix) ||
            prefix.startsWith(rule.resource.value))
            ? [rule.resource.value]
            : [],
        ),
      ),
    ),
  ].sort();
}

export const BROWSE_SCOPE_ACTIONS = [SecurityAction.VERTEX_READ] as const;

/** Chooses an optional receipt strategy; the Server still authorizes every Add. */
export function receiptSuggestedForEdge(
  roles: Roles,
  tail: string,
  head: string,
): boolean {
  const rules = roles
    .flatMap((role) => role.rules)
    .filter(
      (rule) =>
        rule.action === SecurityAction.RECEIPT_READ &&
        rule.resource.case === "prefix",
    );
  return [tail, head].every((key) => {
    const matches = rules.filter(
      (rule) =>
        rule.resource.case === "prefix" && key.startsWith(rule.resource.value),
    );
    return (
      matches.some((rule) => rule.effect === SecurityEffect.ALLOW) &&
      !matches.some((rule) => rule.effect === SecurityEffect.DENY)
    );
  });
}
export const QUERY_SCOPE_ACTIONS = [
  SecurityAction.VERTEX_READ,
  SecurityAction.QUERY,
] as const;
