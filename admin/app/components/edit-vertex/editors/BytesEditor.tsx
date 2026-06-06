import { Field, RadioGroup, Radio, Textarea } from "@fluentui/react-components";
import type { BytesEncoding } from "~/lib/client/usecase/edit-vertex/value-codec";

export interface BytesEditorProps {
  value: string;
  encoding: BytesEncoding;
  onChange: (value: string) => void;
  onEncodingChange: (encoding: BytesEncoding) => void;
}

/**
 * Binary payload editor. The encoding toggle changes how the textarea is
 * interpreted; the value is round-tripped to base64 only at save time so
 * the user can re-edit without precision loss.
 */
export function BytesEditor(props: BytesEditorProps) {
  return (
    <>
      <Field label="bytes encoding">
        <RadioGroup
          layout="horizontal"
          value={props.encoding}
          onChange={(_, data) =>
            props.onEncodingChange(data.value as BytesEncoding)
          }
        >
          <Radio value="hex" label="hex (0x…)" />
          <Radio value="base64" label="base64" />
        </RadioGroup>
      </Field>
      <Field label="bytes value" hint="Whitespace is ignored.">
        <Textarea
          value={props.value}
          onChange={(_, data) => props.onChange(data.value)}
          rows={3}
          resize="vertical"
          data-testid="vertex-editor-bytes"
        />
      </Field>
    </>
  );
}
