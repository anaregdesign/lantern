import { Field, Input } from "@fluentui/react-components";

export interface NumberEditorProps {
  label: string;
  value: string;
  onChange: (value: string) => void;
  hint?: string;
  /** Native `step` for the spinner UI. */
  step?: string;
}

/**
 * Shared base for `float64`/`float32` editors. `type="number"` keeps the
 * mobile-friendly numeric keypad while letting users paste raw values.
 */
export function NumberEditor(props: NumberEditorProps) {
  return (
    <Field label={props.label} hint={props.hint}>
      <Input
        type="number"
        value={props.value}
        step={props.step ?? "any"}
        onChange={(_, data) => props.onChange(data.value)}
        data-testid={`vertex-editor-${props.label}`}
      />
    </Field>
  );
}
