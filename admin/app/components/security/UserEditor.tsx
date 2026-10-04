import {
  Button,
  Field,
  Input,
  Select,
  Title3,
} from "@fluentui/react-components";
import { SecurityPrincipalKind, SecurityPrincipalState } from "lantern-sdk/web";
import type { SecurityEditor } from "~/lib/client/usecase/security/use-security-editor";
import styles from "./UserEditor.module.css";

export function UserEditor({
  editor,
  disabled,
}: {
  editor: SecurityEditor;
  disabled: boolean;
}) {
  const draft = editor.user;
  return (
    <section className={styles.form}>
      <Title3>{draft.existing ? "Manage user" : "Register user"}</Title3>
      <p>
        Identify users by exact Issuer and subject. Account, password and MFA
        settings belong to the identity provider.
      </p>
      <fieldset disabled={disabled} className={styles.fields}>
        {draft.kind === SecurityPrincipalKind.MACHINE ? (
          <Field label="Machine name">
            <Input readOnly value={draft.machineName} />
          </Field>
        ) : (
          <>
            <Field label="User Issuer" required>
              <Input
                readOnly={draft.existing}
                value={draft.issuer}
                onChange={(_, data) => editor.patchUser({ issuer: data.value })}
              />
            </Field>
            <Field label="Exact subject" required>
              <Input
                readOnly={draft.existing}
                value={draft.subject}
                onChange={(_, data) =>
                  editor.patchUser({ subject: data.value })
                }
              />
            </Field>
          </>
        )}
        <Field label="Account state">
          <Select
            value={String(draft.state)}
            onChange={(_, data) =>
              editor.patchUser({
                state: Number(data.value) as SecurityPrincipalState,
              })
            }
            disabled={draft.state === SecurityPrincipalState.DELETED}
          >
            <option value={SecurityPrincipalState.ACTIVE}>Active</option>
            <option value={SecurityPrincipalState.SUSPENDED}>Suspended</option>
            {draft.state === SecurityPrincipalState.DELETED && (
              <option value={SecurityPrincipalState.DELETED}>Deleted</option>
            )}
          </Select>
        </Field>
        <div className={styles.actions}>
          <Button
            appearance="primary"
            onClick={editor.saveUser}
            disabled={draft.state === SecurityPrincipalState.DELETED}
          >
            Review user state
          </Button>
          {draft.existing && (
            <>
              <Button onClick={editor.revokeUser}>
                Review session revocation
              </Button>
              <Button
                onClick={editor.deleteUser}
                disabled={draft.state === SecurityPrincipalState.DELETED}
              >
                Review user deletion
              </Button>
            </>
          )}
        </div>
        {draft.existing && draft.state !== SecurityPrincipalState.DELETED && (
          <>
            <Title3>Assigned Roles</Title3>
            {draft.assignments.length === 0 ? (
              <p>No Roles assigned.</p>
            ) : (
              <ul className={styles.assignments}>
                {draft.assignments.map((assignment) => (
                  <li key={assignment.roleId}>
                    <span>
                      {assignment.roleId}
                      {assignment.envOwned ? " · Environment-owned" : ""}
                    </span>
                    <Button
                      disabled={assignment.envOwned}
                      onClick={() => editor.removeAssignment(assignment.roleId)}
                    >
                      Remove {assignment.roleId}
                    </Button>
                  </li>
                ))}
              </ul>
            )}
            <Field
              label="Existing Role ID"
              hint="Permissions are granted through Roles only."
            >
              <Input
                value={draft.roleId}
                onChange={(_, data) => editor.patchUser({ roleId: data.value })}
              />
            </Field>
            <Button onClick={editor.assignRole}>Review Role assignment</Button>
          </>
        )}
      </fieldset>
    </section>
  );
}
