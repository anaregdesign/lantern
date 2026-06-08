import {
  Combobox,
  Field,
  Option,
  Text,
  type ComboboxProps,
} from "@fluentui/react-components";
import { useCallback } from "react";
import { selectCaption } from "~/lib/client/usecase/vertex-picker/selectors";
import {
  useVertexPicker,
  type UseVertexPickerOptions,
} from "~/lib/client/usecase/vertex-picker/use-vertex-picker";
import styles from "./VertexPicker.module.css";

export interface VertexPickerProps {
  /** The current (live, un-debounced) prefix text. Fully controlled. */
  value: string;
  /** Called on every keystroke with the new prefix text. */
  onValueChange: (next: string) => void;
  /** Called when a suggestion is committed (click, ↵, or Tab). */
  onSelect: (key: string) => void;
  label?: string;
  placeholder?: string;
  autoFocus?: boolean;
  disabled?: boolean;
  /** Test id forwarded to the underlying `<input>`. */
  inputTestId?: string;
  /** Test id forwarded to the caption line. */
  captionTestId?: string;
  /** Tuning knobs forwarded to {@link useVertexPicker}. */
  pickerOptions?: UseVertexPickerOptions;
}

/**
 * Shared type-ahead picker for vertex keys (#457).
 *
 * Backed by ScanVertices (suggestions) + CountVerticesByPrefix
 * ("N matches"), both debounced and cancellable via
 * {@link useVertexPicker}. Built on a Fluent `Combobox` so ↑/↓ navigation,
 * Esc-to-dismiss, and Tab-to-commit come for free; choosing an option
 * (click, ↵, or Tab) calls {@link VertexPickerProps.onSelect}. The live
 * input text is fully controlled by the parent, which decides what a
 * commit means — open a new seed (SeedPrompt) or expand from a key
 * (IlluminateToolbar).
 */
export function VertexPicker({
  value,
  onValueChange,
  onSelect,
  label = "Vertex key",
  placeholder = "Type at least 1 character…",
  autoFocus,
  disabled,
  inputTestId,
  captionTestId,
  pickerOptions,
}: VertexPickerProps) {
  const { state, suggestions } = useVertexPicker(value, pickerOptions);

  const onChange = useCallback<NonNullable<ComboboxProps["onChange"]>>(
    (event) => {
      onValueChange(event.target.value);
    },
    [onValueChange],
  );

  const onOptionSelect = useCallback<
    NonNullable<ComboboxProps["onOptionSelect"]>
  >(
    (_event, data) => {
      const key = data.optionValue;
      if (!key) {
        return;
      }
      onValueChange(key);
      onSelect(key);
    },
    [onSelect, onValueChange],
  );

  const caption = selectCaption(state);

  return (
    <Field label={label} className={styles.field}>
      <Combobox
        freeform
        value={value}
        placeholder={placeholder}
        autoFocus={autoFocus}
        disabled={disabled}
        onChange={onChange}
        onOptionSelect={onOptionSelect}
        input={
          inputTestId
            ? ({ "data-testid": inputTestId } as ComboboxProps["input"])
            : undefined
        }
      >
        {suggestions.map((key) => (
          <Option key={key} value={key} text={key}>
            {key}
          </Option>
        ))}
      </Combobox>
      <Text
        size={200}
        aria-live="polite"
        className={styles.caption}
        data-testid={captionTestId}
      >
        {caption}
      </Text>
    </Field>
  );
}
