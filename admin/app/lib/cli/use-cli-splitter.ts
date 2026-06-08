import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type AriaAttributes,
  type DOMAttributes,
  type PointerEvent as ReactPointerEvent,
  type RefObject,
  type KeyboardEvent as ReactKeyboardEvent,
} from "react";
import {
  browserStorage,
  type BrowserStorage,
} from "~/lib/client/infrastructure/browser/storage";
import {
  SPLITTER_DEFAULT_RATIO,
  SPLITTER_STORAGE_KEY,
  clampRatio,
  formatStoredRatio,
  nudgeRatio,
  parseStoredRatio,
} from "./cli-splitter";

export interface UseCliSplitterOptions {
  /**
   * When false, the hook does not register a ResizeObserver and the handle
   * does nothing. Use this to keep state stable while the splitter is hidden
   * (no graph rendered yet).
   */
  enabled: boolean;
  /** Ref to the splitter's grid container; CSS vars are written here. */
  rootRef: RefObject<HTMLDivElement | null>;
  /** Override for tests. Defaults to {@link browserStorage}. */
  storage?: BrowserStorage;
}

type HandleProps = AriaAttributes &
  Pick<
    DOMAttributes<HTMLDivElement>,
    | "onPointerDown"
    | "onPointerMove"
    | "onPointerUp"
    | "onPointerCancel"
    | "onDoubleClick"
    | "onKeyDown"
  > & {
    role: "separator";
    tabIndex: number;
    title: string;
  };

export interface CliSplitterApi {
  /** The current canvas-share ratio (committed value, not the live drag). */
  ratio: number;
  /** True while a pointer drag is in progress. */
  dragging: boolean;
  /** Spread onto the splitter handle element. */
  handleProps: HandleProps;
}

/**
 * Drives the L/R splitter on the /cli page (#465).
 *
 * Writes the canvas fraction to two CSS variables on the host root —
 * `--cli-canvas-frac` and `--cli-right-frac` — so the grid layout updates
 * without re-rendering the React tree on every pointermove. Persists the
 * final ratio to localStorage under {@link SPLITTER_STORAGE_KEY}.
 *
 * The committed `ratio` state is updated only on drag end, keyboard nudge,
 * double-click reset, and ResizeObserver re-clamps — i.e. at most once per
 * gesture, never per frame.
 */
export function useCliSplitter({
  enabled,
  rootRef,
  storage,
}: UseCliSplitterOptions): CliSplitterApi {
  const store = useMemo(() => storage ?? browserStorage(), [storage]);
  const [ratio, setRatio] = useState<number>(() => {
    const stored = parseStoredRatio(store.get(SPLITTER_STORAGE_KEY));
    return stored ?? SPLITTER_DEFAULT_RATIO;
  });
  const [dragging, setDragging] = useState(false);
  const liveRatioRef = useRef(ratio);
  const widthRef = useRef(0);

  // Apply CSS vars whenever the committed ratio changes. During a drag we
  // also write them directly from onPointerMove, but that path doesn't go
  // through state so this effect doesn't fight with it.
  useEffect(() => {
    liveRatioRef.current = ratio;
    const root = rootRef.current;
    if (!root) return;
    root.style.setProperty("--cli-canvas-frac", `${ratio}fr`);
    root.style.setProperty("--cli-right-frac", `${1 - ratio}fr`);
  }, [ratio, rootRef]);

  // Observe container width so the ratio re-clamps when the window shrinks.
  // Disabled while the splitter is hidden so we don't pay observer cost on
  // the empty-canvas state.
  useEffect(() => {
    if (!enabled) return;
    const root = rootRef.current;
    if (!root || typeof ResizeObserver === "undefined") return;
    const obs = new ResizeObserver((entries) => {
      const entry = entries[0];
      const w = entry?.contentRect.width ?? root.getBoundingClientRect().width;
      widthRef.current = w;
      const clamped = clampRatio(liveRatioRef.current, w);
      if (clamped !== liveRatioRef.current) {
        liveRatioRef.current = clamped;
        setRatio(clamped);
      }
    });
    obs.observe(root);
    return () => obs.disconnect();
  }, [enabled, rootRef]);

  const persist = useCallback(
    (next: number) => {
      liveRatioRef.current = next;
      setRatio(next);
      store.set(SPLITTER_STORAGE_KEY, formatStoredRatio(next));
    },
    [store],
  );

  const onPointerDown = useCallback(
    (e: ReactPointerEvent<HTMLDivElement>) => {
      if (!enabled) return;
      e.preventDefault();
      try {
        e.currentTarget.setPointerCapture(e.pointerId);
      } catch {
        // Some browsers throw if the pointer is already captured; non-fatal.
      }
      setDragging(true);
      if (typeof document !== "undefined") {
        document.body.style.cursor = "col-resize";
      }
    },
    [enabled],
  );

  const onPointerMove = useCallback(
    (e: ReactPointerEvent<HTMLDivElement>) => {
      if (!dragging) return;
      const root = rootRef.current;
      if (!root) return;
      const rect = root.getBoundingClientRect();
      if (rect.width <= 0) return;
      const desired = (e.clientX - rect.left) / rect.width;
      const next = clampRatio(desired, rect.width);
      liveRatioRef.current = next;
      root.style.setProperty("--cli-canvas-frac", `${next}fr`);
      root.style.setProperty("--cli-right-frac", `${1 - next}fr`);
      e.currentTarget.setAttribute(
        "aria-valuenow",
        String(Math.round(next * 100)),
      );
    },
    [dragging, rootRef],
  );

  const onPointerUp = useCallback(
    (e: ReactPointerEvent<HTMLDivElement>) => {
      if (!dragging) return;
      setDragging(false);
      if (typeof document !== "undefined") {
        document.body.style.cursor = "";
      }
      try {
        e.currentTarget.releasePointerCapture(e.pointerId);
      } catch {
        // Safe to ignore — capture may have already been released by the browser.
      }
      persist(liveRatioRef.current);
    },
    [dragging, persist],
  );

  const onDoubleClick = useCallback(() => {
    if (!enabled) return;
    persist(SPLITTER_DEFAULT_RATIO);
    store.remove(SPLITTER_STORAGE_KEY);
  }, [enabled, persist, store]);

  const onKeyDown = useCallback(
    (e: ReactKeyboardEvent<HTMLDivElement>) => {
      if (!enabled) return;
      const next = nudgeRatio(liveRatioRef.current, e.key, {
        shiftKey: e.shiftKey,
      });
      if (next === null) return;
      e.preventDefault();
      const root = rootRef.current;
      const w = root ? root.getBoundingClientRect().width : widthRef.current;
      persist(clampRatio(next, w));
    },
    [enabled, persist, rootRef],
  );

  return {
    ratio,
    dragging,
    handleProps: {
      role: "separator",
      "aria-orientation": "vertical",
      "aria-valuenow": Math.round(ratio * 100),
      "aria-valuemin": 0,
      "aria-valuemax": 100,
      tabIndex: 0,
      title: "Drag to resize · double-click to reset",
      onPointerDown,
      onPointerMove,
      onPointerUp,
      onPointerCancel: onPointerUp,
      onDoubleClick,
      onKeyDown,
    },
  };
}
