import { Field, Switch } from "@fluentui/react-components";

export interface BoolEditorProps {
  value: boolean;
  onChange: (value: boolean) => void;
}

export function BoolEditor(props: BoolEditorProps) {
  return (
    <Field label="bool value">
      <Switch
        checked={props.value}
        label={props.value ? "true" : "false"}
        onChange={(_, data) => props.onChange(data.checked)}
        data-testid="vertex-editor-bool"
      />
    </Field>
  );
}
