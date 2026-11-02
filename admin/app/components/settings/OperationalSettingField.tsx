import { Field, Input, Select } from "@fluentui/react-components";
import type { OperationalField } from "~/lib/client/usecase/settings/operational-settings-fields";
import styles from "./OperationalSettingField.module.css";
export function OperationalSettingField({
  field,
  value,
  disabled,
  change,
}: {
  field: OperationalField;
  value: string;
  disabled: boolean;
  change(value: string): void;
}) {
  return (
    <Field label={field.label} className={styles.field}>
      {field.kind === "boolean" ? (
        <Select
          value={value}
          disabled={disabled}
          onChange={(_, data) => change(data.value)}
        >
          <option value="" disabled>
            Choose enabled or disabled
          </option>
          <option value="true">Enabled</option>
          <option value="false">Disabled</option>
        </Select>
      ) : field.kind === "search-mode" ? (
        <Select
          value={value}
          disabled={disabled}
          onChange={(_, data) => change(data.value)}
        >
          <option value="" disabled>
            Choose matching mode
          </option>
          <option value="1">Any term</option>
          <option value="2">All terms</option>
          <option value="3">Minimum matching terms</option>
        </Select>
      ) : (
        <Input
          value={value}
          disabled={disabled}
          inputMode={field.kind === "decimal" ? "decimal" : "numeric"}
          onChange={(_, data) => change(data.value)}
        />
      )}
    </Field>
  );
}
