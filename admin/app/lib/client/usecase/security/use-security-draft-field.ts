import {
  useRef,
  useState,
  type Dispatch,
  type SetStateAction,
  type RefObject,
} from "react";

/** Capture an editing event before React commit or a concurrent auth abort. */
export function useSecurityDraftField<T>(
  key: string,
  fields: RefObject<Record<string, unknown>>,
  changed: () => void,
  initial: T | (() => T),
): [T, Dispatch<SetStateAction<T>>];
export function useSecurityDraftField<T = undefined>(
  key: string,
  fields: RefObject<Record<string, unknown>>,
  changed: () => void,
): [T | undefined, Dispatch<SetStateAction<T | undefined>>];
export function useSecurityDraftField<T>(
  key: string,
  fields: RefObject<Record<string, unknown>>,
  changed: () => void,
  initial?: T | (() => T),
): [T | undefined, Dispatch<SetStateAction<T | undefined>>] {
  const [value, setValue] = useState(initial);
  const current = useRef(value);
  fields.current[key] = current.current;
  return [
    value,
    (update) => {
      const next =
        typeof update === "function"
          ? (update as (previous: T | undefined) => T | undefined)(
              current.current,
            )
          : update;
      current.current = next;
      fields.current[key] = next;
      changed();
      setValue(next);
    },
  ];
}
