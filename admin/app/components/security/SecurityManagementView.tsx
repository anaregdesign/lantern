import { CurrentSecurityDisposition } from "lantern-sdk/web";
import { useEffect, useRef } from "react";
import {
  Button,
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
import { SecurityRecords } from "./SecurityRecords";
import { SecurityRecordDetails } from "./SecurityRecordDetails";
import { RoleTemplatePicker } from "./RoleTemplatePicker";
import { SecurityPolicySettings } from "./SecurityPolicySettings";
import { useSecurityPolicies } from "~/lib/client/usecase/security/use-security-policies";
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
    auth.drafts,
    auth.draftOwner,
  );
  const editor = useSecurityEditor(controller);
  const policies = useSecurityPolicies(controller, state);
  const busy = state.mutation === "sending";
  const disabled =
    busy ||
    state.recoveryBlocked ||
    !auth.state.canMutate ||
    state.mutation === "unconfirmed" ||
    state.mutation === "pending" ||
    state.phase !== "ready" ||
    !state.version;
  const inspectionDisabled =
    busy ||
    state.mutation === "unconfirmed" ||
    state.mutation === "pending" ||
    state.phase !== "ready";
  const editing = editor.mode === "edit" || editor.mode === "create";
  const showReview =
    !!state.review &&
    state.mutation !== "unconfirmed" &&
    state.mutation !== "pending";
  const feedback = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (state.message && !showReview && state.mutation !== "idle") {
      feedback.current?.focus();
      feedback.current?.scrollIntoView({ block: "nearest" });
    }
  }, [state.message, state.mutation, showReview]);
  return (
    <div className={styles.view}>
      {state.message && !showReview && (
        <div ref={feedback} tabIndex={-1}>
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
        </div>
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
          appearance="primary"
          disabled={disabled}
          onClick={() =>
            section === "issuers"
              ? editor.editIssuer()
              : section === "users"
                ? editor.editUser()
                : editor.editRole()
          }
        >
          {section === "issuers"
            ? "Add Issuer"
            : section === "users"
              ? "Register user"
              : "Create Role"}
        </Button>
        <Button disabled={inspectionDisabled} onClick={editor.showExplanation}>
          Explain another user&apos;s access
        </Button>
      </div>
      {!auth.state.canMutate && (
        <p>
          Read-only inspection. A supported human session with the required
          write grant is needed to review changes.
        </p>
      )}
      {state.phase === "loading" && state.mutation !== "unconfirmed" && (
        <Spinner label="Loading security state" />
      )}
      {state.version && (
        <p>
          Inspected policy cut {state.version.currentCut?.sequence.toString()}
        </p>
      )}
      <SecurityRecords
        section={section}
        state={state}
        editor={editor}
        disabled={inspectionDisabled}
      />
      {state.nextCursor && (
        <Button
          disabled={inspectionDisabled}
          onClick={() => {
            editor.close();
            void controller.load(state.nextCursor);
          }}
        >
          Next {section} page
        </Button>
      )}
      {editor.mode !== "list" && (
        <section
          className={styles.workspace}
          aria-label="Selected security task"
        >
          <div className={styles.actions}>
            <Button
              disabled={busy || state.mutation === "unconfirmed"}
              onClick={editor.close}
            >
              Back to list
            </Button>
          </div>
          {(editor.mode === "details" || editor.mode === "members") && (
            <SecurityRecordDetails editor={editor} disabled={disabled} />
          )}
          {editing && editor.selectedKind === "issuers" && (
            <IssuerEditor editor={editor} disabled={disabled} />
          )}
          {editing && editor.selectedKind === "roles" && (
            <>
              {editor.mode === "create" && (
                <RoleTemplatePicker
                  editor={editor}
                  state={state}
                  disabled={disabled}
                  load={() => {
                    void controller.loadTemplates(editor.templatePrefix);
                  }}
                />
              )}
              {(editor.mode !== "create" ||
                editor.roleSource === "blank" ||
                editor.templateName) && (
                <RoleEditor editor={editor} disabled={disabled} />
              )}
            </>
          )}
          {(editing || editor.mode === "members") &&
            editor.selectedKind === "users" && (
              <UserEditor
                editor={editor}
                disabled={disabled}
                roles={state.roles}
                roleCursor={state.roleCursor}
                loadRoles={() => {
                  void controller.loadRoleChoices(state.roleCursor);
                }}
                membershipOnly={editor.mode === "members"}
              />
            )}
          {editor.mode === "members" &&
            editor.selectedKind === "roles" &&
            editor.role.existing && (
              <RoleMembers
                roleId={editor.role.id}
                members={
                  state.memberRole === editor.role.id ? state.members : []
                }
                cursor={
                  state.memberRole === editor.role.id ? state.memberCursor : ""
                }
                disabled={disabled}
                load={(cursor) => {
                  void controller.loadRoleMembers(editor.role.id, cursor);
                }}
                select={(user) => {
                  editor.editUser(user);
                }}
              />
            )}
          {editor.mode === "explain" && (
            <AccessExplanation
              editor={editor}
              result={state.explanation}
              disabled={inspectionDisabled}
            />
          )}
        </section>
      )}
      {showReview && state.review && (
        <ChangeReview
          review={state.review}
          busy={busy}
          message={state.message}
          apply={() => {
            const operation = state.review?.changes[0]?.operation.case;
            if (
              operation === "putEdgePolicy" ||
              operation === "putRegistrationPolicy"
            )
              void policies.apply();
            else void editor.apply();
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
      <SecurityPolicySettings
        state={state}
        policies={policies}
        disabled={disabled}
        busy={busy}
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
