import {
  Button,
  Checkbox,
  Field,
  Input,
  Select,
  Title3,
} from "@fluentui/react-components";
import type { SecurityEditor } from "~/lib/client/usecase/security/use-security-editor";
import { FieldHelp } from "~/components/shared/FieldHelp";
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
        <Field
          label="Issuer URL"
          required
          hint={
            <FieldHelp
              meaning="Exact HTTPS Issuer identifier from provider discovery; existing identities keep this value."
              example="https://idp.example"
            />
          }
        >
          <Input
            value={draft.issuer}
            readOnly={draft.existing}
            onChange={(_, data) => editor.patchIssuer({ issuer: data.value })}
          />
        </Field>
        <Field
          label="Browser client ID"
          hint={
            <FieldHelp
              meaning="OAuth client registered for browser Code/PKCE login. Set together with the exact callback URL."
              example="lantern-admin-client"
              details="This is not the API audience. An Issuer alone does not enable unknown-user enrollment."
            />
          }
        >
          <Input
            value={draft.clientId}
            onChange={(_, data) => editor.patchIssuer({ clientId: data.value })}
          />
        </Field>
        <Field
          label="API audience"
          hint={
            <FieldHelp
              meaning="Expected audience for the separate external API-token profile. Empty refuses external API tokens."
              example="https://lantern-api.example"
              details="A browser cookie or provider ID/access token is not automatically an API credential."
            />
          }
        >
          <Input
            value={draft.apiAudience}
            onChange={(_, data) =>
              editor.patchIssuer({ apiAudience: data.value })
            }
          />
        </Field>
        <Field
          label="Browser redirect URI"
          hint={
            <FieldHelp
              meaning="Exact HTTPS callback registered with the provider, including the Admin/Gateway origin and path."
              example="https://lantern.example/auth/callback"
              details="No wildcard, fragment or embedded credentials. Must match the deployed callback exactly."
            />
          }
        >
          <Input
            value={draft.redirectUri}
            onChange={(_, data) =>
              editor.patchIssuer({ redirectUri: data.value })
            }
          />
        </Field>
        <Field
          label="Signing algorithms"
          hint={
            <FieldHelp
              meaning="Comma-separated, distinct accepted signature algorithms: EdDSA, RS256, ES256."
              example="RS256"
              details="Google requires exactly RS256. These constrain token verification; choose only algorithms supported by the configured provider."
            />
          }
        >
          <Input
            value={draft.algorithms}
            onChange={(_, data) =>
              editor.patchIssuer({ algorithms: data.value })
            }
          />
        </Field>
        <Field
          label="Browser client secret binding"
          hint={
            <FieldHelp
              meaning="Keep, replace or remove the operator-installed secret handle. Secret values are never displayed or entered here."
              details="Removing browser login also requires clearing its retained binding. Replacement uses a preinstalled handle, not a client secret."
            />
          }
        >
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
            hint={
              <FieldHelp
                meaning="Installed operator handle: 1–64 lowercase letters, numbers, hyphens or underscores. Enter no secret value."
                example="browser-client-binding"
              />
            }
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
        <Field hint="Disabled providers reject new authentication; disabling or deleting revokes their sessions. Enrollment is controlled separately.">
          <Checkbox
            label="Enable this Issuer"
            checked={draft.enabled}
            onChange={(_, data) =>
              editor.patchIssuer({ enabled: data.checked === true })
            }
          />
        </Field>
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
            Review changes
          </Button>
          {draft.existing && (
            <>
              <Button onClick={editor.disableIssuer}>Review disable</Button>
              <Button onClick={editor.deleteIssuer}>Review delete</Button>
            </>
          )}
        </div>
      </fieldset>
      <Button disabled={disabled} onClick={editor.cancel}>
        Cancel draft
      </Button>
    </form>
  );
}
