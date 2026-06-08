import {
  Dropdown,
  Input,
  Option,
  Switch,
  type InputProps,
} from "@fluentui/react-components";
import { useCallback } from "react";
import {
  CLI_ALGORITHMS,
  CLI_CLICK_K_MAX,
  CLI_CLICK_K_MIN,
  CLI_CLICK_STEP_MAX,
  CLI_CLICK_STEP_MIN,
  CLI_OBJECTIVES,
  formatIlluminateClick,
  type CliClickAxes,
} from "~/lib/cli/illuminate-axes";
import type {
  AlgorithmName,
  ObjectiveName,
  WeightingName,
} from "~/lib/cli/types";
import styles from "./CliAxisPicker.module.css";

export interface CliAxisPickerProps {
  axes: CliClickAxes;
  setAxis<K extends keyof CliClickAxes>(key: K, value: CliClickAxes[K]): void;
  /**
   * Disable the strip while a command is in flight (#433). The hook
   * state itself is preserved; only the controls go read-only so a
   * mid-flight click cannot mutate the next click string.
   */
  disabled?: boolean;
}

/**
 * Click-to-illuminate axis picker (#464).
 *
 * Renders a single-line strip of Fluent UI primitives that map 1:1 to
 * the optional kwargs of the long-form illuminate verb (post-#410):
 * step, k, algorithm, objective, and a Raw/TF-IDF Switch. Wraps on
 * narrow viewports without horizontal scroll, per the architecture
 * skill's responsive guidance.
 *
 * The component owns no business state — every change goes through
 * {@link CliAxisPickerProps.setAxis}, which the parent hook
 * (`useCliAxisPicker`) persists. Bounds are sourced from the same
 * registry the formatter uses so picker UI, persistence, and command
 * formatting never disagree on the legal range.
 */
export function CliAxisPicker({ axes, setAxis, disabled }: CliAxisPickerProps) {
  const onStepChange = useCallback<NonNullable<InputProps["onChange"]>>(
    (_, data) => {
      const n = Number.parseInt(data.value, 10);
      if (!Number.isInteger(n)) return;
      if (n < CLI_CLICK_STEP_MIN || n > CLI_CLICK_STEP_MAX) return;
      setAxis("step", n);
    },
    [setAxis],
  );

  const onKChange = useCallback<NonNullable<InputProps["onChange"]>>(
    (_, data) => {
      const n = Number.parseInt(data.value, 10);
      if (!Number.isInteger(n)) return;
      if (n < CLI_CLICK_K_MIN || n > CLI_CLICK_K_MAX) return;
      setAxis("k", n);
    },
    [setAxis],
  );

  const preview = formatIlluminateClick("<key>", axes);

  return (
    <div
      className={styles.strip}
      data-testid="cli-axis-picker"
      role="group"
      aria-label="Click-to-illuminate axes"
      aria-describedby="cli-axis-picker-preview"
    >
      <label className={styles.field}>
        <span className={styles.label}>step</span>
        <Input
          className={styles.numberInput}
          type="number"
          min={CLI_CLICK_STEP_MIN}
          max={CLI_CLICK_STEP_MAX}
          value={String(axes.step)}
          onChange={onStepChange}
          disabled={disabled}
          data-testid="cli-axis-step"
          aria-label={`Step (${CLI_CLICK_STEP_MIN}–${CLI_CLICK_STEP_MAX})`}
        />
      </label>

      <label className={styles.field}>
        <span className={styles.label}>k</span>
        <Input
          className={styles.numberInput}
          type="number"
          min={CLI_CLICK_K_MIN}
          max={CLI_CLICK_K_MAX}
          value={String(axes.k)}
          onChange={onKChange}
          disabled={disabled}
          data-testid="cli-axis-k"
          aria-label={`K (${CLI_CLICK_K_MIN}–${CLI_CLICK_K_MAX})`}
        />
      </label>

      <label className={styles.field}>
        <span className={styles.label}>algorithm</span>
        <Dropdown
          className={styles.dropdown}
          value={labelFor(CLI_ALGORITHMS, axes.algorithm)}
          selectedOptions={[axes.algorithm]}
          disabled={disabled}
          onOptionSelect={(_, data) => {
            if (!data.optionValue) return;
            setAxis("algorithm", data.optionValue as AlgorithmName);
          }}
          data-testid="cli-axis-algorithm"
          aria-label="Algorithm"
        >
          {CLI_ALGORITHMS.map((opt) => (
            <Option key={opt.value} value={opt.value}>
              {opt.label}
            </Option>
          ))}
        </Dropdown>
      </label>

      <label className={styles.field}>
        <span className={styles.label}>objective</span>
        <Dropdown
          className={styles.dropdown}
          value={labelFor(CLI_OBJECTIVES, axes.objective)}
          selectedOptions={[axes.objective]}
          disabled={disabled}
          onOptionSelect={(_, data) => {
            if (!data.optionValue) return;
            setAxis("objective", data.optionValue as ObjectiveName);
          }}
          data-testid="cli-axis-objective"
          aria-label="Objective"
        >
          {CLI_OBJECTIVES.map((opt) => (
            <Option key={opt.value} value={opt.value}>
              {opt.label}
            </Option>
          ))}
        </Dropdown>
      </label>

      <Switch
        className={styles.tfidf}
        label="TF-IDF"
        checked={axes.weighting === "tfidf"}
        disabled={disabled}
        onChange={(_, data) =>
          setAxis<"weighting">(
            "weighting",
            data.checked ? "tfidf" : ("raw" as WeightingName),
          )
        }
        data-testid="cli-axis-tfidf"
      />

      <code
        id="cli-axis-picker-preview"
        className={styles.preview}
        data-testid="cli-axis-preview"
      >
        {preview}
      </code>
    </div>
  );
}

function labelFor<T extends string>(
  options: ReadonlyArray<{ value: T; label: string }>,
  value: T,
): string {
  for (const opt of options) {
    if (opt.value === value) return opt.label;
  }
  return value;
}
