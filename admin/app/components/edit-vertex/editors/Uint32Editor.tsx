import { NumberEditor } from "./NumberEditor";

export interface Uint32EditorProps {
  value: string;
  onChange: (value: string) => void;
}

export function Uint32Editor(props: Uint32EditorProps) {
  return (
    <NumberEditor
      label="uint32 value"
      value={props.value}
      onChange={props.onChange}
      hint="Unsigned 32-bit integer (0 … 4,294,967,295)."
      step="1"
    />
  );
}
