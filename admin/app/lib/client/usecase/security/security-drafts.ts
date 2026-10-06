import {
  SecurityAction,
  SecurityEffect,
  SecurityPrincipalKind,
  SecurityPrincipalState,
  type SecurityChange,
  type SecurityIdentity,
  type SecurityIssuer,
  type SecurityRole,
  type SecurityRule,
  type SecurityUser,
} from "lantern-sdk/web";

export const ACTIONS = [
  [SecurityAction.VERTEX_READ, "Read vertices"],
  [SecurityAction.VERTEX_WRITE, "Write vertices"],
  [SecurityAction.VERTEX_DELETE, "Delete vertices"],
  [SecurityAction.QUERY, "Search and explore"],
  [SecurityAction.CDC_IDENTITY, "Identity CDC"],
  [SecurityAction.CDC_VALUE, "Value CDC"],
  [SecurityAction.EXPORT, "Export"],
  [SecurityAction.RECEIPT_READ, "Read receipts"],
  [SecurityAction.OPERATIONS_READ, "Observe operations"],
  [SecurityAction.SCHEMA_READ, "Read schema"],
  [SecurityAction.MANAGE, "Manage security"],
] as const;
export const EXPLANATION_ACTIONS = [
  ...ACTIONS,
  [SecurityAction.EDGE_READ, "Read edges"],
  [SecurityAction.EDGE_CREATE, "Create connections"],
  [SecurityAction.EDGE_ADD, "Add edge contributions"],
  [SecurityAction.EDGE_WRITE, "Write edges"],
  [SecurityAction.EDGE_DELETE, "Delete edges"],
] as const;
const GLOBAL_ACTIONS = new Set<SecurityAction>([
  SecurityAction.OPERATIONS_READ,
  SecurityAction.SCHEMA_READ,
  SecurityAction.MANAGE,
]);
export interface IssuerDraft {
  issuer: string;
  clientId: string;
  apiAudience: string;
  redirectUri: string;
  algorithms: string;
  enabled: boolean;
  secretMode: "preserve" | "replace" | "clear";
  secretRef: string;
  locked: boolean;
  existing: boolean;
  humanSubjectNamespaceQualified: boolean;
}
export interface RuleDraft {
  id: string;
  action: SecurityAction;
  effect: SecurityEffect;
  prefix: string;
  allKeys: boolean;
  global: boolean;
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
  action: SecurityAction;
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
    locked: value?.envOwned ?? false,
    existing: !!value,
    humanSubjectNamespaceQualified:
      value?.humanSubjectNamespaceQualified ?? false,
  };
}
export function roleDraft(value?: SecurityRole): RoleDraft {
  if (
    value?.rules.some(
      (rule) =>
        rule.resource.case === "pair" ||
        !ACTIONS.some(([action]) => action === rule.action),
    )
  )
    throw new Error(
      "This Role uses obsolete Edge grants or pair selectors. Ask the operator to migrate its policy.",
    );
  return {
    id: value?.id ?? "",
    name: value?.name ?? "",
    existing: !!value,
    locked: value?.envOwned ?? false,
    rules:
      value?.rules.map((rule) => ({
        id: rule.id,
        action: rule.action,
        effect: rule.effect,
        prefix: rule.resource.case === "prefix" ? rule.resource.value : "",
        allKeys: rule.resource.case === "prefix" && rule.resource.value === "",
        global: rule.resource.case === "global",
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
export function globalAction(action: SecurityAction) {
  return GLOBAL_ACTIONS.has(action);
}
export function edgeSelectorAction(action: SecurityAction) {
  return [
    SecurityAction.EDGE_READ,
    SecurityAction.EDGE_CREATE,
    SecurityAction.EDGE_ADD,
    SecurityAction.EDGE_WRITE,
    SecurityAction.EDGE_DELETE,
    SecurityAction.CDC_IDENTITY,
    SecurityAction.CDC_VALUE,
    SecurityAction.EXPORT,
    SecurityAction.RECEIPT_READ,
  ].includes(action);
}
export function derivedEdgeAction(action: SecurityAction) {
  return [
    SecurityAction.EDGE_READ,
    SecurityAction.EDGE_CREATE,
    SecurityAction.EDGE_ADD,
    SecurityAction.EDGE_WRITE,
    SecurityAction.EDGE_DELETE,
  ].includes(action);
}
export function newRule(index: number): RuleDraft {
  return {
    id: `rule-${index}`,
    action: SecurityAction.VERTEX_READ,
    effect: SecurityEffect.ALLOW,
    prefix: "",
    allKeys: false,
    global: false,
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
  const url = new URL(value);
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
  return {
    $typeName: "graph.v1.SecurityIssuer",
    issuer: https(draft.issuer, "Issuer"),
    clientId: required(draft.clientId, "Client ID"),
    apiAudience: required(draft.apiAudience, "API audience"),
    redirectUri: https(draft.redirectUri, "Redirect URI"),
    algorithms,
    enabled: draft.enabled,
    humanSubjectNamespaceQualified: draft.humanSubjectNamespaceQualified,
    secretRef:
      draft.secretMode === "preserve"
        ? undefined
        : draft.secretMode === "clear"
          ? ""
          : required(draft.secretRef, "Operator secret handle"),
    configRevision: 0n,
    envOwned: false,
    deleted: false,
    hasSecretBinding: false,
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
      !ACTIONS.some(([action]) => action === rule.action) ||
      ![SecurityEffect.ALLOW, SecurityEffect.DENY].includes(rule.effect)
    )
      throw new Error("Choose an action and Allow or Deny.");
    if (globalAction(rule.action) !== rule.global)
      throw new Error("Choose the resource scope appropriate to the action.");
    if (!rule.global && !rule.allKeys && !rule.prefix)
      throw new Error(
        "Enter a literal logical prefix or explicitly select all data keys.",
      );
    if (!rule.global && !rule.allKeys && /^(sys:|data:)/.test(rule.prefix))
      throw new Error(
        "Use public logical keys, without sys: or data: prefixes.",
      );
    return {
      $typeName: "graph.v1.SecurityRule",
      id: rule.id,
      action: rule.action,
      effect: rule.effect,
      resource: rule.global
        ? { case: "global", value: true }
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
