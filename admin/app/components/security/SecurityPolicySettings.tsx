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
            Inspected saved values. After Apply, reload this section for the
            current saved policy.
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
            <Button
              disabled={!policies.edgeDirty}
              onClick={policies.reviewEdge}
            >
              Review shared Edge rule
            </Button>
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
            <p>
              Joining Role: <strong>personal_user</strong> · intended scope:
              users:&lt;trusted UID&gt;: · Read + Write, including Delete and
              TTL changes.
            </p>
            {!state.joiningRole ? (
              <p role="alert">
                Joining Role is missing. Create the scoped personal_user Role
                through a reviewed management change before enabling
                registration. Normal startup and registration never recreate it.
              </p>
            ) : (
              <p>
                Server checks the saved joining Role before enrollment. It
                grants no management authority or another user&apos;s namespace.
              </p>
            )}
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
            {policies.enrollmentDirty && (
              <p role="status">
                Unsaved registration draft. Existing users and Role assignments
                are unchanged until Apply.
              </p>
            )}
            <Button
              disabled={!policies.enrollmentDirty}
              onClick={policies.reviewEnrollment}
            >
              Review registration policy
            </Button>
          </fieldset>
          {(policies.edgeDirty || policies.enrollmentDirty) && (
            <Button
              disabled={busy || state.mutation === "unconfirmed"}
              onClick={policies.cancel}
            >
              Cancel shared policy drafts
            </Button>
          )}
        </>
      )}
    </section>
  );
}
