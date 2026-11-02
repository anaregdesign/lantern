import { Button, Title3 } from "@fluentui/react-components";
import { SecurityAuthorizationRequirement } from "lantern-sdk/web";
import type { SettingsState } from "~/lib/client/usecase/settings/settings-management";
import styles from "./SettingsChangeReview.module.css";

export type SettingsChangeReviewProps = {
  review: NonNullable<SettingsState["review"]>;
  firstAdoption: boolean;
  busy: boolean;
  apply(): void;
  cancel(): void;
  authorize(): void;
  checkAuthorization(): void;
};

export function SettingsChangeReview({
  review,
  firstAdoption,
  busy,
  apply,
  cancel,
  authorize,
  checkAuthorization,
}: SettingsChangeReviewProps) {
  const requiresAuthorization =
    review.approval === "required" ||
    (review.approval === "failed" &&
      review.requirement === SecurityAuthorizationRequirement.REAUTHENTICATION);
  return (
    <section className={styles.review} aria-label="Review log level change">
      <Title3 as="h2">Review log level change</Title3>
      <p>{review.label}</p>
      <p>
        This saves the shared desired policy. Each node reports its application
        separately.
      </p>
      {firstAdoption && (
        <p>
          Applying this review can prevent an older server version from
          restarting, even if the policy is not committed. Environment changes
          will not override an adopted policy.
        </p>
      )}
      {review.approval === "preparing" && (
        <p>Checking the reviewed change with Server…</p>
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
          Apply reviewed change
        </Button>
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
        <Button disabled={busy} onClick={cancel}>
          Cancel review
        </Button>
      </div>
    </section>
  );
}
