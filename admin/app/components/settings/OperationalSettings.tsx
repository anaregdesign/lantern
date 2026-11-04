import {
  Button,
  MessageBar,
  MessageBarBody,
  Spinner,
  Title3,
} from "@fluentui/react-components";
import { operationalDefinitions } from "~/lib/client/usecase/settings/operational-settings-fields";
import {
  retentionAcknowledgementRequired,
  type OperationalSettingsController,
  type OperationalSettingsState,
} from "~/lib/client/usecase/settings/operational-settings-management";
import { OperationalPolicyCard } from "./OperationalPolicyCard";
import { OperationalChangeReview } from "./OperationalChangeReview";
import styles from "./OperationalSettings.module.css";
export function OperationalSettings({
  controller,
  state,
  blocked,
}: {
  controller: OperationalSettingsController | null;
  state: OperationalSettingsState;
  blocked: boolean;
}) {
  const busy = state.mutation === "sending";
  return (
    <section className={styles.groups} aria-label="Operational policies">
      <Title3 as="h2">Operational policies</Title3>
      <p>
        Each group is adopted and changed atomically. Status describes the
        connected node
        {state.settings ? ` (member ${state.settings.member})` : ""}. Bootstrap
        listeners, storage, trust and secret values remain outside these
        policies.
      </p>
      {state.message && (
        <MessageBar
          intent={
            state.phase === "error" ||
            ["conflict", "rejected"].includes(state.mutation)
              ? "error"
              : "info"
          }
          layout="multiline"
        >
          <MessageBarBody>{state.message}</MessageBarBody>
        </MessageBar>
      )}
      {state.stale && (
        <MessageBar intent="warning">
          <MessageBarBody>
            Displayed operational values are stale. Reload before making
            changes.
          </MessageBarBody>
        </MessageBar>
      )}
      {state.originalId && (
        <section
          className={styles.original}
          aria-label="Original operational change status"
        >
          <p>Original operational change {state.originalId}</p>
          <Button
            disabled={busy}
            onClick={() => void controller?.checkStatus()}
          >
            Check original operational change status
          </Button>
          <p>
            {state.result?.original
              ? "An original outcome is recorded. Reload to inspect actual node application."
              : "The original outcome is not confirmed. Current settings do not settle it; check original status before another operational change."}
          </p>
        </section>
      )}
      <Button disabled={busy} onClick={() => void controller?.load()}>
        Reload operational settings
      </Button>
      {state.phase === "loading" && (
        <Spinner label="Loading operational settings" />
      )}
      {state.settings &&
        operationalDefinitions.map((definition) => {
          const settings = state.settings!.groups.find(
            (g) => g.group === definition.group,
          );
          return settings ? (
            <OperationalPolicyCard
              key={definition.group}
              settings={settings}
              selected={state.selected === definition.group}
              draft={
                state.selected === definition.group ? state.draft : undefined
              }
              blocked={blocked}
              retentionRequired={
                state.selected === definition.group &&
                retentionAcknowledgementRequired(state)
              }
              retentionAcknowledged={state.retentionAcknowledged}
              backupPreview={state.backupPreview}
              capacityPreview={state.capacityPreview}
              previewBackup={() => void controller?.previewBackup()}
              previewCapacity={() => void controller?.previewCapacity()}
              select={(adopt) => controller?.select(definition.group, adopt)}
              edit={(key, value) => controller?.edit(key, value)}
              acknowledge={(value) => controller?.acknowledge(value)}
              review={() => void controller?.review()}
            />
          ) : null;
        })}
      {state.review && (
        <OperationalChangeReview
          review={state.review}
          busy={busy}
          apply={() => void controller?.apply()}
          cancel={() => controller?.change.cancelReview()}
          authorize={() => void controller?.change.authorize()}
          checkAuthorization={() =>
            void controller?.change.checkAuthorization()
          }
        />
      )}
    </section>
  );
}
