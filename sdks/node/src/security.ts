import type { MessageInitShape } from "@bufbuild/protobuf";
import { createClient, type Client, type CallOptions, type Transport } from "@connectrpc/connect";
import { wrapConnectError } from "./errors.js";
import {
  LanternSecurityService,
  ApplySecurityChangeRequestSchema,
  ApplySecurityChangesRequestSchema,
  ExplainAccessRequestSchema,
  GetAuthCapabilitiesRequestSchema,
  GetCurrentPrincipalRequestSchema,
  GetIssuerRequestSchema,
  GetRoleRequestSchema,
  GetRoleTemplatesRequestSchema,
  GetSecurityChangeStatusRequestSchema,
  GetUserRequestSchema,
  ListRoleAssignmentsRequestSchema,
  ListIssuersRequestSchema,
  ListRolesRequestSchema,
  ListUsersRequestSchema,
  ListSecurityAuditRequestSchema,
  ValidateIssuerRequestSchema,
} from "./gen/graph/v1/security_pb.js";

/** Thin control-plane facade. The Server owns identity, Role and CAS semantics.
 * Mutations receive one attempt; applications retain change IDs for status lookup.
 * Browser cookies, CSRF and session lifetime are application-owned transport policy.
 */
export class SecurityClient {
  private readonly client: Client<typeof LanternSecurityService>;
  private constructor(transport: Transport) {
    this.client = createClient(LanternSecurityService, transport);
  }
  static withTransport(transport: Transport): SecurityClient {
    return new SecurityClient(transport);
  }
  private async invoke<T>(call: () => Promise<T>): Promise<T> {
    try {
      return await call();
    } catch (error) {
      throw wrapConnectError(error);
    }
  }
  getAuthCapabilities(
    request: MessageInitShape<typeof GetAuthCapabilitiesRequestSchema> = {},
    options?: CallOptions,
  ) {
    return this.invoke(() => this.client.getAuthCapabilities(request, options));
  }
  getCurrentPrincipal(
    request: MessageInitShape<typeof GetCurrentPrincipalRequestSchema> = {},
    options?: CallOptions,
  ) {
    return this.invoke(() => this.client.getCurrentPrincipal(request, options));
  }
  listIssuers(
    request: MessageInitShape<typeof ListIssuersRequestSchema> = {},
    options?: CallOptions,
  ) {
    return this.invoke(() => this.client.listIssuers(request, options));
  }
  getIssuer(request: MessageInitShape<typeof GetIssuerRequestSchema> = {}, options?: CallOptions) {
    return this.invoke(() => this.client.getIssuer(request, options));
  }
  listRoles(request: MessageInitShape<typeof ListRolesRequestSchema> = {}, options?: CallOptions) {
    return this.invoke(() => this.client.listRoles(request, options));
  }
  getRole(request: MessageInitShape<typeof GetRoleRequestSchema> = {}, options?: CallOptions) {
    return this.invoke(() => this.client.getRole(request, options));
  }
  listUsers(request: MessageInitShape<typeof ListUsersRequestSchema> = {}, options?: CallOptions) {
    return this.invoke(() => this.client.listUsers(request, options));
  }
  getUser(request: MessageInitShape<typeof GetUserRequestSchema> = {}, options?: CallOptions) {
    return this.invoke(() => this.client.getUser(request, options));
  }
  listRoleAssignments(
    request: MessageInitShape<typeof ListRoleAssignmentsRequestSchema> = {},
    options?: CallOptions,
  ) {
    return this.invoke(() => this.client.listRoleAssignments(request, options));
  }
  listSecurityAudit(
    request: MessageInitShape<typeof ListSecurityAuditRequestSchema> = {},
    options?: CallOptions,
  ) {
    return this.invoke(() => this.client.listSecurityAudit(request, options));
  }
  getRoleTemplates(
    request: MessageInitShape<typeof GetRoleTemplatesRequestSchema> = {},
    options?: CallOptions,
  ) {
    return this.invoke(() => this.client.getRoleTemplates(request, options));
  }
  explainAccess(
    request: MessageInitShape<typeof ExplainAccessRequestSchema> = {},
    options?: CallOptions,
  ) {
    return this.invoke(() => this.client.explainAccess(request, options));
  }
  validateIssuer(
    request: MessageInitShape<typeof ValidateIssuerRequestSchema> = {},
    options?: CallOptions,
  ) {
    return this.invoke(() => this.client.validateIssuer(request, options));
  }
  applySecurityChanges(
    request: MessageInitShape<typeof ApplySecurityChangesRequestSchema> = {},
    options?: CallOptions,
  ) {
    return this.invoke(() => this.client.applySecurityChanges(request, options));
  }
  applySecurityChange(
    request: MessageInitShape<typeof ApplySecurityChangeRequestSchema> = {},
    options?: CallOptions,
  ) {
    return this.invoke(() => this.client.applySecurityChange(request, options));
  }
  getSecurityChangeStatus(
    request: MessageInitShape<typeof GetSecurityChangeStatusRequestSchema> = {},
    options?: CallOptions,
  ) {
    return this.invoke(() => this.client.getSecurityChangeStatus(request, options));
  }
}
