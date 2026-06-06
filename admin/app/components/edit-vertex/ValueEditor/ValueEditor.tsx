import { BoolEditor } from "../editors/BoolEditor";
import { BytesEditor } from "../editors/BytesEditor";
import { DurationEditor } from "../editors/DurationEditor";
import { Float32Editor } from "../editors/Float32Editor";
import { Float64Editor } from "../editors/Float64Editor";
import { Int32Editor } from "../editors/Int32Editor";
import { Int64Editor } from "../editors/Int64Editor";
import { NilEditor } from "../editors/NilEditor";
import { StringEditor } from "../editors/StringEditor";
import { TimestampEditor } from "../editors/TimestampEditor";
import { Uint32Editor } from "../editors/Uint32Editor";
import { Uint64Editor } from "../editors/Uint64Editor";
import type {
  BytesEncoding,
  VertexInputs,
  VertexValueKind,
} from "~/lib/client/usecase/edit-vertex/value-codec";

export interface ValueEditorProps {
  kind: VertexValueKind;
  inputs: VertexInputs;
  onTextInput: (field: keyof VertexInputs, value: string) => void;
  onBoolChange: (value: boolean) => void;
  onBytesEncodingChange: (value: BytesEncoding) => void;
}

/**
 * Picks the right variant editor based on the active kind. Each branch
 * forwards only the inputs that variant uses, so the per-kind editors
 * stay focused on their single responsibility.
 */
export function ValueEditor(props: ValueEditorProps) {
  switch (props.kind) {
    case "float64":
      return (
        <Float64Editor
          value={props.inputs.float64}
          onChange={(v) => props.onTextInput("float64", v)}
        />
      );
    case "float32":
      return (
        <Float32Editor
          value={props.inputs.float32}
          onChange={(v) => props.onTextInput("float32", v)}
        />
      );
    case "int32":
      return (
        <Int32Editor
          value={props.inputs.int32}
          onChange={(v) => props.onTextInput("int32", v)}
        />
      );
    case "int64":
      return (
        <Int64Editor
          value={props.inputs.int64}
          onChange={(v) => props.onTextInput("int64", v)}
        />
      );
    case "uint32":
      return (
        <Uint32Editor
          value={props.inputs.uint32}
          onChange={(v) => props.onTextInput("uint32", v)}
        />
      );
    case "uint64":
      return (
        <Uint64Editor
          value={props.inputs.uint64}
          onChange={(v) => props.onTextInput("uint64", v)}
        />
      );
    case "bool":
      return (
        <BoolEditor value={props.inputs.bool} onChange={props.onBoolChange} />
      );
    case "string":
      return (
        <StringEditor
          value={props.inputs.string}
          onChange={(v) => props.onTextInput("string", v)}
        />
      );
    case "bytes":
      return (
        <BytesEditor
          value={props.inputs.bytesInput}
          encoding={props.inputs.bytesEncoding}
          onChange={(v) => props.onTextInput("bytesInput", v)}
          onEncodingChange={props.onBytesEncodingChange}
        />
      );
    case "timestamp":
      return (
        <TimestampEditor
          value={props.inputs.timestamp}
          onChange={(v) => props.onTextInput("timestamp", v)}
        />
      );
    case "duration":
      return (
        <DurationEditor
          value={props.inputs.duration}
          onChange={(v) => props.onTextInput("duration", v)}
        />
      );
    case "nil":
      return <NilEditor />;
  }
}
