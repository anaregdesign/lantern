import { Button } from "@fluentui/react-components";
import { SaveReviewDialog } from "~/components/shared/SaveReviewDialog";
import {
  RegistrationProfileKind,
  SecurityAuthorizationRequirement,
} from "lantern-sdk/web";
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
  message,
}: {
  review: NonNullable<SecurityManagementState["review"]>;
  busy: boolean;
  apply(): void;
  cancel(): void;
  authorize(): void;
  checkAuthorization(): void;
  message?: string;
}) {
  return (
    <SaveReviewDialog
      busy={busy}
      canSave={review.approval === "ordinary" || review.approval === "approved"}
      save={apply}
      back={cancel}
      message={message}
      messageIntent={review.approval === "failed" ? "error" : "info"}
      actions={
        <>
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
        </>
      }
    >
      <p>{review.label}</p>
      {review.changes.map(({ operation }, index) =>
        operation.case === "putRegistrationPolicy" ? (
          <section key={index} aria-label="Registration changes">
            <p>
              New-user registration: {operation.value.enabled ? "ON" : "OFF"}
            </p>
            <p>Joining Role: {operation.value.roleId}</p>
            <ul>
              {operation.value.profiles.map((profile) => (
                <li
                  key={`${profile.issuer}/${profile.kind}/${profile.clientProfileId}`}
                >
                  {profile.issuer} ·{" "}
                  {profile.kind === RegistrationProfileKind.BROWSER
                    ? "Browser"
                    : "Native"}{" "}
                  ({profile.clientProfileId})
                </li>
              ))}
            </ul>
          </section>
        ) : null,
      )}
      <p>Policy cut {review.version.currentCut?.sequence.toString()}</p>
      <details>
        <summary>Change details</summary>
        <pre className={styles.details}>{reviewSummary(review.changes)}</pre>
      </details>
      {review.approval === "preparing" && !message && (
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
    </SaveReviewDialog>
  );
}
