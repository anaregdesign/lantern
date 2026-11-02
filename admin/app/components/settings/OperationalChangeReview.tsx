import { Button, Title3 } from "@fluentui/react-components";
import {
  SecurityAuthorizationRequirement,
  validateOperationalValues,
} from "lantern-sdk/web";
import type { OperationalSettingsState } from "~/lib/client/usecase/settings/operational-settings-management";
import {
  operationalDefinition,
  operationalDraft,
} from "~/lib/client/usecase/settings/operational-settings-fields";
import styles from "./OperationalChangeReview.module.css";
export function OperationalChangeReview({
  review,
  busy,
  apply,
  cancel,
  authorize,
  checkAuthorization,
}: {
  review: NonNullable<OperationalSettingsState["review"]>;
  busy: boolean;
  apply(): void;
  cancel(): void;
  authorize(): void;
  checkAuthorization(): void;
}) {
  const operation = review.changes[0]?.operation;
  if (
    review.changes.length !== 1 ||
    operation?.case !== "putOperationalPolicy" ||
    !operation.value.values
  )
    return null;
  const change = operation.value,
    values = change.values!,
    definition = operationalDefinition(validateOperationalValues(values)),
    draft = operationalDraft(values);
  const requiresAuthorization =
    review.approval === "required" ||
    (review.approval === "failed" &&
      review.requirement === SecurityAuthorizationRequirement.REAUTHENTICATION);
  return (
    <section
      className={styles.review}
      aria-label="Review operational policy change"
    >
      <Title3 as="h3">Review {definition.label.toLowerCase()} change</Title3>
      <p>{review.label}</p>
      <p>
        Expected generation {change.expectedGeneration.toString()}. All fields
        below are one atomic reviewed change. This saves shared desired policy;
        each node reports its application separately.
      </p>
      {change.expectedGeneration === 0n && (
        <p>
          Applying this review may prevent older server versions from
          restarting, even if the policy does not commit. After adoption,
          environment changes will not override this group.
        </p>
      )}
      <dl className={styles.values}>
        {definition.fields.map((field) => (
          <div key={field.key}>
            <dt>{field.label}</dt>
            <dd>{draft[field.key]}</dd>
          </div>
        ))}
      </dl>
      {change.compatibilityFingerprint && (
        <p>
          Search compatibility fingerprint: {change.compatibilityFingerprint}.
          Follow the reported coordinated restart procedure.
        </p>
      )}
      {change.retentionReductionAcknowledged && (
        <p>
          Future retention deletion has been acknowledged. Review and Apply do
          not delete backups.
        </p>
      )}
      {review.approval === "preparing" && (
        <p>Checking the exact reviewed change with Server…</p>
      )}
      {requiresAuthorization && (
        <p>This exact reviewed change requires reauthentication.</p>
      )}
      {review.approval === "approved" && (
        <p>This exact reviewed change has been approved.</p>
      )}
      <div className={styles.actions}>
        <Button
          appearance="primary"
          disabled={
            busy ||
            (review.approval !== "ordinary" && review.approval !== "approved")
          }
          onClick={apply}
        >
          Apply reviewed operational change
        </Button>
        {requiresAuthorization && (
          <Button disabled={busy} onClick={authorize}>
            Reauthenticate reviewed operational change
          </Button>
        )}
        {review.approval === "authenticating" && (
          <Button disabled={busy} onClick={checkAuthorization}>
            Check operational reauthentication
          </Button>
        )}
        <Button disabled={busy} onClick={cancel}>
          Cancel operational review
        </Button>
      </div>
    </section>
  );
}
