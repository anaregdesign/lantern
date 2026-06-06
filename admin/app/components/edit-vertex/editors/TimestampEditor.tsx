import { Field, Input } from "@fluentui/react-components";

export interface TimestampEditorProps {
  value: string;
  onChange: (value: string) => void;
}

/**
 * Datetime-local accepts `YYYY-MM-DDTHH:mm`. The codec converts to ISO
 * UTC at save time, treating the input as local-zone.
 */
export function TimestampEditor(props: TimestampEditorProps) {
  return (
    <Field
      label="timestamp value"
      hint="Interpreted in your local timezone, transmitted as RFC3339 UTC."
    >
      <Input
        type="datetime-local"
        value={props.value}
        onChange={(_, data) => props.onChange(data.value)}
        data-testid="vertex-editor-timestamp"
      />
    </Field>
  );
}
