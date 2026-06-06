import { Field, Input } from "@fluentui/react-components";

export interface Int64EditorProps {
  value: string;
  onChange: (value: string) => void;
}

/**
 * 64-bit integers are transmitted as JSON strings because JS `number`
 * loses precision past 2^53. The input is a plain text box for the same
 * reason — `type="number"` would silently round.
 */
export function Int64Editor(props: Int64EditorProps) {
  return (
    <Field
      label="int64 value"
      hint="Signed 64-bit integer. Transmitted as a string to preserve precision."
    >
      <Input
        value={props.value}
        onChange={(_, data) => props.onChange(data.value)}
        data-testid="vertex-editor-int64"
        inputMode="numeric"
      />
    </Field>
  );
}
