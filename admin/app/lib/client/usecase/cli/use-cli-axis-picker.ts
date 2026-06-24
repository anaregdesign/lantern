import { useCallback, useEffect, useMemo, useState } from "react";
import {
  browserStorage,
  type BrowserStorage,
} from "~/lib/client/infrastructure/browser/storage";
import {
  AXIS_STORAGE_KEYS,
  CLI_CLICK_AXIS_DEFAULTS,
  formatStoredFloat,
  formatStoredK,
  formatStoredStep,
  parseStoredAlgorithm,
  parseStoredFloat,
  parseStoredK,
  parseStoredObjective,
  parseStoredPrefix,
  parseStoredStep,
  parseStoredWeighting,
  type CliClickAxes,
} from "~/lib/cli/illuminate-axes";

export interface UseCliAxisPickerOptions {
  /** Override for tests. Defaults to {@link browserStorage}. */
  storage?: BrowserStorage;
}

export interface CliAxisPickerApi {
  axes: CliClickAxes;
  setAxis<K extends keyof CliClickAxes>(key: K, value: CliClickAxes[K]): void;
}

/**
 * Owns the click-to-illuminate axis picker state on the /cli page (#464).
 *
 * The initial render lazily hydrates from localStorage so the picker
 * survives page reloads — a refresh is the most common way an operator
 * loses an in-flight exploration session, and the previous hard-coded
 * `step:2 k:5` click ignored any prior tuning. Each setter writes back
 * synchronously; invalid or out-of-range stored values are silently
 * replaced with the documented defaults so a corrupted entry never
 * crashes the page.
 *
 * The hook deliberately mirrors {@link useCliSplitter}: storage is
 * injectable for tests, the React surface is one piece of state plus
 * one setter, and DOM-coupled wiring (Fluent UI Dropdowns / Switches)
 * lives in the component that consumes the hook — not here.
 */
export function useCliAxisPicker(
  options: UseCliAxisPickerOptions = {},
): CliAxisPickerApi {
  const store = useMemo(
    () => options.storage ?? browserStorage(),
    [options.storage],
  );

  const [axes, setAxes] = useState<CliClickAxes>(() => ({
    step:
      parseStoredStep(store.get(AXIS_STORAGE_KEYS.step)) ??
      CLI_CLICK_AXIS_DEFAULTS.step,
    k:
      parseStoredK(store.get(AXIS_STORAGE_KEYS.k)) ?? CLI_CLICK_AXIS_DEFAULTS.k,
    algorithm:
      parseStoredAlgorithm(store.get(AXIS_STORAGE_KEYS.algorithm)) ??
      CLI_CLICK_AXIS_DEFAULTS.algorithm,
    objective:
      parseStoredObjective(store.get(AXIS_STORAGE_KEYS.objective)) ??
      CLI_CLICK_AXIS_DEFAULTS.objective,
    weighting:
      parseStoredWeighting(store.get(AXIS_STORAGE_KEYS.weighting)) ??
      CLI_CLICK_AXIS_DEFAULTS.weighting,
    vertexPrefix:
      parseStoredPrefix(store.get(AXIS_STORAGE_KEYS.vertexPrefix)) ??
      CLI_CLICK_AXIS_DEFAULTS.vertexPrefix,
    restartProb:
      parseStoredFloat(store.get(AXIS_STORAGE_KEYS.restartProb)) ??
      CLI_CLICK_AXIS_DEFAULTS.restartProb,
    epsilon:
      parseStoredFloat(store.get(AXIS_STORAGE_KEYS.epsilon)) ??
      CLI_CLICK_AXIS_DEFAULTS.epsilon,
  }));

  // Re-persist whenever the axes change. We persist all eight every time
  // rather than diffing because the picker fires at most once per user
  // gesture — a Dropdown selection, a Switch toggle, or a keystroke in the
  // prefix field — so the cost is negligible and avoids subtle bugs from
  // forgetting to persist a key after splitting setters apart.
  useEffect(() => {
    store.set(AXIS_STORAGE_KEYS.step, formatStoredStep(axes.step));
    store.set(AXIS_STORAGE_KEYS.k, formatStoredK(axes.k));
    store.set(AXIS_STORAGE_KEYS.algorithm, axes.algorithm);
    store.set(AXIS_STORAGE_KEYS.objective, axes.objective);
    store.set(AXIS_STORAGE_KEYS.weighting, axes.weighting);
    store.set(AXIS_STORAGE_KEYS.vertexPrefix, axes.vertexPrefix);
    store.set(
      AXIS_STORAGE_KEYS.restartProb,
      formatStoredFloat(axes.restartProb),
    );
    store.set(AXIS_STORAGE_KEYS.epsilon, formatStoredFloat(axes.epsilon));
  }, [axes, store]);

  const setAxis = useCallback(
    <K extends keyof CliClickAxes>(key: K, value: CliClickAxes[K]) => {
      setAxes((prev) =>
        Object.is(prev[key], value) ? prev : { ...prev, [key]: value },
      );
    },
    [],
  );

  return { axes, setAxis };
}
