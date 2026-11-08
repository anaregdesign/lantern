import {
  Button,
  Field,
  Input,
  Select,
  Title3,
} from "@fluentui/react-components";
import {
  SecurityPrincipalKind,
  SecurityPrincipalState,
  type SecurityRole,
} from "lantern-sdk/web";
import type { SecurityEditor } from "~/lib/client/usecase/security/use-security-editor";
import { FieldHelp } from "~/components/shared/FieldHelp";
import styles from "./UserEditor.module.css";

export function UserEditor({
  editor,
  disabled,
  roles,
  roleCursor,
  loadRoles,
  membershipOnly = false,
}: {
  editor: SecurityEditor;
  disabled: boolean;
  roles: SecurityRole[];
  roleCursor: string;
  loadRoles(): void;
  membershipOnly?: boolean;
}) {
  const draft = editor.user;
  return (
    <section className={styles.form}>
      <Title3>
        {membershipOnly
          ? "Manage assigned Roles"
          : draft.existing
            ? "Edit user state"
            : "Register user"}
      </Title3>
      <p>
        Identify users by exact Issuer and subject. Account, password and MFA
        settings belong to the identity provider.
      </p>
      <fieldset disabled={disabled} className={styles.fields}>
        {!membershipOnly && (
          <>
            {draft.kind === SecurityPrincipalKind.MACHINE ? (
              <Field
                label="Machine name"
                hint="Exact enrolled machine identity; not an editable display label."
              >
                <Input readOnly value={draft.machineName} />
              </Field>
            ) : (
              <>
                <Field
                  label="User Issuer"
                  required
                  hint={
                    <FieldHelp
                      meaning="Exact configured identity-provider URL; a matching subject at another Issuer is a different user."
                      example="https://idp.example"
                    />
                  }
                >
                  <Input
                    readOnly={draft.existing}
                    value={draft.issuer}
                    onChange={(_, data) =>
                      editor.patchUser({ issuer: data.value })
                    }
                  />
                </Field>
                <Field
                  label="Exact subject"
                  required
                  hint={
                    <FieldHelp
                      meaning="The provider-issued immutable subject; never enter an email to link accounts."
                      example="sample-user-42"
                    />
                  }
                >
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
            <Field
              label="Account state"
              hint="Active permits normal authorization; Suspended blocks access. Deletion and session revocation require their own reviewed change."
            >
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
                <option value={SecurityPrincipalState.SUSPENDED}>
                  Suspended
                </option>
                {draft.state === SecurityPrincipalState.DELETED && (
                  <option value={SecurityPrincipalState.DELETED}>
                    Deleted
                  </option>
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
          </>
        )}
        {membershipOnly &&
          draft.existing &&
          draft.state !== SecurityPrincipalState.DELETED && (
            <>
              <Title3>Assigned Roles</Title3>
              {draft.assignments.length === 0 ? (
                <p>No Roles assigned.</p>
              ) : (
                <ul className={styles.assignments}>
                  {draft.assignments.map((assignment) => (
                    <li key={assignment.roleId}>
                      <span>
                        {roles.find((role) => role.id === assignment.roleId)
                          ?.name || assignment.roleId}{" "}
                        ({assignment.roleId})
                        {assignment.envOwned
                          ? " · Environment-owned, removal locked"
                          : ""}
                      </span>
                      <Button
                        disabled={assignment.envOwned}
                        onClick={() =>
                          editor.removeAssignment(assignment.roleId)
                        }
                      >
                        Remove {assignment.roleId}
                      </Button>
                    </li>
                  ))}
                </ul>
              )}
              <Field
                label="Existing Role ID"
                hint={
                  <FieldHelp
                    meaning="Choose a saved Role. Creating a Role alone grants no user access."
                    example="Project reader (project-reader)"
                    details="Allows from every assigned Role combine by union. Removing one assignment may leave access granted by another Role. Protected environment-owned assignments cannot be removed here."
                  />
                }
              >
                <Select
                  value={draft.roleId}
                  onChange={(_, data) =>
                    editor.patchUser({ roleId: data.value })
                  }
                >
                  <option value="">Choose a Role</option>
                  {roles
                    .filter((role) => role.id !== "cluster_replica")
                    .map((role) => (
                      <option key={role.id} value={role.id}>
                        {role.name} ({role.id})
                      </option>
                    ))}
                </Select>
              </Field>
              {roleCursor && (
                <Button onClick={loadRoles}>Next Role choices</Button>
              )}
              <Button disabled={!draft.roleId} onClick={editor.assignRole}>
                Review Role assignment
              </Button>
            </>
          )}
      </fieldset>
      <Button disabled={disabled} onClick={editor.cancel}>
        Cancel draft
      </Button>
    </section>
  );
}
