import { Dropdown, Field, Option } from "@fluentui/react-components";
import {
  VERTEX_VALUE_KINDS,
  type VertexValueKind,
} from "~/lib/client/usecase/edit-vertex/value-codec";

export interface KindSelectorProps {
  value: VertexValueKind;
  onChange: (value: VertexValueKind) => void;
  /** When true the selector is disabled (view mode). */
  disabled?: boolean;
}

/**
 * Drop-down picker over the 12 `v1Vertex` oneof variants. Order matches
 * the codec; the active value is the controlled string.
 */
export function KindSelector(props: KindSelectorProps) {
  return (
    <Field label="value kind">
      <Dropdown
        value={props.value}
        selectedOptions={[props.value]}
        disabled={props.disabled}
        onOptionSelect={(_, data) => {
          if (!data.optionValue) return;
          props.onChange(data.optionValue as VertexValueKind);
        }}
        data-testid="vertex-kind-selector"
      >
        {VERTEX_VALUE_KINDS.map((kind) => (
          <Option key={kind} value={kind}>
            {kind}
          </Option>
        ))}
      </Dropdown>
    </Field>
  );
}
