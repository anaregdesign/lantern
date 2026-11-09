import { useRef, useState } from "react";
import {
  Button,
  Dialog,
  DialogActions,
  DialogBody,
  DialogContent,
  DialogSurface,
  DialogTitle,
  DialogTrigger,
  Field,
  Input,
} from "@fluentui/react-components";
import { PlugConnected24Regular } from "@fluentui/react-icons";
import { useConnection } from "~/lib/client/usecase/connection/connection-context";
import {
  DEFAULT_BASE_URL,
  normaliseBaseUrl,
} from "~/lib/client/usecase/connection/base-url";
import styles from "./ConnectionSwitcher.module.css";

export function ConnectionSwitcher() {
  const { connection, setBaseUrl } = useConnection();
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(connection.baseUrl);
  const [error, setError] = useState<string | null>(null);
  const [pendingSave, setPendingSave] = useState<string | null>(null);
  const trigger = useRef<HTMLButtonElement>(null);

  const onOpenChange = (next: boolean) => {
    setOpen(next);
    if (next) {
      setDraft(connection.baseUrl);
      setPendingSave(null);
      setError(null);
    }
  };

  const onSave = () => {
    const normalized = normaliseBaseUrl(draft);
    if (!normalized) {
      setError("Enter an http(s) URL like http://localhost:6380");
      return;
    }
    // Changing the Gateway replaces the authenticated app tree. Close the
    // focus trap before that replacement so the new shell stays accessible.
    setPendingSave(normalized);
    setOpen(false);
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(_, data) => onOpenChange(data.open)}
      surfaceMotion={{
        onMotionFinish: (_, { direction }) => {
          if (direction !== "exit" || !pendingSave) return;
          trigger.current?.focus();
          setPendingSave(null);
          setBaseUrl(pendingSave);
        },
      }}
    >
      <div className={styles.root}>
        <span className={styles.label}>Gateway</span>
        <DialogTrigger disableButtonEnhancement>
          <Button
            ref={trigger}
            appearance="subtle"
            icon={<PlugConnected24Regular />}
            aria-label="Change gateway connection"
          >
            <span className={styles.value}>{connection.baseUrl}</span>
          </Button>
        </DialogTrigger>
      </div>
      <DialogSurface>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            onSave();
          }}
        >
          <DialogBody>
            <DialogTitle>Lantern gateway connection</DialogTitle>
            <DialogContent>
              <div className={styles.dialogBody}>
                <Field
                  label="Base URL"
                  validationState={error ? "error" : "none"}
                  validationMessage={error ?? undefined}
                  hint={`Default: ${DEFAULT_BASE_URL}`}
                >
                  <Input
                    value={draft}
                    onChange={(_, data) => {
                      setDraft(data.value);
                      setError(null);
                    }}
                    placeholder={DEFAULT_BASE_URL}
                  />
                </Field>
                <p className={styles.draftStatus} role="status">
                  {draft !== connection.baseUrl
                    ? "Unsaved changes. Select Save changes to use this URL."
                    : "Edit the URL, then select Save changes."}
                </p>
              </div>
            </DialogContent>
            <DialogActions className={styles.actions}>
              <Button
                appearance="secondary"
                type="button"
                onClick={() => {
                  setDraft(DEFAULT_BASE_URL);
                  setError(null);
                }}
              >
                Reset to default
              </Button>
              <DialogTrigger disableButtonEnhancement>
                <Button appearance="secondary" type="button">
                  Cancel
                </Button>
              </DialogTrigger>
              <Button appearance="primary" type="submit">
                Save changes
              </Button>
            </DialogActions>
          </DialogBody>
        </form>
      </DialogSurface>
    </Dialog>
  );
}
