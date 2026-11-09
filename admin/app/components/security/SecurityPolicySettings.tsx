import {
  Button,
  Checkbox,
  Field,
  Select,
  Spinner,
  Title3,
} from "@fluentui/react-components";
import { EdgeEndpointMode, RegistrationProfileKind } from "lantern-sdk/web";
import { FieldHelp } from "~/components/shared/FieldHelp";
import {
  PERMISSIONS,
  RESOURCES,
} from "~/lib/client/usecase/security/security-drafts";
import {
  EDGE_MODES,
  enrollmentProfileKey,
} from "~/lib/client/usecase/security/security-policy-drafts";
import type { useSecurityPolicies } from "~/lib/client/usecase/security/use-security-policies";
import type { SecurityManagementState } from "~/lib/client/usecase/security/security-management";
import styles from "./SecurityPolicySettings.module.css";

export function SecurityPolicySettings({
  state,
  policies,
  disabled,
  busy,
}: {
  state: SecurityManagementState;
  policies: ReturnType<typeof useSecurityPolicies>;
  disabled: boolean;
  busy: boolean;
}) {
  const current = state.registrationPolicy;
  const draft = policies.enrollment;
  const known = policies.choices;
  const retained = (draft?.profiles ?? []).filter(
    (profile) =>
      !known.some(
        (choice) =>
          enrollmentProfileKey(choice) === enrollmentProfileKey(profile),
      ),
  );
  return (
    <section className={styles.settings} aria-label="Shared Security settings">
      <Title3>Shared Security settings</Title3>
      <p>
        New-user registration and Edge editing apply cluster-wide, independently
        of the selected User or Role.
      </p>
      <Button
        disabled={
          busy ||
          state.mutation === "unconfirmed" ||
          state.mutation === "pending"
        }
        onClick={() => policies.load()}
      >
        Load shared Security settings
      </Button>
      {state.policyPhase === "loading" && (
        <Spinner label="Loading shared Security settings" />
      )}
      {state.policyMessage && <p role="alert">{state.policyMessage}</p>}
      {policies.error && <p role="alert">{policies.error}</p>}
      {state.policyPhase === "ready" && (
        <>
          <p>
            Edit settings, select Review changes, then Save changes in the
            confirmation dialog. Changes are not saved automatically.
          </p>
          <fieldset disabled={disabled} className={styles.fields}>
            <legend>Edge editing</legend>
            <p>
              Current:{" "}
              {EDGE_MODES.find(
                ([mode]) => mode === state.edgePolicy?.mode,
              )?.[1] ?? "Unsupported mode"}{" "}
              · fresh-install default: readable → readable
            </p>
            <Field
              label="Shared Edge editing rule"
              hint={
                <FieldHelp
                  meaning="One permission pair for every user: tail Vertex → head Vertex. Read and Write are independent."
                  example="readable → writable requires Read on the tail and Write on the head"
                  details="readable → readable allows users with Read on both Vertices to edit their Edge. Writable-based modes may permit blind edits. Edge reads still require Read on both endpoints; lifecycle, absence, TTL and receipt safeguards remain enforced by Server."
                />
              }
            >
              <Select
                value={String(policies.edgeMode)}
                onChange={(_, value) =>
                  policies.setEdgeMode(Number(value.value) as EdgeEndpointMode)
                }
              >
                {EDGE_MODES.map(([value, label]) => (
                  <option key={value} value={value}>
                    {label}
                  </option>
                ))}
              </Select>
            </Field>
            {policies.edgeDirty && (
              <p role="status">
                Unsaved Edge policy draft · affects every user&apos;s derived
                Edge-edit access.
              </p>
            )}
          </fieldset>
          <fieldset disabled={disabled} className={styles.fields}>
            <legend>New-user registration</legend>
            <p>
              Current: {current?.enabled ? "ON" : "OFF"}. Turning OFF blocks new
              enrollment and preserves existing-user login.
            </p>
            <Field
              hint={
                <FieldHelp
                  meaning="Registration is OFF until explicitly enabled for selected browser/native profiles. An enabled Issuer alone does not open signup."
                  details="Browser enrollment uses verified Code/PKCE/state/nonce identity. New users receive personal_user only, never data_admin or security_admin. Enabling or expanding profiles requires approval for this exact reviewed operation."
                />
              }
            >
              <Checkbox
                label="Allow new-user registration"
                checked={draft?.enabled ?? false}
                onChange={(_, value) =>
                  policies.setEnabled(value.checked === true)
                }
              />
            </Field>
            <fieldset className={styles.profiles}>
              <legend>Eligible enrollment profiles</legend>
              <FieldHelp
                meaning="Select exact enabled Issuer and browser/native profile combinations. Browser uses profile ID browser; native uses the enrolled client profile ID."
                example="https://idp.example · Browser (browser)"
                details="Only one bounded Issuer page is shown. Retained selections from other pages remain explicit below; removing a selection stages a policy change. Email never links or enrolls accounts."
              />
              <div className={styles.profiles}>
                {known.length === 0 && (
                  <p>
                    No eligible profiles on this Issuer page. Configure and
                    enable a supported Issuer/profile first.
                  </p>
                )}
                {[...known, ...retained].map((profile) => (
                  <Checkbox
                    key={enrollmentProfileKey(profile)}
                    label={`${profile.issuer} · ${profile.kind === RegistrationProfileKind.BROWSER ? "Browser" : "Native"} (${profile.clientProfileId})${retained.includes(profile) ? " · retained / not on this eligible page" : ""}`}
                    checked={
                      draft?.profiles.some(
                        (item) =>
                          enrollmentProfileKey(item) ===
                          enrollmentProfileKey(profile),
                      ) ?? false
                    }
                    onChange={(_, value) =>
                      policies.toggleProfile(profile, value.checked === true)
                    }
                  />
                ))}
              </div>
            </fieldset>
            {state.policyIssuerCursor && (
              <Button onClick={() => policies.load(state.policyIssuerCursor)}>
                Next enrollment Issuer page
              </Button>
            )}
            {!state.joiningRole && (
              <p role="alert">
                Joining Role personal_user is missing. Ask an administrator to
                create its scoped Role before enabling registration.
              </p>
            )}
            <details>
              <summary>View joining Role and saved permissions</summary>
              <p>
                Joining Role: <strong>personal_user</strong>. Fresh-install
                default: Logical data Read + Write on users:&lt;trusted
                UID&gt;:, including Delete and TTL changes. Its saved
                permissions may have been narrowed or removed.
              </p>
              {!state.joiningRole ? (
                <p role="alert">
                  Joining Role is missing. Create the scoped personal_user Role
                  through a reviewed management change before enabling
                  registration. Normal startup and registration never recreate
                  it.
                </p>
              ) : (
                <section aria-label="Saved joining Role permissions">
                  <p>
                    <strong>Current saved permissions</strong>
                  </p>
                  <FieldHelp
                    meaning="These are the saved personal_user rules returned by Server, including their exact scope. Registration does not restore or expand them."
                    details="Read and Write are independent. A Read-only Role adds no Write grant; an empty Role adds no permissions. Narrower trusted-UID prefixes limit the namespace covered by each listed rule. Server validates the joining Role and decides actual access."
                  />
                  {state.joiningRole.rules.length === 0 ? (
                    <p>No saved permissions; this Role adds no access.</p>
                  ) : (
                    <ul>
                      {state.joiningRole.rules.map((rule) => (
                        <li key={rule.id}>
                          {rule.id}: Allow{" "}
                          {PERMISSIONS.find(
                            ([permission]) => permission === rule.permission,
                          )?.[1] ??
                            `Unsupported permission (${rule.permission})`}{" "}
                          on{" "}
                          {RESOURCES.find(
                            ([resource]) => resource === rule.resourceKind,
                          )?.[1] ??
                            `Unsupported resource (${rule.resourceKind})`}
                          {rule.resource.case === "prefix"
                            ? ` · ${rule.resource.value || "all logical data keys (empty prefix)"}`
                            : rule.resource.case === "principalUidPrefix"
                              ? ` · ${rule.resource.value.literalBefore}<trusted UID>${rule.resource.value.literalAfter}`
                              : ""}
                        </li>
                      ))}
                    </ul>
                  )}
                </section>
              )}
            </details>
            {policies.enrollmentDirty && (
              <p role="status">
                Unsaved registration draft. Select Review changes, then Save
                changes to save the registration policy.
              </p>
            )}
            {draft?.enabled && !draft.profiles.length && (
              <p>
                Select at least one eligible profile before saving registration.
              </p>
            )}
          </fieldset>
          <div className={styles.saveActions}>
            <span role="status">
              {policies.edgeDirty || policies.enrollmentDirty
                ? "Unsaved changes · Review all shared settings together"
                : "No unsaved changes"}
            </span>
            <div className={styles.buttons}>
              <Button
                disabled={
                  disabled || (!policies.edgeDirty && !policies.enrollmentDirty)
                }
                onClick={policies.cancel}
              >
                Discard changes
              </Button>
              <Button
                appearance="primary"
                disabled={
                  disabled ||
                  (!policies.edgeDirty && !policies.enrollmentDirty) ||
                  (policies.enrollmentDirty &&
                    draft?.enabled &&
                    (!draft.profiles.length || !state.joiningRole))
                }
                onClick={policies.reviewChanges}
              >
                Review changes
              </Button>
            </div>
          </div>
        </>
      )}
    </section>
  );
}
