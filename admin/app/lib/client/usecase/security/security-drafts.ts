import {
  SecurityPermission,
  SecurityResource,
  SecurityPrincipalKind,
  SecurityPrincipalState,
  type SecurityChange,
  type SecurityIdentity,
  type SecurityIssuer,
  type SecurityRole,
  type SecurityRule,
  type SecurityUser,
} from "lantern-sdk/web";

export const PERMISSIONS = [
  [SecurityPermission.READ, "Read"],
  [SecurityPermission.WRITE, "Write"],
] as const;
export const RESOURCES = [
  [SecurityResource.DATA, "Logical data"],
  [SecurityResource.SECURITY, "Security management"],
  [SecurityResource.SETTINGS, "Operational Settings"],
  [SecurityResource.OPERATIONS, "Operations and metrics"],
  [SecurityResource.SCHEMA, "Schema"],
] as const;
export interface IssuerDraft {
  issuer: string;
  clientId: string;
  apiAudience: string;
  redirectUri: string;
  algorithms: string;
  enabled: boolean;
  secretMode: "preserve" | "replace" | "clear";
  secretRef: string;
  hasSecretBinding: boolean;
  locked: boolean;
  existing: boolean;
  humanSubjectNamespaceQualified: boolean;
  // The browser editor preserves profiles managed through operator tools.
  nativeClients: SecurityIssuer["nativeClients"];
}
export interface RuleDraft {
  id: string;
  permission: SecurityPermission;
  resourceKind: SecurityResource;
  prefix: string;
  allKeys: boolean;
  personal: boolean;
  uidBefore: string;
  uidAfter: string;
}
export interface RoleDraft {
  id: string;
  name: string;
  rules: RuleDraft[];
  locked: boolean;
  existing: boolean;
}
export interface UserDraft {
  issuer: string;
  subject: string;
  machineName: string;
  kind: SecurityPrincipalKind;
  state: SecurityPrincipalState;
  roleId: string;
  assignments: SecurityUser["assignments"];
  existing: boolean;
}
export interface ExplanationDraft {
  issuer: string;
  subject: string;
  permission: SecurityPermission;
  resourceKind: SecurityResource;
  key: string;
  edge: boolean;
  tail: string;
  head: string;
}
export function issuerDraft(value?: SecurityIssuer): IssuerDraft {
  return {
    issuer: value?.issuer ?? "",
    clientId: value?.clientId ?? "",
    apiAudience: value?.apiAudience ?? "",
    redirectUri: value?.redirectUri ?? "",
    algorithms: value?.algorithms.join(", ") ?? "EdDSA, RS256, ES256",
    enabled: value?.enabled ?? false,
    secretMode: "preserve",
    secretRef: "",
    hasSecretBinding: value?.hasSecretBinding ?? false,
    locked: value?.envOwned ?? false,
    existing: !!value,
    humanSubjectNamespaceQualified:
      value?.humanSubjectNamespaceQualified ?? false,
    nativeClients: copyNativeClients(value?.nativeClients ?? []),
  };
}
function copyNativeClients(clients: SecurityIssuer["nativeClients"]) {
  return clients.map((client) => ({
    ...client,
    presenters: [...client.presenters],
  }));
}
export function roleDraft(value?: SecurityRole): RoleDraft {
  if (
    value?.rules.some(
      (rule) =>
        !PERMISSIONS.some(([permission]) => permission === rule.permission) ||
        !RESOURCES.some(([resource]) => resource === rule.resourceKind) ||
        (rule.resourceKind === SecurityResource.DATA
          ? rule.resource.case !== "prefix" &&
            rule.resource.case !== "principalUidPrefix"
          : rule.resource.case !== undefined),
    )
  )
    throw new Error(
      "This Role uses an unsupported permission or resource. Ask the operator to use a compatible policy client.",
    );
  return {
    id: value?.id ?? "",
    name: value?.name ?? "",
    existing: !!value,
    locked: value?.envOwned ?? false,
    rules:
      value?.rules.map((rule) => ({
        id: rule.id,
        permission: rule.permission,
        resourceKind: rule.resourceKind,
        prefix: rule.resource.case === "prefix" ? rule.resource.value : "",
        allKeys: rule.resource.case === "prefix" && rule.resource.value === "",
        personal: rule.resource.case === "principalUidPrefix",
        uidBefore:
          rule.resource.case === "principalUidPrefix"
            ? rule.resource.value.literalBefore
            : "users:",
        uidAfter:
          rule.resource.case === "principalUidPrefix"
            ? rule.resource.value.literalAfter
            : ":",
      })) ?? [],
  };
}
export function userDraft(value?: SecurityUser): UserDraft {
  return {
    issuer: value?.identity?.issuer ?? "",
    subject: value?.identity?.subject ?? "",
    machineName: value?.identity?.machineName ?? "",
    kind: value?.identity?.kind ?? SecurityPrincipalKind.OIDC,
    state: value?.state ?? SecurityPrincipalState.ACTIVE,
    roleId: "",
    assignments: value?.assignments ?? [],
    existing: !!value,
  };
}
export function newRule(index: number): RuleDraft {
  return {
    id: `rule-${index}`,
    permission: SecurityPermission.READ,
    resourceKind: SecurityResource.DATA,
    prefix: "",
    allKeys: false,
    personal: false,
    uidBefore: "users:",
    uidAfter: ":",
  };
}
function required(value: string, label: string) {
  if (!value || value !== value.trim())
    throw new Error(
      `${label} is required and must not have surrounding spaces.`,
    );
  return value;
}
function https(value: string, label: string) {
  required(value, label);
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    throw new Error(`${label} must be a valid HTTPS URL.`);
  }
  if (url.protocol !== "https:" || url.username || url.password || url.hash)
    throw new Error(
      `${label} must be an HTTPS URL without credentials or a fragment.`,
    );
  return value;
}
function identifier(value: string, label: string) {
  required(value, label);
  if (!/^[a-z0-9_-]{1,64}$/.test(value))
    throw new Error(
      `${label} must use 1–64 lowercase letters, numbers, hyphens or underscores.`,
    );
  return value;
}
export function buildIssuer(draft: IssuerDraft): SecurityIssuer {
  if (draft.locked)
    throw new Error(
      "Environment-owned Issuers are configured by the operator.",
    );
  const algorithms = draft.algorithms
    .split(",")
    .map((part) => part.trim())
    .filter(Boolean);
  if (
    !algorithms.length ||
    new Set(algorithms).size !== algorithms.length ||
    algorithms.some((value) => !["EdDSA", "RS256", "ES256"].includes(value))
  )
    throw new Error(
      "Choose distinct supported signing algorithms: EdDSA, RS256 or ES256.",
    );
  const issuer = https(draft.issuer, "Issuer");
  if (
    issuer === "https://accounts.google.com" &&
    (algorithms.length !== 1 || algorithms[0] !== "RS256")
  )
    throw new Error("Google requires the RS256 signing algorithm.");
  const secretRef =
    draft.secretMode === "preserve"
      ? undefined
      : draft.secretMode === "clear"
        ? ""
        : identifier(draft.secretRef, "Operator secret handle");
  const browserConfigured = !!(
    draft.clientId ||
    draft.redirectUri ||
    secretRef ||
    (draft.secretMode === "preserve" && draft.hasSecretBinding)
  );
  const apiAudience = draft.apiAudience
    ? required(draft.apiAudience, "API audience")
    : "";
  if (!browserConfigured && !apiAudience && !draft.nativeClients.length)
    throw new Error(
      "Configure browser sign-in, an API audience or a native client.",
    );
  if (draft.humanSubjectNamespaceQualified && !apiAudience)
    throw new Error(
      "Human API subject qualification requires an API audience.",
    );
  return {
    $typeName: "graph.v1.SecurityIssuer",
    issuer,
    clientId: browserConfigured
      ? required(draft.clientId, "Browser client ID")
      : "",
    apiAudience,
    redirectUri: browserConfigured
      ? https(draft.redirectUri, "Browser redirect URI")
      : "",
    algorithms,
    enabled: draft.enabled,
    humanSubjectNamespaceQualified: draft.humanSubjectNamespaceQualified,
    secretRef,
    configRevision: 0n,
    envOwned: false,
    deleted: false,
    hasSecretBinding: false,
    nativeClients: copyNativeClients(draft.nativeClients).map((client) => ({
      ...client,
      hasSecretBinding: false,
    })),
  };
}
export function buildRole(draft: RoleDraft): SecurityRole {
  if (draft.locked)
    throw new Error(
      "Environment-owned Role policies are configured by the operator.",
    );
  const id = identifier(draft.id, "Role ID");
  if (id === "cluster_replica")
    throw new Error(
      "Peer identities are managed on the private workload plane.",
    );
  const ids = new Set<string>();
  const rules: SecurityRule[] = draft.rules.map((rule) => {
    identifier(rule.id, "Rule ID");
    if (ids.has(rule.id))
      throw new Error("Rule IDs must be unique within a Role.");
    ids.add(rule.id);
    if (
      !PERMISSIONS.some(([permission]) => permission === rule.permission) ||
      !RESOURCES.some(([resource]) => resource === rule.resourceKind)
    )
      throw new Error(
        "Choose a supported resource and Read or Write permission.",
      );
    if (
      [SecurityResource.OPERATIONS, SecurityResource.SCHEMA].includes(
        rule.resourceKind,
      ) &&
      rule.permission === SecurityPermission.WRITE
    )
      throw new Error("Operations and Schema support Read only.");
    const data = rule.resourceKind === SecurityResource.DATA;
    if (data && !rule.personal && !rule.allKeys && !rule.prefix)
      throw new Error(
        "Enter a literal logical prefix or explicitly select all data keys.",
      );
    if (
      data &&
      !rule.personal &&
      !rule.allKeys &&
      /^(sys:|data:)/.test(rule.prefix)
    )
      throw new Error(
        "Use public logical keys, without sys: or data: prefixes.",
      );
    if (
      data &&
      rule.personal &&
      (rule.uidBefore !== "users:" || rule.uidAfter !== ":")
    )
      throw new Error("Personal scope must be users:<trusted UID>:.");
    return {
      $typeName: "graph.v1.SecurityRule",
      id: rule.id,
      permission: rule.permission,
      resourceKind: rule.resourceKind,
      resource: !data
        ? { case: undefined }
        : rule.personal
          ? {
              case: "principalUidPrefix",
              value: {
                $typeName: "graph.v1.SecurityPrincipalUIDPrefix",
                literalBefore: rule.uidBefore,
                literalAfter: rule.uidAfter,
              },
            }
          : { case: "prefix", value: rule.allKeys ? "" : rule.prefix },
    };
  });
  return {
    $typeName: "graph.v1.SecurityRole",
    id,
    name: required(draft.name, "Role name"),
    rules,
    envOwned: false,
  };
}
export function userIdentity(draft: UserDraft): SecurityIdentity {
  return {
    $typeName: "graph.v1.SecurityIdentity",
    kind: draft.kind,
    issuer:
      draft.kind === SecurityPrincipalKind.OIDC
        ? https(draft.issuer, "Issuer")
        : "",
    subject:
      draft.kind === SecurityPrincipalKind.OIDC
        ? required(draft.subject, "Exact subject")
        : "",
    machineName:
      draft.kind === SecurityPrincipalKind.MACHINE
        ? required(draft.machineName, "Machine name")
        : "",
  };
}
export function change(operation: SecurityChange["operation"]): SecurityChange {
  return { $typeName: "graph.v1.SecurityChange", operation };
}
export function assignmentChange(
  draft: UserDraft,
  remove = false,
  roleId = draft.roleId,
): SecurityChange {
  const id = identifier(roleId, "Existing Role ID");
  if (id === "cluster_replica")
    throw new Error("Peer identities cannot be assigned to users.");
  if (
    remove &&
    draft.assignments.some(
      (assignment) => assignment.roleId === id && assignment.envOwned,
    )
  )
    throw new Error("Environment-owned membership is locked.");
  return change({
    case: remove ? "deleteAssignment" : "putAssignment",
    value: {
      $typeName: "graph.v1.SecurityRoleAssignment",
      identity: userIdentity(draft),
      roleId: id,
      envOwned: false,
    },
  });
}
export function reviewSummary(changes: SecurityChange[]): string {
  return JSON.stringify(
    changes,
    (key, value: unknown) =>
      key === "$typeName" || value === undefined
        ? undefined
        : typeof value === "bigint"
          ? value.toString()
          : value,
    2,
  );
}
