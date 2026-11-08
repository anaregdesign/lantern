import { OperationalSettings } from "~/components/settings/OperationalSettings";
import { useOperationalSettingsClient } from "~/lib/client/infrastructure/api/use-operational-settings-client";
import { useOperationalSettingsManagement } from "~/lib/client/usecase/settings/use-operational-settings-management";
import { operationalPolicyRecoveryOwner } from "~/lib/client/usecase/security/security-change-recovery";
import { SettingsPage } from "~/components/settings/SettingsPage";
import { useLogSettingsClient } from "~/lib/client/infrastructure/api/use-log-settings-client";
import { useAuth } from "~/lib/client/usecase/auth/use-auth";
import { useConnection } from "~/lib/client/usecase/connection/connection-context";
import { logPolicyRecoveryOwner } from "~/lib/client/usecase/security/security-change-recovery";
import { useSettingsManagement } from "~/lib/client/usecase/settings/use-settings-management";

export function meta() {
  return [{ title: "Settings — Lantern Admin" }];
}

export default function SettingsRoute() {
  const auth = useAuth();
  const port = useLogSettingsClient();
  const operationalPort = useOperationalSettingsClient();
  const { connection } = useConnection();
  const ready = auth.state.kind === "ready" ? auth.state : undefined;
  const owner = ready
    ? logPolicyRecoveryOwner(connection.baseUrl, ready.principal)
    : "";
  const { controller, state, view } = useSettingsManagement(
    port,
    ready?.signal,
    auth.recovery,
    owner,
    ready?.canMutate ?? false,
  );
  const operational = useOperationalSettingsManagement(
    operationalPort,
    ready?.signal,
    auth.recovery,
    ready
      ? operationalPolicyRecoveryOwner(connection.baseUrl, ready.principal)
      : "",
    ready?.canMutate ?? false,
  );
  return (
    <SettingsPage
      authentication={
        auth.state.kind === "off" ? "off" : ready ? "ready" : "required"
      }
      state={state}
      view={view}
      editLevel={(level) => controller?.editLevel(level)}
      reload={() => {
        void controller?.load();
      }}
      review={() => {
        void controller?.review();
      }}
      apply={() => {
        void controller?.apply();
      }}
      cancel={() => controller?.change.cancelReview()}
      cancelDraft={() => controller?.cancelDraft()}
      checkStatus={() => {
        void controller?.checkStatus();
      }}
      authorize={() => {
        void controller?.change.authorize();
      }}
      checkAuthorization={() => {
        void controller?.change.checkAuthorization();
      }}
    >
      <OperationalSettings
        controller={operational.controller}
        state={operational.state}
        blocked={operational.blocked}
      />
    </SettingsPage>
  );
}
