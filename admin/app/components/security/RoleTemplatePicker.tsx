import {
  Button,
  Field,
  Input,
  Select,
  Spinner,
} from "@fluentui/react-components";
import { FieldHelp } from "~/components/shared/FieldHelp";
import {
  PERMISSIONS,
  RESOURCES,
} from "~/lib/client/usecase/security/security-drafts";
import type { SecurityEditor } from "~/lib/client/usecase/security/use-security-editor";
import type { SecurityManagementState } from "~/lib/client/usecase/security/security-management";
import styles from "./RoleTemplatePicker.module.css";

export function RoleTemplatePicker({
  editor,
  state,
  disabled,
  load,
}: {
  editor: SecurityEditor;
  state: SecurityManagementState;
  disabled: boolean;
  load(): void;
}) {
  return (
    <section className={styles.picker} aria-label="Choose Role starting point">
      <Field
        label="Role starting point"
        hint="Templates only populate an unsaved draft; selection does not save or assign permissions."
      >
        <Select
          value={editor.roleSource}
          disabled={disabled}
          onChange={(_, value) =>
            editor.setRoleSource(value.value as "blank" | "template")
          }
        >
          <option value="blank">Blank Role</option>
          <option value="template">From template</option>
        </Select>
      </Field>
      {editor.roleSource === "template" && (
        <>
          <Field
            label="Template logical prefix"
            hint={
              <FieldHelp
                meaning="Choose the literal data namespace before loading Server templates. Empty means all logical data."
                example="projects:demo:"
                details="No wildcard expansion. Personal templates use each assigned user's trusted UID. Management templates target their named resource independently from data."
              />
            }
          >
            <Input
              value={editor.templatePrefix}
              disabled={disabled}
              onChange={(_, value) => editor.setTemplatePrefix(value.value)}
            />
          </Field>
          <Button
            disabled={disabled || state.templatePhase === "loading"}
            onClick={load}
          >
            Load templates
          </Button>
          {state.templatePhase === "loading" && (
            <Spinner label="Loading Server templates" />
          )}
          {state.templateMessage && <p role="alert">{state.templateMessage}</p>}
          {state.templatePhase === "ready" && state.templates.length === 0 && (
            <p>No templates were returned for this scope.</p>
          )}
          <ul className={styles.templates}>
            {state.templates.map((role) => (
              <li key={role.id}>
                <strong>{role.name}</strong>
                <ul>
                  {role.rules.map((rule) => (
                    <li key={rule.id}>
                      Allow{" "}
                      {
                        PERMISSIONS.find(
                          ([permission]) => permission === rule.permission,
                        )?.[1]
                      }{" "}
                      ·{" "}
                      {
                        RESOURCES.find(
                          ([resource]) => resource === rule.resourceKind,
                        )?.[1]
                      }{" "}
                      ·{" "}
                      {rule.resource.case === "prefix"
                        ? rule.resource.value || "all logical data"
                        : rule.resource.case === "principalUidPrefix"
                          ? "users:<trusted UID>:"
                          : "named management resource"}
                    </li>
                  ))}
                </ul>
                <Button
                  disabled={disabled}
                  onClick={() => editor.useTemplate(role)}
                >
                  Use {role.name}
                </Button>
              </li>
            ))}
          </ul>
        </>
      )}
    </section>
  );
}
