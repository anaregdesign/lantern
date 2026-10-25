import {
  Button,
  Checkbox,
  Field,
  Input,
  Select,
  Title3,
} from "@fluentui/react-components";
import type { SecurityEditor } from "~/lib/client/usecase/security/use-security-editor";
import styles from "./IssuerEditor.module.css";

export function IssuerEditor({
  editor,
  disabled,
}: {
  editor: SecurityEditor;
  disabled: boolean;
}) {
  const draft = editor.issuer;
  return (
    <form
      className={styles.form}
      onSubmit={(event) => {
        event.preventDefault();
        editor.saveIssuer();
      }}
    >
      <Title3>{draft.existing ? "Edit Issuer" : "Add Issuer"}</Title3>
      {draft.locked && (
        <p>
          Environment-owned configuration. Contact the operator to change it.
        </p>
      )}
      <fieldset disabled={disabled || draft.locked} className={styles.fields}>
        <Field label="Issuer URL" required>
          <Input
            value={draft.issuer}
            readOnly={draft.existing}
            onChange={(_, data) => editor.patchIssuer({ issuer: data.value })}
          />
        </Field>
        <Field
          label="Browser client ID"
          hint="Set with a redirect URI to enable browser sign-in."
        >
          <Input
            value={draft.clientId}
            onChange={(_, data) => editor.patchIssuer({ clientId: data.value })}
          />
        </Field>
        <Field
          label="API audience"
          hint="Leave empty to reject external API tokens."
        >
          <Input
            value={draft.apiAudience}
            onChange={(_, data) =>
              editor.patchIssuer({ apiAudience: data.value })
            }
          />
        </Field>
        <Field label="Browser redirect URI">
          <Input
            value={draft.redirectUri}
            onChange={(_, data) =>
              editor.patchIssuer({ redirectUri: data.value })
            }
          />
        </Field>
        <Field
          label="Signing algorithms"
          hint="Comma-separated: EdDSA, RS256, ES256"
        >
          <Input
            value={draft.algorithms}
            onChange={(_, data) =>
              editor.patchIssuer({ algorithms: data.value })
            }
          />
        </Field>
        <Field label="Browser client secret binding">
          <Select
            value={draft.secretMode}
            onChange={(_, data) =>
              editor.patchIssuer({
                secretMode: data.value as "preserve" | "replace" | "clear",
              })
            }
          >
            <option value="preserve">Keep the existing binding</option>
            <option value="replace">Use an installed operator handle</option>
            <option value="clear">Remove the binding</option>
          </Select>
        </Field>
        {draft.secretMode === "replace" && (
          <Field
            label="Operator secret handle"
            required
            hint="Enter an installed handle, never a client secret."
          >
            <Input
              autoComplete="off"
              value={draft.secretRef}
              onChange={(_, data) =>
                editor.patchIssuer({ secretRef: data.value })
              }
            />
          </Field>
        )}
        <Checkbox
          label="Enable this Issuer"
          checked={draft.enabled}
          onChange={(_, data) =>
            editor.patchIssuer({ enabled: data.checked === true })
          }
        />
        <Field hint="For API access only. Enable after verifying that OAuth clients cannot use or impersonate enrolled human subjects. This trust change requires operation reauthentication.">
          <Checkbox
            label="Human and OAuth client subjects are qualified as separate"
            checked={draft.humanSubjectNamespaceQualified}
            onChange={(_, data) =>
              editor.patchIssuer({
                humanSubjectNamespaceQualified: data.checked === true,
              })
            }
          />
        </Field>
        <div className={styles.actions}>
          <Button onClick={editor.validateIssuer}>Validate with Server</Button>
          <Button type="submit" appearance="primary">
            Review Issuer change
          </Button>
          {draft.existing && (
            <>
              <Button onClick={editor.disableIssuer}>Review disable</Button>
              <Button onClick={editor.deleteIssuer}>Review delete</Button>
            </>
          )}
        </div>
      </fieldset>
    </form>
  );
}
