import {
  Dropdown,
  Input,
  Option,
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
  CLI_WEIGHTINGS,
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
 * step, k, algorithm, objective, and a raw/TF-IDF/BM25 weighting
 * Dropdown, plus a free-text prefix filter. When a push-based family is
 * selected (algorithm=ppr or algorithm=community), two extra numeric
 * inputs (restart_prob / epsilon) appear for the shared locality knobs
 * (#801/#942); a blank knob means "server default", and the step input is
 * disabled because neither family gives it a wire meaning. Wraps on narrow
 * viewports without horizontal scroll, per the architecture skill's
 * responsive guidance.
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

  // Free-text axis (#617): any string is valid, including "" (= no filter).
  // The formatter only echoes a non-empty prefix, so clearing the field
  // restores the canonical short-form click.
  const onPrefixChange = useCallback<NonNullable<InputProps["onChange"]>>(
    (_, data) => {
      setAxis("vertexPrefix", data.value);
    },
    [setAxis],
  );

  // #801: PPR knobs are non-negative floats; an empty field means "0 = server
  // default". Reject NaN / negative entries (the server owns the (0,1) / >0
  // bounds) so a stray keystroke never persists a nonsensical knob.
  const onRestartProbChange = useCallback<NonNullable<InputProps["onChange"]>>(
    (_, data) => {
      if (data.value === "") {
        setAxis("restartProb", 0);
        return;
      }
      const n = Number.parseFloat(data.value);
      if (!Number.isFinite(n) || n < 0) return;
      setAxis("restartProb", n);
    },
    [setAxis],
  );

  const onEpsilonChange = useCallback<NonNullable<InputProps["onChange"]>>(
    (_, data) => {
      if (data.value === "") {
        setAxis("epsilon", 0);
        return;
      }
      const n = Number.parseFloat(data.value);
      if (!Number.isFinite(n) || n < 0) return;
      setAxis("epsilon", n);
    },
    [setAxis],
  );

  const preview = formatIlluminateClick("<key>", axes);

  // #801/#942: ppr and community are the two push-based families that carry
  // the α/ε locality knobs; they also give the `step` axis no wire meaning.
  const isPushFamily =
    axes.algorithm === "ppr" || axes.algorithm === "community";
  // #942: for the community family `k` is the max_size UPPER BOUND (the
  // conductance sweep may stop earlier), not an exact neighbour count.
  const kTitle =
    axes.algorithm === "community"
      ? "k: max community size (upper bound; the sweep may stop earlier)"
      : "k: top-k neighbours kept per hop";

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
          disabled={disabled || isPushFamily}
          data-testid="cli-axis-step"
          aria-label={`Step (${CLI_CLICK_STEP_MIN}–${CLI_CLICK_STEP_MAX})`}
          title={
            isPushFamily
              ? "step has no meaning for the ppr / community families"
              : undefined
          }
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
          title={kTitle}
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

      <label className={styles.field}>
        <span className={styles.label}>weighting</span>
        <Dropdown
          className={styles.dropdown}
          value={labelFor(CLI_WEIGHTINGS, axes.weighting)}
          selectedOptions={[axes.weighting]}
          disabled={disabled}
          onOptionSelect={(_, data) => {
            if (!data.optionValue) return;
            setAxis("weighting", data.optionValue as WeightingName);
          }}
          data-testid="cli-axis-weighting"
          aria-label="Weighting"
        >
          {CLI_WEIGHTINGS.map((opt) => (
            <Option key={opt.value} value={opt.value}>
              {opt.label}
            </Option>
          ))}
        </Dropdown>
      </label>

      <label className={styles.field}>
        <span className={styles.label}>prefix</span>
        <Input
          className={styles.textInput}
          type="text"
          value={axes.vertexPrefix}
          onChange={onPrefixChange}
          disabled={disabled}
          placeholder="(none)"
          data-testid="cli-axis-prefix"
          aria-label="Vertex prefix (optional)"
        />
      </label>

      {isPushFamily && (
        <>
          <label className={styles.field}>
            <span className={styles.label}>restart_prob</span>
            <Input
              className={styles.numberInput}
              type="number"
              min={0}
              max={1}
              step="any"
              value={axes.restartProb > 0 ? String(axes.restartProb) : ""}
              onChange={onRestartProbChange}
              disabled={disabled}
              placeholder="0.15"
              data-testid="cli-axis-restart-prob"
              aria-label="Restart probability α (0–1; blank = server default)"
            />
          </label>

          <label className={styles.field}>
            <span className={styles.label}>epsilon</span>
            <Input
              className={styles.numberInput}
              type="number"
              min={0}
              step="any"
              value={axes.epsilon > 0 ? String(axes.epsilon) : ""}
              onChange={onEpsilonChange}
              disabled={disabled}
              placeholder="1e-4"
              data-testid="cli-axis-epsilon"
              aria-label="Residual threshold ε (> 0; blank = server default)"
            />
          </label>
        </>
      )}

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
