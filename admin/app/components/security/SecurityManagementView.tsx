import { CurrentSecurityDisposition } from "lantern-sdk/web";
import {
  Button,
  Field,
  Input,
  MessageBar,
  MessageBarBody,
  Spinner,
  Title3,
} from "@fluentui/react-components";
import type { AuthContextValue } from "~/lib/client/usecase/auth/use-auth";
import { useSecurityManagement } from "~/lib/client/usecase/security/use-security-management";
import { useSecurityEditor } from "~/lib/client/usecase/security/use-security-editor";
import type {
  SecurityManagementPort,
  SecuritySection,
} from "~/lib/client/usecase/security/security-management";
import { IssuerEditor } from "./IssuerEditor";
import { UserEditor } from "./UserEditor";
import { RoleMembers } from "./RoleMembers";
import { RoleEditor } from "./RoleEditor";
import { ChangeReview } from "./ChangeReview";
import { AccessExplanation } from "./AccessExplanation";
import styles from "./SecurityManagementView.module.css";
import { securityRecoveryOwner } from "~/lib/client/usecase/security/security-change-recovery";
import { useConnection } from "~/lib/client/usecase/connection/connection-context";

export function SecurityManagementView({
  section,
  port,
  auth,
}: {
  section: SecuritySection;
  port: SecurityManagementPort;
  auth: AuthContextValue & {
    state: Extract<AuthContextValue["state"], { kind: "ready" }>;
  };
}) {
  const { principal, signal } = auth.state;
  const { connection } = useConnection();
  const owner = securityRecoveryOwner(connection.baseUrl, principal);
  const { state, controller } = useSecurityManagement(
    port,
    section,
    signal,
    auth.recovery,
    owner,
  );
  const editor = useSecurityEditor(controller);
  const busy = state.mutation === "sending";
  const disabled =
    busy ||
    state.recoveryBlocked ||
    !auth.state.canMutate ||
    state.mutation === "unconfirmed" ||
    state.mutation === "pending" ||
    state.phase !== "ready" ||
    !state.version;
  return (
    <div className={styles.view}>
      {state.message && (
        <MessageBar
          layout="multiline"
          intent={
            state.phase === "error" ||
            state.mutation === "conflict" ||
            state.mutation === "rejected"
              ? "error"
              : "info"
          }
        >
          <MessageBarBody>{state.message}</MessageBarBody>
        </MessageBar>
      )}
      {editor.error && (
        <MessageBar intent="error" layout="multiline">
          <MessageBarBody>{editor.error}</MessageBarBody>
        </MessageBar>
      )}
      {controller.changeId() && (
        <section className={styles.status} aria-label="Change status">
          <p>
            Change {controller.changeId()} · {state.mutation}
          </p>
          <Button
            disabled={busy}
            onClick={() => {
              void controller.checkStatus();
            }}
          >
            Check original change status
          </Button>
          {state.result?.original && (
            <p>
              Original commit {state.result.original.commit?.slot.toString()} ·{" "}
              {state.result.original.items
                .map(
                  (item) =>
                    `${item.index + 1}: ${CurrentSecurityDisposition[item.disposition]}`,
                )
                .join("; ")}
            </p>
          )}
        </section>
      )}
      <div className={styles.actions}>
        <Button
          disabled={busy || state.mutation === "unconfirmed"}
          onClick={() => {
            void controller.load();
          }}
        >
          Reload security state
        </Button>
        <Button
          disabled={disabled}
          onClick={() =>
            section === "issuers"
              ? editor.editIssuer()
              : section === "users"
                ? editor.editUser()
                : editor.editRole()
          }
        >
          New{" "}
          {section === "issuers"
            ? "Issuer"
            : section === "users"
              ? "user"
              : "Role"}
        </Button>
      </div>
      {state.phase === "loading" && state.mutation !== "unconfirmed" && (
        <Spinner label="Loading security state" />
      )}
      {state.version && (
        <p>
          Inspected policy cut {state.version.currentCut?.sequence.toString()}
        </p>
      )}
      <div className={styles.records} aria-label={`${section} list`}>
        {section === "issuers" &&
          state.issuers.map((issuer) => (
            <Button
              key={issuer.issuer}
              disabled={disabled}
              onClick={() => editor.editIssuer(issuer)}
            >
              {issuer.issuer} ·{" "}
              {issuer.deleted
                ? "Deleted"
                : issuer.enabled
                  ? "Enabled"
                  : "Disabled"}
              {issuer.envOwned ? " · Environment-owned" : ""}
              {issuer.hasSecretBinding ? " · Secret bound" : ""}
            </Button>
          ))}
        {section === "users" &&
          state.users.map((user) => (
            <Button
              key={JSON.stringify(user.identity)}
              disabled={disabled}
              onClick={() => editor.editUser(user)}
            >
              {user.identity?.subject || user.identity?.machineName} ·{" "}
              {user.identity?.issuer} · {user.assignments.length} Roles
            </Button>
          ))}
        {section === "roles" &&
          state.roles.map((role) => (
            <Button
              key={role.id}
              disabled={disabled}
              onClick={() => editor.editRole(role)}
            >
              {role.name} ({role.id}) · {role.rules.length} rules
              {role.envOwned ? " · Environment-owned" : ""}
            </Button>
          ))}
      </div>
      {state.nextCursor && (
        <Button
          disabled={disabled}
          onClick={() => {
            void controller.load(state.nextCursor);
          }}
        >
          Next {section} page
        </Button>
      )}
      {section === "issuers" && (
        <IssuerEditor editor={editor} disabled={disabled} />
      )}
      {section === "users" && (
        <UserEditor editor={editor} disabled={disabled} />
      )}
      {section === "roles" && (
        <>
          <section className={styles.templates}>
            <Title3>Role templates</Title3>
            <Field
              label="Template logical prefix"
              hint="Choose the data scope before creating a Role."
            >
              <Input
                value={editor.templatePrefix}
                onChange={(_, data) => editor.setTemplatePrefix(data.value)}
              />
            </Field>
            <Button
              disabled={disabled}
              onClick={() => {
                void controller.loadTemplates(editor.templatePrefix);
              }}
            >
              Load templates
            </Button>
            <div className={styles.records}>
              {state.templates.map((role) => (
                <Button
                  key={role.id}
                  disabled={disabled}
                  onClick={() => editor.useTemplate(role)}
                >
                  Use {role.name}
                </Button>
              ))}
            </div>
          </section>
          <RoleEditor editor={editor} disabled={disabled} />
          {editor.role.existing && (
            <RoleMembers
              roleId={editor.role.id}
              members={state.memberRole === editor.role.id ? state.members : []}
              cursor={
                state.memberRole === editor.role.id ? state.memberCursor : ""
              }
              disabled={disabled}
              load={(cursor) => {
                void controller.loadRoleMembers(editor.role.id, cursor);
              }}
            />
          )}
        </>
      )}
      {state.review && (
        <ChangeReview
          review={state.review}
          busy={busy}
          apply={() => {
            void controller.apply();
          }}
          cancel={() => controller.cancelReview()}
          authorize={() => {
            void controller.authorize();
          }}
          checkAuthorization={() => {
            void controller.checkAuthorization();
          }}
        />
      )}
      <AccessExplanation
        editor={editor}
        result={state.explanation}
        disabled={disabled}
      />
      <section className={styles.audit}>
        <Title3>Security audit</Title3>
        <Button
          disabled={busy}
          onClick={() => {
            void controller.loadAudit();
          }}
        >
          Load audit
        </Button>
        <ul>
          {state.audit.map((record) => (
            <li key={record.result?.original?.commit?.slot.toString()}>
              Commit {record.result?.original?.commit?.slot.toString()} ·{" "}
              {record.operation} ·{" "}
              {record.result?.original &&
                CurrentSecurityDisposition[record.result.original.disposition]}
            </li>
          ))}
        </ul>
        {state.auditCursor && (
          <Button
            disabled={busy}
            onClick={() => {
              void controller.loadAudit(state.auditCursor);
            }}
          >
            Next audit page
          </Button>
        )}
      </section>
    </div>
  );
}
