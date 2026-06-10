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
import { DataTrending24Regular } from "@fluentui/react-icons";

import { DEFAULT_PROMETHEUS_URL } from "~/lib/client/usecase/ops/metrics/prometheus-url";
import type { UsePrometheusUrl } from "~/lib/client/usecase/ops/metrics/use-prometheus-url";
import styles from "./PrometheusSwitcher.module.css";

export interface PrometheusSwitcherProps {
  prometheus: UsePrometheusUrl;
}

/**
 * PrometheusSwitcher mirrors ConnectionSwitcher but targets the Prometheus
 * query base URL used by the Ops Metrics section. It is mounted inside the
 * Metrics toolbar (ops-local) rather than the global header because the
 * Prometheus endpoint is only meaningful on this page.
 */
export function PrometheusSwitcher({ prometheus }: PrometheusSwitcherProps) {
  const { prometheusUrl, setPrometheusUrl, reset } = prometheus;
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(prometheusUrl);
  const [error, setError] = useState<string | null>(null);

  const onOpenChange = (next: boolean) => {
    setOpen(next);
    if (next) {
      setDraft(prometheusUrl);
      setError(null);
    }
  };

  const onSave = () => {
    const ok = setPrometheusUrl(draft);
    if (!ok) {
      setError("Enter a same-origin path like /api/prom or an http(s) URL");
      return;
    }
    setOpen(false);
  };

  return (
    <Dialog open={open} onOpenChange={(_, data) => onOpenChange(data.open)}>
      <div className={styles.root}>
        <span className={styles.label}>Prometheus</span>
        <DialogTrigger disableButtonEnhancement>
          <Button
            appearance="subtle"
            icon={<DataTrending24Regular />}
            aria-label="Change Prometheus endpoint"
            data-testid="ops-prometheus-switcher"
          >
            <span className={styles.value}>{prometheusUrl}</span>
          </Button>
        </DialogTrigger>
      </div>
      <DialogSurface>
        <DialogBody>
          <DialogTitle>Prometheus query endpoint</DialogTitle>
          <DialogContent>
            <div className={styles.dialogBody}>
              <Field
                label="Query base URL"
                validationState={error ? "error" : "none"}
                validationMessage={error ?? undefined}
                hint={`Default: ${DEFAULT_PROMETHEUS_URL} (reverse-proxied to Prometheus). The SPA appends /api/v1/query_range.`}
              >
                <Input
                  value={draft}
                  onChange={(_, data) => {
                    setDraft(data.value);
                    setError(null);
                  }}
                  placeholder={DEFAULT_PROMETHEUS_URL}
                  data-testid="ops-prometheus-input"
                />
              </Field>
            </div>
          </DialogContent>
          <DialogActions>
            <Button
              appearance="secondary"
              onClick={() => {
                reset();
                setDraft(DEFAULT_PROMETHEUS_URL);
                setError(null);
              }}
            >
              Reset
            </Button>
            <DialogTrigger disableButtonEnhancement>
              <Button appearance="secondary">Cancel</Button>
            </DialogTrigger>
            <Button
              appearance="primary"
              onClick={onSave}
              data-testid="ops-prometheus-save"
            >
              Save
            </Button>
          </DialogActions>
        </DialogBody>
      </DialogSurface>
    </Dialog>
  );
}
