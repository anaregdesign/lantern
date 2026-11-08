import {
  Button,
  Field,
  MessageBar,
  MessageBarBody,
  Select,
  Title3,
} from "@fluentui/react-components";
import { FieldHelp } from "~/components/shared/FieldHelp";
import type { ManagedLogLevel } from "lantern-sdk/web";
import {
  logLevelChoices,
  type SettingsState,
  type SettingsView,
} from "~/lib/client/usecase/settings/settings-management";
import styles from "./LogLevelSettings.module.css";

export type LogLevelSettingsProps = {
  state: SettingsState;
  view: SettingsView;
  editLevel(level: ManagedLogLevel): void;
  review(): void;
  cancelDraft(): void;
};

export function LogLevelSettings({
  state,
  view,
  editLevel,
  review,
  cancelDraft,
}: LogLevelSettingsProps) {
  return (
    <section aria-label="Log level settings" className={styles.panel}>
      <Title3 as="h2">Log level</Title3>
      <dl className={styles.values}>
        <dt>Connected node</dt>
        <dd>{view.member}</dd>
        <dt>Applied / desired generation</dt>
        <dd>{view.generation}</dd>
        <dt>Application</dt>
        <dd>{view.application}</dd>
      </dl>
      {view.applicationError && (
        <MessageBar intent="error">
          <MessageBarBody>{view.applicationError}</MessageBarBody>
        </MessageBar>
      )}
      {view.fenced && (
        <MessageBar intent="warning" layout="multiline">
          <MessageBarBody>
            This node is prepared for managed log policy, but the policy has not
            been adopted. An older server version cannot restart with this
            state.
            {view.preparationId && (
              <p className={styles.preparation}>
                Preparation change {view.preparationId}
              </p>
            )}
          </MessageBarBody>
        </MessageBar>
      )}
      {view.firstAdoption && (
        <p className={styles.notice}>
          Adoption requires all enrolled nodes. It can prevent an older server
          version from restarting, even if the policy is not committed. After
          adoption, environment changes will not replace the managed level.
        </p>
      )}
      <div className={styles.tableWrap}>
        <table className={styles.table}>
          <caption>Log level values and unsaved draft</caption>
          <thead>
            <tr>
              <th scope="col">Setting</th>
              <th scope="col">Effective</th>
              <th scope="col">Desired</th>
              <th scope="col">Draft</th>
              <th scope="col">Effective source</th>
            </tr>
          </thead>
          <tbody>
            <tr>
              <th scope="row">Log level</th>
              <td data-label="Effective">{view.effective}</td>
              <td data-label="Desired">{view.desired}</td>
              <td data-label="Draft">
                <Field
                  label="New log level"
                  hint={
                    <FieldHelp
                      meaning="Minimum severity recorded in structured logs. Higher levels record fewer events."
                      example="Warn records warnings and errors."
                      details="Choose Debug, Info, Warn, or Error. Fresh startup default: Info. Empty does not select a default. Changes apply to new log events after successful Apply; environment settings no longer override an adopted policy."
                    />
                  }
                >
                  <Select
                    aria-label="New log level"
                    disabled={view.blocked}
                    value={String(state.draft)}
                    onChange={(_, data) =>
                      editLevel(Number(data.value) as ManagedLogLevel)
                    }
                  >
                    <option value="0">Choose a level</option>
                    {logLevelChoices.map(({ value, label }) => (
                      <option key={value} value={value}>
                        {label}
                      </option>
                    ))}
                  </Select>
                </Field>
              </td>
              <td data-label="Effective source">{view.source}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <Button appearance="primary" disabled={!view.canReview} onClick={review}>
        Review log level change
      </Button>
      <Button disabled={view.blocked} onClick={cancelDraft}>
        Cancel log level draft
      </Button>
    </section>
  );
}
