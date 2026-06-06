import { Field, Textarea } from "@fluentui/react-components";

export interface StringEditorProps {
  value: string;
  onChange: (value: string) => void;
}

export function StringEditor(props: StringEditorProps) {
  return (
    <Field label="string value">
      <Textarea
        value={props.value}
        onChange={(_, data) => props.onChange(data.value)}
        rows={4}
        resize="vertical"
        data-testid="vertex-editor-string"
      />
    </Field>
  );
}
