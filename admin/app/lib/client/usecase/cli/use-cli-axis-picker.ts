import { useCallback, useEffect, useMemo, useState } from "react";
import {
  browserStorage,
  type BrowserStorage,
} from "~/lib/client/infrastructure/browser/storage";
import {
  AXIS_STORAGE_KEYS,
  migrateLegacyCliClickPickerState,
  parseStoredCliClickPickerState,
  replaceCliClickFamilyAxes,
  selectCliClickFamily,
  serialiseCliClickPickerState,
  type CliClickAxes,
  type CliClickPickerState,
} from "~/lib/cli/illuminate-axes";
import type { AlgorithmName } from "~/lib/cli/types";

export interface UseCliAxisPickerOptions {
  /** Override for tests. Defaults to {@link browserStorage}. */
  storage?: BrowserStorage;
}

export interface CliAxisPickerApi {
  /** The selected family's native controls and no irrelevant fields. */
  axes: CliClickAxes;
  selectFamily(family: AlgorithmName): void;
  setAxes(axes: CliClickAxes): void;
}

/**
 * Owns the family-native picker state. A versioned localStorage snapshot keeps
 * one last-used value set per family, so switching from PageRank to BFS never
 * turns `top_n` into `fan_out` or overwrites the PageRank configuration.
 *
 * The first hydration migrates the retired flat keys. Its ambiguous `k` is
 * applied solely to whichever family was selected at migration time; every
 * other family gets its documented default. The v2 snapshot then takes
 * precedence on all later loads.
 */
export function useCliAxisPicker(
  options: UseCliAxisPickerOptions = {},
): CliAxisPickerApi {
  const store = useMemo(
    () => options.storage ?? browserStorage(),
    [options.storage],
  );
  const [state, setState] = useState<CliClickPickerState>(() => {
    return (
      parseStoredCliClickPickerState(store.get(AXIS_STORAGE_KEYS.state)) ??
      migrateLegacyCliClickPickerState(store)
    );
  });

  useEffect(() => {
    store.set(AXIS_STORAGE_KEYS.state, serialiseCliClickPickerState(state));
  }, [state, store]);

  const selectFamily = useCallback((family: AlgorithmName) => {
    setState((previous) => selectCliClickFamily(previous, family));
  }, []);

  const setAxes = useCallback((axes: CliClickAxes) => {
    setState((previous) => replaceCliClickFamilyAxes(previous, axes));
  }, []);

  return { axes: state.families[state.selectedFamily], selectFamily, setAxes };
}
