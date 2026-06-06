import { Field, Input } from "@fluentui/react-components";

export interface DurationEditorProps {
  value: string;
  onChange: (value: string) => void;
}

export function DurationEditor(props: DurationEditorProps) {
  return (
    <Field
      label="duration value"
      hint="Go syntax: e.g. 5m, 1h30m, 750ms."
    >
      <Input
        value={props.value}
        onChange={(_, data) => props.onChange(data.value)}
        placeholder="1h30m"
        data-testid="vertex-editor-duration"
      />
    </Field>
  );
}
