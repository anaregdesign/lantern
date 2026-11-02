import { createSecurityClient } from "./security-client";
import { createSecurityManagementClient } from "./security-management-client";
import type { LogSettingsPort } from "~/lib/client/usecase/settings/settings-management";

export function createLogSettingsClient(
  baseUrl: string,
  scope: AbortSignal,
  csrf: string,
): LogSettingsPort {
  const client = createSecurityClient(baseUrl, scope, csrf);
  return {
    ...createSecurityManagementClient(baseUrl, scope, csrf),
    settings: (signal) =>
      client.getLogSettings({}, { signal, timeoutMs: 5000 }),
  };
}
