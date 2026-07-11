import {
  Dropdown,
  Field,
  Input,
  Option,
  type InputProps,
} from "@fluentui/react-components";
import { useCallback, useEffect, useState } from "react";
import {
  CLI_ALGORITHMS,
  CLI_CLICK_BFS_FAN_OUT_MAX,
  CLI_CLICK_BFS_FAN_OUT_MIN,
  CLI_CLICK_BFS_STEP_MAX,
  CLI_CLICK_BFS_STEP_MIN,
  CLI_CLICK_MAX_SIZE_MAX,
  CLI_CLICK_MAX_SIZE_MIN,
  CLI_CLICK_TOP_N_MAX,
  CLI_CLICK_TOP_N_MIN,
  CLI_OBJECTIVES,
  CLI_REDUCTIONS,
  CLI_WEIGHTINGS,
  formatFamilyClick,
  isReadyPushKnob,
  type BfsCliClickAxes,
  type CliClickAxes,
  type CommunityCliClickAxes,
  type PagerankCliClickAxes,
  validateEpsilonInput,
  validateRestartProbInput,
} from "~/lib/cli/illuminate-axes";
import type {
  AlgorithmName,
  ObjectiveName,
  ReductionName,
  WeightingName,
} from "~/lib/cli/types";
import styles from "./CliAxisPicker.module.css";

export interface CliAxisPickerProps {
  axes: CliClickAxes;
  selectFamily(family: AlgorithmName): void;
  setAxes(axes: CliClickAxes): void;
  disabled?: boolean;
  /** Lets the canvas block a click while an α/ε draft is incomplete. */
  onPushKnobValidityChange(valid: boolean): void;
}

/**
 * Family-native controls for click-to-explore. The selected family is visible
 * before a graph exists and TypeScript narrows every child to the controls its
 * command grammar actually understands.
 */
export function CliAxisPicker({
  axes,
  selectFamily,
  setAxes,
  disabled,
  onPushKnobValidityChange,
}: CliAxisPickerProps) {
  const [isPushCommandValid, setIsPushCommandValid] = useState(true);
  const reportPushKnobValidity = useCallback(
    (valid: boolean) => {
      setIsPushCommandValid(valid);
      onPushKnobValidityChange(valid);
    },
    [onPushKnobValidityChange],
  );
  useEffect(() => {
    if (axes.family === "bfs") reportPushKnobValidity(true);
  }, [axes.family, reportPushKnobValidity]);

  return (
    <div
      className={styles.strip}
      data-testid="cli-axis-picker"
      role="group"
      aria-label="Traversal family and click-to-explore controls"
      aria-describedby="cli-axis-picker-preview"
    >
      <label className={styles.field}>
        <span className={styles.label}>family</span>
        <Dropdown
          className={styles.dropdown}
          value={labelFor(CLI_ALGORITHMS, axes.family)}
          selectedOptions={[axes.family]}
          disabled={disabled}
          onOptionSelect={(_, data) => {
            if (data.optionValue)
              selectFamily(data.optionValue as AlgorithmName);
          }}
          data-testid="cli-axis-algorithm"
          aria-label="Traversal family"
        >
          {CLI_ALGORITHMS.map((option) => (
            <Option key={option.value} value={option.value}>
              {option.label}
            </Option>
          ))}
        </Dropdown>
      </label>

      {axes.family === "bfs" ? (
        <BfsControls axes={axes} setAxes={setAxes} disabled={disabled} />
      ) : axes.family === "pagerank" ? (
        <PagerankControls
          axes={axes}
          setAxes={setAxes}
          disabled={disabled}
          onPushKnobValidityChange={reportPushKnobValidity}
        />
      ) : (
        <CommunityControls
          axes={axes}
          setAxes={setAxes}
          disabled={disabled}
          onPushKnobValidityChange={reportPushKnobValidity}
        />
      )}
      <Preview axes={axes} valid={isPushCommandValid} />
    </div>
  );
}

function BfsControls({
  axes,
  setAxes,
  disabled,
}: {
  axes: BfsCliClickAxes;
  setAxes(axes: CliClickAxes): void;
  disabled?: boolean;
}) {
  return (
    <>
      <NumberField
        label="step"
        value={axes.step}
        min={CLI_CLICK_BFS_STEP_MIN}
        max={CLI_CLICK_BFS_STEP_MAX}
        testId="cli-axis-step"
        disabled={disabled}
        onValue={(step) => setAxes({ ...axes, step })}
      />
      <NumberField
        label="fan_out"
        value={axes.fanOut}
        min={CLI_CLICK_BFS_FAN_OUT_MIN}
        max={CLI_CLICK_BFS_FAN_OUT_MAX}
        testId="cli-axis-k"
        disabled={disabled}
        onValue={(fanOut) => setAxes({ ...axes, fanOut })}
      />
      <TreeControls axes={axes} setAxes={setAxes} disabled={disabled} />
      <SharedControls axes={axes} setAxes={setAxes} disabled={disabled} />
    </>
  );
}

function PagerankControls({
  axes,
  setAxes,
  disabled,
  onPushKnobValidityChange,
}: {
  axes: PagerankCliClickAxes;
  setAxes(axes: CliClickAxes): void;
  disabled?: boolean;
  onPushKnobValidityChange(valid: boolean): void;
}) {
  return (
    <>
      <NumberField
        label="top_n"
        value={axes.topN}
        min={CLI_CLICK_TOP_N_MIN}
        max={CLI_CLICK_TOP_N_MAX}
        testId="cli-axis-k"
        disabled={disabled}
        title="0 returns every positive-mass vertex"
        onValue={(topN) => setAxes({ ...axes, topN })}
      />
      <PushControls
        key={axes.family}
        axes={axes}
        setAxes={setAxes}
        disabled={disabled}
        onPushKnobValidityChange={onPushKnobValidityChange}
      />
      <SharedControls axes={axes} setAxes={setAxes} disabled={disabled} />
    </>
  );
}

function CommunityControls({
  axes,
  setAxes,
  disabled,
  onPushKnobValidityChange,
}: {
  axes: CommunityCliClickAxes;
  setAxes(axes: CliClickAxes): void;
  disabled?: boolean;
  onPushKnobValidityChange(valid: boolean): void;
}) {
  return (
    <>
      <NumberField
        label="max_size"
        value={axes.maxSize}
        min={CLI_CLICK_MAX_SIZE_MIN}
        max={CLI_CLICK_MAX_SIZE_MAX}
        testId="cli-axis-k"
        disabled={disabled}
        title="0 lets the conductance sweep choose the community size"
        onValue={(maxSize) => setAxes({ ...axes, maxSize })}
      />
      <PushControls
        key={axes.family}
        axes={axes}
        setAxes={setAxes}
        disabled={disabled}
        onPushKnobValidityChange={onPushKnobValidityChange}
      />
      <TreeControls axes={axes} setAxes={setAxes} disabled={disabled} />
      <SharedControls axes={axes} setAxes={setAxes} disabled={disabled} />
    </>
  );
}

function NumberField({
  label,
  value,
  min,
  max,
  testId,
  disabled,
  title,
  onValue,
}: {
  label: string;
  value: number;
  min: number;
  max: number;
  testId: string;
  disabled?: boolean;
  title?: string;
  onValue(value: number): void;
}) {
  const onChange = (_: unknown, data: { value: string }) => {
    if (!/^[+-]?\d+$/.test(data.value)) return;
    const value = Number(data.value);
    if (!Number.isSafeInteger(value) || value < min || value > max) return;
    onValue(value);
  };
  return (
    <label className={styles.field}>
      <span className={styles.label}>{label}</span>
      <Input
        className={styles.numberInput}
        type="number"
        min={min}
        max={max}
        value={String(value)}
        onChange={onChange as NonNullable<InputProps["onChange"]>}
        disabled={disabled}
        data-testid={testId}
        aria-label={`${label} (${min}–${max})`}
        title={title}
      />
    </label>
  );
}

function TreeControls({
  axes,
  setAxes,
  disabled,
}: {
  axes: BfsCliClickAxes | CommunityCliClickAxes;
  setAxes(axes: CliClickAxes): void;
  disabled?: boolean;
}) {
  return (
    <>
      <label className={styles.field}>
        <span className={styles.label}>reduction</span>
        <Dropdown
          className={styles.dropdown}
          value={labelFor(CLI_REDUCTIONS, axes.reduction)}
          selectedOptions={[axes.reduction]}
          disabled={disabled}
          onOptionSelect={(_, data) => {
            if (data.optionValue) {
              setAxes({
                ...axes,
                reduction: data.optionValue as ReductionName,
              });
            }
          }}
          data-testid="cli-axis-reduction"
          aria-label="Reduction"
        >
          {CLI_REDUCTIONS.map((option) => (
            <Option key={option.value} value={option.value}>
              {option.label}
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
            if (data.optionValue) {
              setAxes({
                ...axes,
                objective: data.optionValue as ObjectiveName,
              });
            }
          }}
          data-testid="cli-axis-objective"
          aria-label="Objective"
        >
          {CLI_OBJECTIVES.map((option) => (
            <Option key={option.value} value={option.value}>
              {option.label}
            </Option>
          ))}
        </Dropdown>
      </label>
    </>
  );
}

function SharedControls({
  axes,
  setAxes,
  disabled,
}: {
  axes: CliClickAxes;
  setAxes(axes: CliClickAxes): void;
  disabled?: boolean;
}) {
  return (
    <>
      <label className={styles.field}>
        <span className={styles.label}>weighting</span>
        <Dropdown
          className={styles.dropdown}
          value={labelFor(CLI_WEIGHTINGS, axes.weighting)}
          selectedOptions={[axes.weighting]}
          disabled={disabled}
          onOptionSelect={(_, data) => {
            if (data.optionValue) {
              setAxes({
                ...axes,
                weighting: data.optionValue as WeightingName,
              });
            }
          }}
          data-testid="cli-axis-weighting"
          aria-label="Weighting"
        >
          {CLI_WEIGHTINGS.map((option) => (
            <Option key={option.value} value={option.value}>
              {option.label}
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
          onChange={(_, data) => setAxes({ ...axes, vertexPrefix: data.value })}
          disabled={disabled}
          placeholder="(none)"
          data-testid="cli-axis-prefix"
          aria-label="Vertex prefix (optional)"
        />
      </label>
    </>
  );
}

function PushControls({
  axes,
  setAxes,
  disabled,
  onPushKnobValidityChange,
}: {
  axes: PagerankCliClickAxes | CommunityCliClickAxes;
  setAxes(axes: CliClickAxes): void;
  disabled?: boolean;
  onPushKnobValidityChange(valid: boolean): void;
}) {
  const [restartProbText, setRestartProbText] = useState(
    axes.restartProb > 0 ? String(axes.restartProb) : "",
  );
  const [epsilonText, setEpsilonText] = useState(
    axes.epsilon > 0 ? String(axes.epsilon) : "",
  );
  const restartProbValidation = validateRestartProbInput(restartProbText);
  const epsilonValidation = validateEpsilonInput(epsilonText);
  const ready =
    isReadyPushKnob(restartProbValidation) &&
    isReadyPushKnob(epsilonValidation);
  useEffect(() => {
    onPushKnobValidityChange(ready);
  }, [onPushKnobValidityChange, ready]);

  return (
    <>
      <Field
        className={styles.knobField}
        label="restart_prob"
        validationState={
          restartProbValidation.state === "invalid" ? "error" : "none"
        }
        validationMessage={
          restartProbValidation.state === "invalid"
            ? restartProbValidation.message
            : undefined
        }
        data-testid="cli-axis-restart-prob-field"
      >
        <Input
          className={styles.numberInput}
          type="text"
          inputMode="decimal"
          value={restartProbText}
          onChange={(_, data) => {
            setRestartProbText(data.value);
            const validation = validateRestartProbInput(data.value);
            if (isReadyPushKnob(validation)) {
              setAxes({ ...axes, restartProb: validation.value });
            }
          }}
          disabled={disabled}
          placeholder="0.15"
          data-testid="cli-axis-restart-prob"
          aria-label="Restart probability α (0–1; blank = server default)"
        />
      </Field>
      <Field
        className={styles.knobField}
        label="epsilon"
        validationState={
          epsilonValidation.state === "invalid" ? "error" : "none"
        }
        validationMessage={
          epsilonValidation.state === "invalid"
            ? epsilonValidation.message
            : undefined
        }
        data-testid="cli-axis-epsilon-field"
      >
        <Input
          className={styles.numberInput}
          type="text"
          inputMode="decimal"
          value={epsilonText}
          onChange={(_, data) => {
            setEpsilonText(data.value);
            const validation = validateEpsilonInput(data.value);
            if (isReadyPushKnob(validation)) {
              setAxes({ ...axes, epsilon: validation.value });
            }
          }}
          disabled={disabled}
          placeholder="1e-4"
          data-testid="cli-axis-epsilon"
          aria-label="Residual threshold ε (> 0; blank = server default)"
        />
      </Field>
      {!ready ? (
        <span
          className={styles.blocked}
          data-testid="cli-axis-command-blocked"
          role="status"
        >
          Fix the incomplete or invalid push-knob input before clicking a node.
        </span>
      ) : null}
    </>
  );
}

function Preview({ axes, valid }: { axes: CliClickAxes; valid: boolean }) {
  return (
    <code
      id="cli-axis-picker-preview"
      className={styles.preview}
      data-testid="cli-axis-preview"
    >
      {valid
        ? formatFamilyClick("<key>", axes)
        : "Fix push-knob validation errors before clicking a node."}
    </code>
  );
}

function labelFor<T extends string>(
  options: ReadonlyArray<{ value: T; label: string }>,
  value: T,
): string {
  return options.find((option) => option.value === value)?.label ?? value;
}
