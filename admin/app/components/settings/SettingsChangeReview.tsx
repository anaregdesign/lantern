import { Button } from "@fluentui/react-components";
import { SecurityAuthorizationRequirement } from "lantern-sdk/web";
import type { SettingsState } from "~/lib/client/usecase/settings/settings-management";
import { SaveReviewDialog } from "~/components/shared/SaveReviewDialog";

export type SettingsChangeReviewProps = {
  review: NonNullable<SettingsState["review"]>;
  firstAdoption: boolean;
  busy: boolean;
  blocked: boolean;
  message: string;
  apply(): void;
  cancel(): void;
  authorize(): void;
  checkAuthorization(): void;
};

export function SettingsChangeReview({
  review,
  firstAdoption,
  busy,
  blocked,
  message,
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
      {review.approval === "preparing" && !message && (
        <p>Checking the reviewed change with Server…</p>
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
