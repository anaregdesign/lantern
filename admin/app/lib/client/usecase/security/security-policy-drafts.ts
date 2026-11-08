import {
  EdgeEndpointMode,
  RegistrationProfileKind,
  type SecurityIssuer,
  type SecurityRegistrationPolicy,
  type SecurityRegistrationProfile,
} from "lantern-sdk/web";
export const EDGE_MODES = [
  [EdgeEndpointMode.READ_READ, "readable → readable"],
  [EdgeEndpointMode.WRITE_WRITE, "writable → writable"],
  [EdgeEndpointMode.READ_WRITE, "readable → writable"],
  [EdgeEndpointMode.WRITE_READ, "writable → readable"],
] as const;
export function enrollmentProfileKey(profile: SecurityRegistrationProfile) {
  return JSON.stringify([
    profile.issuer,
    profile.kind,
    profile.clientProfileId,
  ]);
}
export function enrollmentChoices(issuers: SecurityIssuer[]) {
  return issuers.flatMap((issuer) =>
    issuer.enabled && !issuer.deleted
      ? [
          ...(issuer.clientId && issuer.redirectUri
            ? [
                {
                  $typeName: "graph.v1.SecurityRegistrationProfile" as const,
                  issuer: issuer.issuer,
                  kind: RegistrationProfileKind.BROWSER,
                  clientProfileId: "browser",
                },
              ]
            : []),
          ...issuer.nativeClients.map((profile) => ({
            $typeName: "graph.v1.SecurityRegistrationProfile" as const,
            issuer: issuer.issuer,
            kind: RegistrationProfileKind.NATIVE,
            clientProfileId: profile.id,
          })),
        ]
      : [],
  );
}
export function registrationDraft(
  policy?: SecurityRegistrationPolicy,
): SecurityRegistrationPolicy {
  if (policy && (policy.version !== 2 || policy.roleId !== "personal_user"))
    throw new Error(
      "Unsupported enrollment policy. Ask the operator to use a compatible policy client.",
    );
  return policy
    ? structuredClone(policy)
    : {
        $typeName: "graph.v1.SecurityRegistrationPolicy",
        version: 2,
        enabled: false,
        roleId: "personal_user",
        profiles: [],
      };
}
