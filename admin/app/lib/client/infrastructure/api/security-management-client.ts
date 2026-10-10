import { createSecurityClient } from "./security-client";
import { openSecurityAuthorizationWindow } from "../browser/security-authorization-window";
import { CurrentSecurityInvocationRejectedError } from "lantern-sdk/web";
import type {
  SecurityManagementPort,
  SecurityFailure,
} from "~/lib/client/usecase/security/security-management";

function failure(error: unknown): SecurityFailure {
  for (
    let value = error, depth = 0;
    depth < 4 && value && typeof value === "object";
    depth++
  ) {
    const record = value as { code?: unknown; cause?: unknown };
    if (record.code === 10) return "conflict";
    if (record.code === 7 || record.code === 16) return "denied";
    if (record.code === 5) return "not-found";
    if (record.code === 3 || record.code === 9) return "invalid";
    value = record.cause;
  }
  return "unavailable";
}
export function createSecurityManagementClient(
  baseUrl: string,
  scope: AbortSignal,
  csrf: string,
): SecurityManagementPort {
  const client = createSecurityClient(baseUrl, scope, csrf);
  const options = (signal: AbortSignal) => ({ signal, timeoutMs: 5000 });
  return {
    issuers: (cursor, signal) =>
      client.listIssuers({ limit: 100, cursor }, options(signal)),
    users: (cursor, signal) =>
      client.listUsers({ limit: 100, cursor }, options(signal)),
    roles: (cursor, signal) =>
      client.listRoles({ limit: 100, cursor }, options(signal)),
    templates: (prefix, signal) =>
      client.getRoleTemplates({ prefix }, options(signal)),
    audit: async (cursor, signal) => {
      const response = await client.listSecurityAudit(
        { limit: 100, cursor },
        options(signal),
      );
      return {
        records: response.currentRecords,
        version: response.version,
        nextCursor: response.nextCursor,
      };
    },
    validateIssuer: async (issuer, signal) =>
      (await client.validateIssuer({ issuer }, options(signal))).valid,
    explain: (identity, action, logicalKey, signal, edge) =>
      client.explainAccess(
        { identity, action, logicalKey, edge },
        options(signal),
      ),
    apply: (request, signal) =>
      client.applySecurityChanges(request, options(signal)),
    prepare: (review, signal) =>
      client.prepareSecurityChanges({ currentReview: review }, options(signal)),
    beginAuthorization: (review, signal) =>
      client.beginSecurityChangeAuthorization(
        { currentReview: review },
        options(signal),
      ),
    authorization: (review, authorizationId, attemptAffinity, signal) =>
      client.getSecurityChangeAuthorization(
        { authorizationId, currentProfile: review.profile, attemptAffinity },
        options(signal),
      ),
    openAuthorization: () => openSecurityAuthorizationWindow(baseUrl),
    invocationRejected: (error) =>
      error instanceof CurrentSecurityInvocationRejectedError
        ? error.detail
        : undefined,
    status: async (review, signal) => {
      const response = await client.getSecurityChangeStatus(
        {
          currentProfile: review.profile,
          currentChangeId: review.changeId,
          currentIntentDigest: review.intentDigest,
        },
        options(signal),
      );
      if (!response.currentResult) throw new Error("Missing current result.");
      return response.currentResult;
    },
    failure,
  };
}
