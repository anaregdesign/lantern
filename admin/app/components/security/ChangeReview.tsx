import { Button, Title3 } from "@fluentui/react-components";
import { SecurityAuthorizationRequirement } from "lantern-sdk/web";
import type { SecurityManagementState } from "~/lib/client/usecase/security/security-management";
import { reviewSummary } from "~/lib/client/usecase/security/security-drafts";
import styles from "./ChangeReview.module.css";

export function ChangeReview({
  review,
  busy,
  apply,
  cancel,
  authorize,
  checkAuthorization,
}: {
  review: NonNullable<SecurityManagementState["review"]>;
  busy: boolean;
  apply(): void;
  cancel(): void;
  authorize(): void;
  checkAuthorization(): void;
}) {
  return (
    <section className={styles.review} aria-label="Review security change">
      <Title3>Review security change</Title3>
      <p>{review.label}</p>
      <p>Policy cut {review.version.currentCut?.sequence.toString()}</p>
      <details>
        <summary>Change details</summary>
        <pre className={styles.details}>{reviewSummary(review.changes)}</pre>
      </details>
      {review.approval === "preparing" && (
        <p>Checking the reviewed change with Server…</p>
      )}
      {(review.approval === "required" ||
        (review.approval === "failed" &&
          review.requirement ===
            SecurityAuthorizationRequirement.REAUTHENTICATION)) && (
        <p>
          This change requires reauthentication for this exact reviewed
          operation.
        </p>
      )}
      {review.approval === "approved" && (
        <p>This exact reviewed change has been approved.</p>
      )}
      <div className={styles.actions}>
        <Button
          disabled={
            busy ||
            (review.approval !== "ordinary" && review.approval !== "approved")
          }
          appearance="primary"
          onClick={apply}
        >
          Apply reviewed change
        </Button>
        {(review.approval === "required" ||
          (review.approval === "failed" &&
            review.requirement ===
              SecurityAuthorizationRequirement.REAUTHENTICATION)) && (
          <Button disabled={busy} onClick={authorize}>
            Reauthenticate reviewed change
          </Button>
        )}
        {review.approval === "authenticating" && (
          <Button disabled={busy} onClick={checkAuthorization}>
            Check reauthentication
          </Button>
        )}
        <Button disabled={busy} onClick={cancel}>
          Cancel review
        </Button>
      </div>
    </section>
  );
}
