import type { MessageInitShape } from "@bufbuild/protobuf";
import {
  createClient,
  ConnectError,
  type Client,
  type CallOptions,
  type Transport,
} from "@connectrpc/connect";
import { wrapConnectError, FailedPreconditionError, LanternError } from "./errors.js";
import type {
  SecurityOperationAuthorizationRequired,
  SecurityChangePrecommitRejected,
} from "./gen/graph/v1/security_pb.js";
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
  PrepareSecurityChangesRequestSchema,
  BeginSecurityChangeAuthorizationRequestSchema,
  GetSecurityChangeAuthorizationRequestSchema,
  SecurityOperationAuthorizationRequiredSchema,
  SecurityChangePrecommitRejectedSchema,
  SecurityChangeRejectionReason,
} from "./gen/graph/v1/security_pb.js";

/** This invocation definitely did not commit. Earlier uncertain attempts
 * remain uncertain and must be reconciled using their original change ID. */
export class SecurityOperationAuthorizationRequiredError extends FailedPreconditionError {
  constructor(
    readonly detail: SecurityOperationAuthorizationRequired,
    cause: unknown,
  ) {
    super("Reauthenticate the exact reviewed security operation.", { cause });
    this.name = "SecurityOperationAuthorizationRequiredError";
  }
}

/** This Apply invocation was refused before persistence. It does not settle
 * previous attempts; applications must correlate ID/revision and dispatch history. */
export class SecurityChangePrecommitRejectedError extends LanternError {
  constructor(
    readonly detail: SecurityChangePrecommitRejected,
    cause: unknown,
  ) {
    super("The security change was refused before commit.", { cause });
    this.name = "SecurityChangePrecommitRejectedError";
  }
}
function validPrecommitDetail(detail: SecurityChangePrecommitRejected, code: number): boolean {
  const reason = detail.reason;
  const expectedCode =
    reason === SecurityChangeRejectionReason.INVALID_CHANGES
      ? 3
      : reason === SecurityChangeRejectionReason.REVISION_CONFLICT
        ? 10
        : [
              SecurityChangeRejectionReason.UNKNOWN_ROLE,
              SecurityChangeRejectionReason.ISSUER_VALIDATION,
              SecurityChangeRejectionReason.ENVIRONMENT_OWNED,
              SecurityChangeRejectionReason.LAST_ADMINISTRATOR,
            ].includes(reason)
          ? 9
          : 0;
  return (
    expectedCode !== 0 &&
    code === expectedCode &&
    detail.changeId.length === 16 &&
    detail.changeId.some((byte) => byte !== 0) &&
    detail.expectedRevision > 0n &&
    detail.expectedRevision <= 0xffffffffffffffffn
  );
}

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
  private async invoke<T>(call: () => Promise<T>, apply = false): Promise<T> {
    try {
      return await call();
    } catch (error) {
      const failure = ConnectError.from(error);
      const authorization = failure.findDetails(SecurityOperationAuthorizationRequiredSchema);
      const rejections = failure.findDetails(SecurityChangePrecommitRejectedSchema);
      // Conflicting/duplicated details cannot establish a definite first refusal.
      if (
        failure.details.length === 1 &&
        authorization.length === 1 &&
        rejections.length === 0 &&
        failure.code === 9
      )
        throw new SecurityOperationAuthorizationRequiredError(authorization[0]!, error);
      if (
        apply &&
        failure.details.length === 1 &&
        rejections.length === 1 &&
        authorization.length === 0 &&
        validPrecommitDetail(rejections[0]!, failure.code)
      )
        throw new SecurityChangePrecommitRejectedError(rejections[0]!, error);
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
    return this.invoke(() => this.client.applySecurityChanges(request, options), true);
  }
  prepareSecurityChanges(
    request: MessageInitShape<typeof PrepareSecurityChangesRequestSchema> = {},
    options?: CallOptions,
  ) {
    return this.invoke(() => this.client.prepareSecurityChanges(request, options));
  }
  beginSecurityChangeAuthorization(
    request: MessageInitShape<typeof BeginSecurityChangeAuthorizationRequestSchema> = {},
    options?: CallOptions,
  ) {
    return this.invoke(() => this.client.beginSecurityChangeAuthorization(request, options));
  }
  getSecurityChangeAuthorization(
    request: MessageInitShape<typeof GetSecurityChangeAuthorizationRequestSchema> = {},
    options?: CallOptions,
  ) {
    return this.invoke(() => this.client.getSecurityChangeAuthorization(request, options));
  }
  applySecurityChange(
    request: MessageInitShape<typeof ApplySecurityChangeRequestSchema> = {},
    options?: CallOptions,
  ) {
    return this.invoke(() => this.client.applySecurityChange(request, options), true);
  }
  getSecurityChangeStatus(
    request: MessageInitShape<typeof GetSecurityChangeStatusRequestSchema> = {},
    options?: CallOptions,
  ) {
    return this.invoke(() => this.client.getSecurityChangeStatus(request, options));
  }
}
