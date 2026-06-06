import { NumberEditor } from "./NumberEditor";

export interface Int32EditorProps {
  value: string;
  onChange: (value: string) => void;
}

export function Int32Editor(props: Int32EditorProps) {
  return (
    <NumberEditor
      label="int32 value"
      value={props.value}
      onChange={props.onChange}
      hint="Signed 32-bit integer (−2,147,483,648 … 2,147,483,647)."
      step="1"
    />
  );
}
