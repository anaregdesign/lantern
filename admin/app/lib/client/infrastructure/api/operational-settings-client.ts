import { createSecurityClient } from "./security-client";
import { createSecurityManagementClient } from "./security-management-client";
import type { OperationalSettingsPort } from "~/lib/client/usecase/settings/operational-settings-management";
export function createOperationalSettingsClient(
  baseUrl: string,
  scope: AbortSignal,
  csrf: string,
): OperationalSettingsPort {
  const client = createSecurityClient(baseUrl, scope, csrf);
  return {
    ...createSecurityManagementClient(baseUrl, scope, csrf),
    settings: (signal) =>
      client.getOperationalSettings({}, { signal, timeoutMs: 5000 }),
    preview: (values, signal) =>
      client.previewOperationalPolicy({ values }, { signal, timeoutMs: 5000 }),
  };
}
