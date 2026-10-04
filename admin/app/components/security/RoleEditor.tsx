import { Button, Field, Input, Title3 } from "@fluentui/react-components";
import { RuleEditor } from "./RuleEditor";
import type { SecurityEditor } from "~/lib/client/usecase/security/use-security-editor";
import styles from "./RoleEditor.module.css";

export function RoleEditor({
  editor,
  disabled,
}: {
  editor: SecurityEditor;
  disabled: boolean;
}) {
  const draft = editor.role;
  return (
    <form
      className={styles.form}
      onSubmit={(event) => {
        event.preventDefault();
        editor.saveRole();
      }}
    >
      <Title3>{draft.existing ? "Edit Role" : "Create Role"}</Title3>
      <p>
        Matching Deny rules take precedence across all assigned Roles. Role
        changes affect every assigned user.
      </p>
      {draft.locked && (
        <p>Environment-owned Role policy. Contact the operator to change it.</p>
      )}
      <fieldset
        disabled={disabled || draft.locked || draft.id === "cluster_replica"}
        className={styles.fields}
      >
        <Field label="Role ID" required>
          <Input
            readOnly={draft.existing}
            value={draft.id}
            onChange={(_, data) => editor.patchRole({ id: data.value })}
          />
        </Field>
        <Field label="Role name" required>
          <Input
            value={draft.name}
            onChange={(_, data) => editor.patchRole({ name: data.value })}
          />
        </Field>
        {draft.rules.map((rule, index) => (
          <RuleEditor
            key={index}
            rule={rule}
            index={index}
            change={editor.patchRule}
            remove={editor.removeRule}
          />
        ))}
        <div className={styles.actions}>
          <Button onClick={editor.addRule}>Add rule</Button>
          <Button type="submit" appearance="primary">
            Review Role change
          </Button>
          {draft.existing && (
            <Button onClick={editor.deleteRole}>Review Role deletion</Button>
          )}
        </div>
      </fieldset>
      <p>
        Reading does not enable CDC or export. Security administration grants no
        implicit graph access. In-use Roles cannot be deleted; remove or replace
        memberships in Users first. The Server protects the last usable security
        administrator.
      </p>
    </form>
  );
}
