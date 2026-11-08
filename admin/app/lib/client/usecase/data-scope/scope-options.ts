import {
  SecurityPermission,
  SecurityResource,
  type SecurityRole,
} from "lantern-sdk/web";

type Roles = readonly Pick<SecurityRole, "id" | "rules">[];
export type ScopeOption = { prefix: string };

/** Navigation suggestions from current Server Roles, never an authorization decision. */
export function scopeOptions(
  roles: Roles,
  trustedUserId?: string,
): ScopeOption[] {
  const personal =
    trustedUserId &&
    /^[0-9a-f]{32}$/.test(trustedUserId) &&
    /[1-9a-f]/.test(trustedUserId);
  return [
    ...new Set(
      roles.flatMap((role) =>
        role.rules.flatMap((rule) => {
          if (
            rule.resourceKind !== SecurityResource.DATA ||
            rule.permission !== SecurityPermission.READ
          )
            return [];
          if (rule.resource.case === "prefix") return [rule.resource.value];
          if (rule.resource.case === "principalUidPrefix" && personal) {
            const { literalBefore, literalAfter } = rule.resource.value;
            if (literalBefore.endsWith(":") && literalAfter.startsWith(":"))
              return [literalBefore + trustedUserId + literalAfter];
          }
          return [];
        }),
      ),
    ),
  ]
    .sort()
    .map((prefix) => ({ prefix }));
}

/** Chooses an optional receipt strategy; original actor/operation/output checks remain on Server. */
export function receiptSuggestedForEdge(
  roles: Roles,
  tail: string,
  head: string,
  trustedUserId?: string,
): boolean {
  const prefixes = scopeOptions(roles, trustedUserId);
  return [tail, head].every((key) =>
    prefixes.some(({ prefix }) => key.startsWith(prefix)),
  );
}
