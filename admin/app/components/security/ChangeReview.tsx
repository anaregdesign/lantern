import { Button, Title3 } from "@fluentui/react-components";
import type { SecurityManagementState } from "~/lib/client/usecase/security/security-management";
import { reviewSummary } from "~/lib/client/usecase/security/security-drafts";
import styles from "./ChangeReview.module.css";

export function ChangeReview({
  review,
  recent,
  busy,
  apply,
  cancel,
  stepUp,
}: {
  review: NonNullable<SecurityManagementState["review"]>;
  recent: boolean;
  busy: boolean;
  apply(): void;
  cancel(): void;
  stepUp(): void;
}) {
  return (
    <section className={styles.review} aria-label="Review security change">
      <Title3>Review security change</Title3>
      <p>{review.label}</p>
      <p>Revision {review.expectedRevision.toString()}</p>
      <details>
        <summary>Change details</summary>
        <pre className={styles.details}>{reviewSummary(review.changes)}</pre>
      </details>
      {!recent && <p>Sign in again before applying this change.</p>}
      <div className={styles.actions}>
        <Button disabled={!recent || busy} appearance="primary" onClick={apply}>
          Apply reviewed change
        </Button>
        {!recent && <Button onClick={stepUp}>Sign in again</Button>}
        <Button disabled={busy} onClick={cancel}>
          Cancel review
        </Button>
      </div>
    </section>
  );
}
