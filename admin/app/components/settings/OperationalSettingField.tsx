import { Field, Input, Select } from "@fluentui/react-components";
import { FieldHelp } from "~/components/shared/FieldHelp";
import {
  operationalFieldError,
  type OperationalField,
} from "~/lib/client/usecase/settings/operational-settings-fields";
import styles from "./OperationalSettingField.module.css";
export function OperationalSettingField({
  field,
  value,
  disabled,
  editing,
  validationError,
  change,
}: {
  field: OperationalField;
  value: string;
  disabled: boolean;
  editing: boolean;
  validationError?: string;
  change(value: string): void;
}) {
  const error = editing
    ? (validationError ?? operationalFieldError(field, value))
    : undefined;
  return (
    <Field
      label={{ children: field.label, className: styles.label }}
      hint={<FieldHelp {...field.help} />}
      validationState={error ? "error" : "none"}
      validationMessage={error}
      className={styles.field}
    >
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
          placeholder={field.help.example}
          inputMode={field.kind === "decimal" ? "decimal" : "numeric"}
          onChange={(_, data) => change(data.value)}
        />
      )}
    </Field>
  );
}
