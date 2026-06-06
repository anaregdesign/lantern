import { useState } from "react";
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
import { DEFAULT_BASE_URL } from "~/lib/client/usecase/connection/base-url";
import styles from "./ConnectionSwitcher.module.css";

export function ConnectionSwitcher() {
  const { connection, setBaseUrl, reset } = useConnection();
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(connection.baseUrl);
  const [error, setError] = useState<string | null>(null);

  const onOpenChange = (next: boolean) => {
    setOpen(next);
    if (next) {
      setDraft(connection.baseUrl);
      setError(null);
    }
  };

  const onSave = () => {
    const ok = setBaseUrl(draft);
    if (!ok) {
      setError("Enter an http(s) URL like http://localhost:6380");
      return;
    }
    setOpen(false);
  };

  return (
    <Dialog open={open} onOpenChange={(_, data) => onOpenChange(data.open)}>
      <div className={styles.root}>
        <span className={styles.label}>Gateway</span>
        <DialogTrigger disableButtonEnhancement>
          <Button
            appearance="subtle"
            icon={<PlugConnected24Regular />}
            aria-label="Change gateway connection"
          >
            <span className={styles.value}>{connection.baseUrl}</span>
          </Button>
        </DialogTrigger>
      </div>
      <DialogSurface>
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
            </div>
          </DialogContent>
          <DialogActions>
            <Button
              appearance="secondary"
              onClick={() => {
                reset();
                setDraft(DEFAULT_BASE_URL);
                setError(null);
              }}
            >
              Reset
            </Button>
            <DialogTrigger disableButtonEnhancement>
              <Button appearance="secondary">Cancel</Button>
            </DialogTrigger>
            <Button appearance="primary" onClick={onSave}>
              Save
            </Button>
          </DialogActions>
        </DialogBody>
      </DialogSurface>
    </Dialog>
  );
}
