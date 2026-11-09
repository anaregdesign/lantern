import { Button } from "@fluentui/react-components";
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
import { SaveReviewDialog } from "~/components/shared/SaveReviewDialog";
export function OperationalChangeReview({
  review,
  busy,
  blocked,
  message,
  apply,
  cancel,
  authorize,
  checkAuthorization,
}: {
  review: NonNullable<OperationalSettingsState["review"]>;
  busy: boolean;
  blocked: boolean;
  message: string;
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
    <SaveReviewDialog
      busy={busy}
      canSave={
        !blocked &&
        (review.approval === "ordinary" || review.approval === "approved")
      }
      save={apply}
      back={cancel}
      message={message}
      messageIntent={review.approval === "failed" ? "error" : "info"}
      actions={
        <>
          {requiresAuthorization && (
            <Button disabled={busy} onClick={authorize}>
              Reauthenticate reviewed change
            </Button>
          )}
          {review.approval === "authenticating" && (
            <Button disabled={busy} onClick={checkAuthorization}>
              Check reauthentication
            </Button>
          )}
        </>
      }
    >
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
          Future retention deletion has been acknowledged. Review and Save do
          not delete backups.
        </p>
      )}
      {review.approval === "preparing" && !message && (
        <p>Checking the exact reviewed change with Server…</p>
      )}
      {requiresAuthorization && (
        <p>This exact reviewed change requires reauthentication.</p>
      )}
      {review.approval === "approved" && (
        <p>This exact reviewed change has been approved.</p>
      )}
    </SaveReviewDialog>
  );
}
