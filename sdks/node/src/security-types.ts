import { fromJsonString } from "@bufbuild/protobuf";
import { BrowserSessionSchema, SessionRevocationSchema } from "./gen/graph/v1/security_pb.js";
export {
  AuthMode,
  SecurityAction,
  SecurityEffect,
  SecurityEnforcementState,
  SecurityPrincipalKind,
  SecurityPrincipalState,
} from "./gen/graph/v1/security_pb.js";
export type {
  GetAuthCapabilitiesResponse,
  LoginIssuer,
  GetCurrentPrincipalResponse,
  BrowserSession,
  SessionRevocation,
  SecurityIdentity,
  SecurityRole,
  SecurityRule,
  SecurityPrefixPair,
  SecurityEdgeIdentity,
  SecurityUser,
  SecurityIssuer,
  SecurityRoleAssignment,
  SecurityVersion,
  SecurityChange,
  SecurityAuditRecord,
  ApplySecurityChangesResponse,
  GetSecurityChangeStatusResponse,
  GetRoleTemplatesResponse,
  ListRolesResponse,
  ListIssuersResponse,
  ListUsersResponse,
  ExplainAccessResponse,
} from "./gen/graph/v1/security_pb.js";
export function parseBrowserSession(json: string) {
  return fromJsonString(BrowserSessionSchema, json);
}
export function parseSessionRevocation(json: string) {
  return fromJsonString(SessionRevocationSchema, json);
}
