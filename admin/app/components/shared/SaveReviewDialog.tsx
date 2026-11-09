import { useEffect, useRef, useState, type ReactNode } from "react";
import {
  Button,
  Dialog,
  DialogActions,
  DialogBody,
  DialogContent,
  DialogSurface,
  DialogTitle,
  MessageBar,
  MessageBarBody,
} from "@fluentui/react-components";
import styles from "./SaveReviewDialog.module.css";

export type SaveReviewDialogProps = {
  children: ReactNode;
  busy: boolean;
  canSave: boolean;
  save(): void;
  back(): void;
  actions?: ReactNode;
  message?: string;
  messageIntent?: "info" | "error" | "warning" | "success";
};

export function SaveReviewDialog({
  children,
  busy,
  canSave,
  save,
  back,
  actions,
  message,
  messageIntent = "info",
}: SaveReviewDialogProps) {
  const [returnFocus] = useState(() =>
    typeof document !== "undefined" &&
    document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null,
  );
  const returningToDraft = useRef(false);
  const backToEditing = () => {
    returningToDraft.current = true;
    back();
  };
  useEffect(
    () => () => {
      if (!returningToDraft.current || !returnFocus) return;
      queueMicrotask(() => {
        if (returnFocus.isConnected) returnFocus.focus();
      });
    },
    [returnFocus],
  );
  return (
    <Dialog
      open
      onOpenChange={(_, data) => {
        if (!data.open && !busy) backToEditing();
      }}
    >
      <DialogSurface className={styles.surface}>
        <DialogBody className={styles.body}>
          <DialogTitle>Review changes</DialogTitle>
          <DialogContent className={styles.content}>
            <p>
              Review the changes below, then select Save changes to save them.
            </p>
            {message && (
              <MessageBar intent={messageIntent} layout="multiline">
                <MessageBarBody>{message}</MessageBarBody>
              </MessageBar>
            )}
            {children}
          </DialogContent>
          <DialogActions className={styles.actions}>
            <Button disabled={busy} onClick={backToEditing}>
              Back to editing
            </Button>
            {actions}
            <Button
              appearance="primary"
              disabled={busy || !canSave}
              onClick={save}
            >
              {busy ? "Saving changes…" : "Save changes"}
            </Button>
          </DialogActions>
        </DialogBody>
      </DialogSurface>
    </Dialog>
  );
}
