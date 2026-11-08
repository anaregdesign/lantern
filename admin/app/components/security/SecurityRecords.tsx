import { Badge, Button } from "@fluentui/react-components";
import { SecurityPrincipalState } from "lantern-sdk/web";
import type {
  SecurityManagementState,
  SecuritySection,
} from "~/lib/client/usecase/security/security-management";
import type { SecurityEditor } from "~/lib/client/usecase/security/use-security-editor";
import styles from "./SecurityRecords.module.css";

export function SecurityRecords({
  section,
  state,
  editor,
  disabled,
}: {
  section: SecuritySection;
  state: SecurityManagementState;
  editor: SecurityEditor;
  disabled: boolean;
}) {
  const records =
    section === "users"
      ? state.users
      : section === "roles"
        ? state.roles
        : state.issuers;
  return (
    <section aria-label={`${section} list`}>
      {state.phase === "ready" && records.length === 0 ? (
        <p>No {section} on this page.</p>
      ) : (
        <table className={styles.table}>
          <caption>
            {section === "users"
              ? "Users and assigned Roles"
              : section === "roles"
                ? "Saved Roles"
                : "Identity providers"}
          </caption>
          <thead>
            <tr>
              <th scope="col">
                {section === "users"
                  ? "User"
                  : section === "roles"
                    ? "Role"
                    : "Issuer"}
              </th>
              <th scope="col">
                {section === "users"
                  ? "Issuer / state"
                  : section === "roles"
                    ? "Permissions"
                    : "Profiles / state"}
              </th>
              <th scope="col">
                {section === "users" ? "Assigned Roles" : "Ownership"}
              </th>
            </tr>
          </thead>
          <tbody>
            {section === "users" &&
              state.users.map((user) => (
                <tr key={JSON.stringify(user.identity)}>
                  <td data-label="User">
                    <Button
                      disabled={disabled}
                      appearance="transparent"
                      onClick={() => editor.editUser(user)}
                    >
                      {user.verifiedEmail ||
                        user.identity?.subject ||
                        user.identity?.machineName}
                    </Button>
                    <small>
                      {user.verifiedEmail &&
                        (user.identity?.subject || user.identity?.machineName)}
                    </small>
                  </td>
                  <td data-label="Issuer / state">
                    {user.identity?.issuer || "Machine"}
                    <br />
                    {SecurityPrincipalState[user.state]}
                  </td>
                  <td data-label="Assigned Roles">
                    <div className={styles.chips}>
                      {user.assignments.length === 0
                        ? "No Roles"
                        : user.assignments.map((assignment) => (
                            <Badge key={assignment.roleId} appearance="tint">
                              {state.roles.find(
                                (role) => role.id === assignment.roleId,
                              )?.name || assignment.roleId}{" "}
                              ({assignment.roleId})
                              {assignment.envOwned && " · protected"}
                            </Badge>
                          ))}
                    </div>
                  </td>
                </tr>
              ))}
            {section === "roles" &&
              state.roles.map((role) => (
                <tr key={role.id}>
                  <td data-label="Role">
                    <Button
                      disabled={disabled}
                      appearance="transparent"
                      onClick={() => editor.editRole(role)}
                    >
                      {role.name} ({role.id})
                    </Button>
                  </td>
                  <td data-label="Permissions">{role.rules.length} rules</td>
                  <td data-label="Ownership">
                    {role.envOwned
                      ? "Environment-owned · policy locked"
                      : "Managed"}
                  </td>
                </tr>
              ))}
            {section === "issuers" &&
              state.issuers.map((issuer) => (
                <tr key={issuer.issuer}>
                  <td data-label="Issuer">
                    <Button
                      disabled={disabled}
                      appearance="transparent"
                      onClick={() => editor.editIssuer(issuer)}
                    >
                      {issuer.issuer}
                    </Button>
                  </td>
                  <td data-label="Profiles / state">
                    {[
                      issuer.clientId && "Browser",
                      issuer.apiAudience && "API",
                      issuer.nativeClients.length > 0 && "Native",
                    ]
                      .filter(Boolean)
                      .join(" · ")}
                    <br />
                    {issuer.deleted
                      ? "Deleted"
                      : issuer.enabled
                        ? "Enabled"
                        : "Disabled"}
                  </td>
                  <td data-label="Ownership">
                    {issuer.envOwned
                      ? "Environment-owned · fields locked"
                      : "Managed"}
                    {issuer.hasSecretBinding && " · secret bound"}
                  </td>
                </tr>
              ))}
          </tbody>
        </table>
      )}
    </section>
  );
}
