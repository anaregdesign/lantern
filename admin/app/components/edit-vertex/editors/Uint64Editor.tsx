import { Field, Input } from "@fluentui/react-components";

export interface Uint64EditorProps {
  value: string;
  onChange: (value: string) => void;
}

export function Uint64Editor(props: Uint64EditorProps) {
  return (
    <Field
      label="uint64 value"
      hint="Unsigned 64-bit integer. Transmitted as a string to preserve precision."
    >
      <Input
        value={props.value}
        onChange={(_, data) => props.onChange(data.value)}
        data-testid="vertex-editor-uint64"
        inputMode="numeric"
      />
    </Field>
  );
}
