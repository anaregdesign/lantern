import { Badge, Button, Title3 } from "@fluentui/react-components";
import { SecurityPrincipalState } from "lantern-sdk/web";
import {
  PERMISSIONS,
  RESOURCES,
} from "~/lib/client/usecase/security/security-drafts";
import type { SecurityEditor } from "~/lib/client/usecase/security/use-security-editor";
import styles from "./SecurityRecordDetails.module.css";

export function SecurityRecordDetails({
  editor,
  disabled,
}: {
  editor: SecurityEditor;
  disabled: boolean;
}) {
  const role = editor.savedRole;
  const user = editor.savedUser;
  const issuer = editor.savedIssuer;
  return (
    <section className={styles.details} aria-label="Saved record details">
      {editor.selectedKind === "roles" && role && (
        <>
          <Title3>
            {role.name} ({role.id})
          </Title3>
          <p>
            Saved Role ·{" "}
            {role.envOwned
              ? "Environment-owned policy, locked for editing"
              : "Managed policy"}
          </p>
          <ul>
            {role.rules.length === 0 ? (
              <li>No permissions; assigning this Role adds no access.</li>
            ) : (
              role.rules.map((rule) => (
                <li key={rule.id}>
                  {rule.id}: Allow{" "}
                  {
                    PERMISSIONS.find(
                      ([permission]) => permission === rule.permission,
                    )?.[1]
                  }{" "}
                  on{" "}
                  {
                    RESOURCES.find(
                      ([resource]) => resource === rule.resourceKind,
                    )?.[1]
                  }
                  {rule.resource.case === "prefix"
                    ? ` · ${rule.resource.value || "all logical data keys (empty prefix)"}`
                    : rule.resource.case === "principalUidPrefix"
                      ? ` · ${rule.resource.value.literalBefore}<trusted UID>${rule.resource.value.literalAfter}`
                      : ""}
                </li>
              ))
            )}
          </ul>
          <p>
            Allows combine across all assigned Roles. Removing this Role may
            leave access granted by another Role. The shared Edge policy also
            determines Edge-edit authority.
          </p>
          <div className={styles.actions}>
            <Button
              disabled={disabled || role.envOwned}
              onClick={editor.beginEdit}
            >
              Edit permissions
            </Button>
            <Button disabled={disabled} onClick={editor.showMembers}>
              Assign members
            </Button>
            <Button disabled={disabled} onClick={editor.showExplanation}>
              Explain access
            </Button>
          </div>
        </>
      )}
      {editor.selectedKind === "users" && user && (
        <>
          <Title3>
            {user.verifiedEmail ||
              user.identity?.subject ||
              user.identity?.machineName}
          </Title3>
          <dl>
            <dt>Exact subject / machine</dt>
            <dd>{user.identity?.subject || user.identity?.machineName}</dd>
            <dt>Issuer</dt>
            <dd>{user.identity?.issuer || "Machine identity"}</dd>
            <dt>State</dt>
            <dd>{SecurityPrincipalState[user.state]}</dd>
          </dl>
          <p>
            Email is verified display metadata; identity and permissions use the
            exact Issuer and subject.
          </p>
          <div className={styles.chips}>
            {user.assignments.map((assignment) => (
              <Badge key={assignment.roleId} appearance="tint">
                {assignment.roleId}
                {assignment.envOwned && " · environment-owned"}
              </Badge>
            ))}
          </div>
          {user.assignments.length === 0 && <p>No Roles assigned.</p>}
          <div className={styles.actions}>
            <Button disabled={disabled} onClick={editor.beginEdit}>
              Edit user state
            </Button>
            <Button disabled={disabled} onClick={editor.showMembers}>
              Manage assigned Roles
            </Button>
            <Button disabled={disabled} onClick={editor.showExplanation}>
              Explain access
            </Button>
          </div>
        </>
      )}
      {editor.selectedKind === "issuers" && issuer && (
        <>
          <Title3>{issuer.issuer}</Title3>
          <p>
            Saved Issuer ·{" "}
            {issuer.deleted
              ? "Deleted"
              : issuer.enabled
                ? "Enabled"
                : "Disabled"}
            {issuer.envOwned && " · environment-owned, fields locked"}
          </p>
          <dl>
            <dt>Browser client</dt>
            <dd>{issuer.clientId || "Not configured"}</dd>
            <dt>Exact callback</dt>
            <dd>{issuer.redirectUri || "Not configured"}</dd>
            <dt>API audience</dt>
            <dd>{issuer.apiAudience || "External API tokens refused"}</dd>
            <dt>Signing algorithms</dt>
            <dd>{issuer.algorithms.join(", ")}</dd>
            <dt>Client secret binding</dt>
            <dd>
              {issuer.hasSecretBinding
                ? "Installed operator binding; secret is never shown"
                : "No binding"}
            </dd>
            <dt>Native profiles</dt>
            <dd>
              {issuer.nativeClients.map((profile) => profile.id).join(", ") ||
                "None"}
            </dd>
          </dl>
          {issuer.envOwned && (
            <p>Contact the operator to change declarative configuration.</p>
          )}
          <Button
            disabled={disabled || issuer.envOwned}
            onClick={editor.beginEdit}
          >
            Edit Issuer
          </Button>
        </>
      )}
    </section>
  );
}
