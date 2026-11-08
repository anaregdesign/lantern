import { Button, Field, Input, Title3 } from "@fluentui/react-components";
import { RuleEditor } from "./RuleEditor";
import { FieldHelp } from "~/components/shared/FieldHelp";
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
      <Title3>{draft.existing ? "Edit permissions" : "Create Role"}</Title3>
      <p role="status">
        {draft.existing
          ? "Unsaved changes to the selected Role"
          : "Unsaved draft"}
        {editor.templateName && ` · From ${editor.templateName}`}
      </p>
      <p>
        Matching Allows from assigned Roles combine by union. No matching Allow
        means refusal. A Role grants access only after it is assigned.
      </p>
      {draft.locked && (
        <p>Environment-owned Role policy. Contact the operator to change it.</p>
      )}
      <fieldset
        disabled={disabled || draft.locked || draft.id === "cluster_replica"}
        className={styles.fields}
      >
        <Field
          label="Role ID"
          required
          validationMessage={editor.fieldError("Role ID")}
          hint={
            <FieldHelp
              meaning="Choose a unique ID: 1–64 lowercase letters, numbers, hyphens or underscores. It cannot change after saving."
              example="project-reader"
            />
          }
        >
          <Input
            readOnly={draft.existing}
            value={draft.id}
            onChange={(_, value) => editor.patchRole({ id: value.value })}
          />
        </Field>
        <Field
          label="Role name"
          required
          validationMessage={editor.fieldError("Role name")}
          hint={
            <FieldHelp
              meaning="A readable label for membership pickers and lists."
              example="Project reader"
            />
          }
        >
          <Input
            value={draft.name}
            onChange={(_, value) => editor.patchRole({ name: value.value })}
          />
        </Field>
        {draft.rules.map((rule, index) => (
          <RuleEditor
            key={index}
            rule={rule}
            index={index}
            error={editor.error}
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
        Read on both endpoints may allow Edge edits under the shared readable →
        readable policy. Writable modes may allow blind edits. Ask Server to
        explain actual access.
      </p>
      <p>
        Role changes affect every assigned user. In-use Roles cannot be deleted.
        The Server protects the last usable security administrator.
      </p>
      <Button disabled={disabled} onClick={editor.cancel}>
        Cancel draft
      </Button>
    </form>
  );
}
