import { NumberEditor } from "./NumberEditor";

export interface Float64EditorProps {
  value: string;
  onChange: (value: string) => void;
}

export function Float64Editor(props: Float64EditorProps) {
  return (
    <NumberEditor
      label="float64 value"
      value={props.value}
      onChange={props.onChange}
      hint="IEEE 754 double-precision."
    />
  );
}
