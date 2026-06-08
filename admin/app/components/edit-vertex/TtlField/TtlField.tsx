import { Field, Input, RadioGroup, Radio } from "@fluentui/react-components";
import type {
  TtlInput,
  TtlMode,
} from "~/lib/client/usecase/edit-vertex/value-codec";
import styles from "./TtlField.module.css";

export interface TtlFieldProps {
  value: TtlInput;
  onChange: (value: TtlInput) => void;
  /** Optional custom label so the field can read e.g. "Expires in" vs "TTL". */
  label?: string;
}

const PRESETS: Array<{ mode: TtlMode; label: string }> = [
  { mode: "preset5m", label: "5 min" },
  { mode: "preset1h", label: "1 hour" },
  { mode: "preset24h", label: "24 hours" },
  { mode: "none", label: "no expiration" },
  { mode: "custom", label: "custom…" },
];

/**
 * Combined TTL selector: radio chips for the four presets plus a custom
 * Go-duration input that only shows up when "custom…" is active.
 */
export function TtlField(props: TtlFieldProps) {
  const { value, onChange } = props;
  return (
    <Field label={props.label ?? "Expires in"}>
      <div data-testid="vertex-ttl-presets">
        <RadioGroup
          layout="horizontal-stacked"
          value={value.mode}
          onChange={(_, data) =>
            onChange({ ...value, mode: data.value as TtlMode })
          }
        >
          {PRESETS.map((p) => (
            <Radio key={p.mode} value={p.mode} label={p.label} />
          ))}
        </RadioGroup>
        {value.mode === "custom" ? (
          <Input
            value={value.custom}
            onChange={(_, data) => onChange({ ...value, custom: data.value })}
            placeholder="e.g. 15m, 2h30m, 7d (use Go syntax)"
            data-testid="vertex-ttl-custom"
            className={styles.customInput}
          />
        ) : null}
      </div>
    </Field>
  );
}
