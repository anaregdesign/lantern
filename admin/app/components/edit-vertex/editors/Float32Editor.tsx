import { NumberEditor } from "./NumberEditor";

export interface Float32EditorProps {
  value: string;
  onChange: (value: string) => void;
}

export function Float32Editor(props: Float32EditorProps) {
  return (
    <NumberEditor
      label="float32 value"
      value={props.value}
      onChange={props.onChange}
      hint="IEEE 754 single-precision (server-side narrowed)."
    />
  );
}
