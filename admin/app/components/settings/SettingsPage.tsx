import {
  Button,
  MessageBar,
  MessageBarBody,
  Spinner,
  Title2,
} from "@fluentui/react-components";
import type { ManagedLogLevel } from "lantern-sdk/web";
import type {
  SettingsState,
  SettingsView,
} from "~/lib/client/usecase/settings/settings-management";
import { LogLevelSettings } from "./LogLevelSettings";
import { SettingsChangeReview } from "./SettingsChangeReview";
import styles from "./SettingsPage.module.css";

export type SettingsPageProps = {
  authentication: "off" | "ready" | "required";
  state: SettingsState;
  view: SettingsView;
  editLevel(level: ManagedLogLevel): void;
  reload(): void;
  review(): void;
  apply(): void;
  cancel(): void;
  checkStatus(): void;
  authorize(): void;
  checkAuthorization(): void;
};

export function SettingsPage(props: SettingsPageProps) {
  const { authentication, state, view } = props;
  return (
    <section className={styles.page}>
      <Title2 as="h1">Settings</Title2>
      {authentication === "off" ? (
        <p>
          Authentication is off. Log level remains environment-owned. OIDC is
          required to manage settings.
        </p>
      ) : authentication !== "ready" ? (
        <p>Authentication is required to manage settings.</p>
      ) : (
        <>
          <p>
            Changes are saved as shared policy. Application status below
            describes this connected node.
          </p>
          {state.message && (
            <MessageBar
              intent={
                state.phase === "error" ||
                state.mutation === "rejected" ||
                state.mutation === "conflict"
                  ? "error"
                  : "info"
              }
              layout="multiline"
            >
              <MessageBarBody>{state.message}</MessageBarBody>
            </MessageBar>
          )}
          {state.stale && (
            <MessageBar intent="warning" layout="multiline">
              <MessageBarBody>
                These values are stale. Reload current settings before making
                another change.
              </MessageBarBody>
            </MessageBar>
          )}
          {view.changeId && (
            <section
              aria-label="Original change status"
              className={styles.status}
            >
              <p>Original change {view.changeId}</p>
              <Button disabled={view.busy} onClick={props.checkStatus}>
                Check original change status
              </Button>
              {state.result?.original && (
                <p>
                  The original operation has a recorded outcome. Reload settings
                  to check this node’s current application.
                </p>
              )}
              {!state.result?.original && (
                <p>
                  The original change is not confirmed. Current settings do not
                  settle its outcome; check original status before another
                  change.
                </p>
              )}
            </section>
          )}
          <Button disabled={view.busy} onClick={props.reload}>
            Reload settings
          </Button>
          {state.phase === "loading" && <Spinner label="Loading settings" />}
          {state.settings && (
            <LogLevelSettings
              state={state}
              view={view}
              editLevel={props.editLevel}
              review={props.review}
            />
          )}
          {state.review && (
            <SettingsChangeReview
              review={state.review}
              firstAdoption={view.firstAdoption}
              busy={view.busy}
              apply={props.apply}
              cancel={props.cancel}
              authorize={props.authorize}
              checkAuthorization={props.checkAuthorization}
            />
          )}
        </>
      )}
    </section>
  );
}
